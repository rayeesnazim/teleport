// Copyright 2022 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tracing

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func generateTLSCertificate() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test CA"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv6loopback, net.IPv4(127, 0, 0, 1)},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	var certificateBuffer bytes.Buffer
	if err := pem.Encode(&certificateBuffer, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return tls.Certificate{}, err
	}
	privDERBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	var privBuffer bytes.Buffer
	if err := pem.Encode(&privBuffer, &pem.Block{Type: "PRIVATE KEY", Bytes: privDERBytes}); err != nil {
		return tls.Certificate{}, err
	}

	tlsCertificate, err := tls.X509KeyPair(certificateBuffer.Bytes(), privBuffer.Bytes())
	if err != nil {
		return tls.Certificate{}, err
	}
	return tlsCertificate, nil
}

func TestNewClient(t *testing.T) {
	t.Parallel()
	c, err := NewCollector(CollectorConfig{})
	require.NoError(t, err)

	cfg := Config{
		Service:     "test",
		ExporterURL: c.GRPCAddr(),
		DialTimeout: time.Second,
	}

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		require.NoError(t, c.Shutdown(shutdownCtx))
	})
	go func() {
		c.Start()
	}()

	// NewClient shouldn't fail - it won't attempt to connect to the Collector
	clt, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, clt)

	// Starting the client should be successful when the Collector is up
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, clt.Start(ctx))

	// NewStartedClient will dial the collector, if everything is OK
	// then it should return a valid client
	clt, err = NewStartedClient(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, clt)

	// Stop the Collector
	require.NoError(t, c.Shutdown(context.Background()))

	// NewClient shouldn't fail - it won't attempt to connect to the Collector
	clt, err = NewClient(cfg)
	require.NoError(t, err, "NewClient failed even though it doesn't dial the Collector")
	require.NotNil(t, clt)

	// Starting the client should fail when the Collector is down
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	require.Error(t, clt.Start(ctx2), "Start was successful when the Collector is down")

	// NewStartedClient will dial the collector, if the Collector is offline
	// then it should return an error
	clt, err = NewStartedClient(context.Background(), cfg)
	require.Error(t, err, "NewStartedClient was successful dialing an offline Collector")
	require.Nil(t, clt)
}

func TestNewExporter(t *testing.T) {
	t.Parallel()
	c, err := NewCollector(CollectorConfig{})
	require.NoError(t, err)

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		require.NoError(t, c.Shutdown(shutdownCtx))
	})
	go func() {
		c.Start()
	}()

	cases := []struct {
		name              string
		config            Config
		errAssertion      require.ErrorAssertionFunc
		exporterAssertion require.ValueAssertionFunc
	}{
		{
			name: "invalid config",
			errAssertion: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err, i...)
				require.True(t, trace.IsBadParameter(err), i...)
			},
			exporterAssertion: require.Nil,
		},
		{
			name: "invalid exporter url",
			config: Config{
				Service:     "test",
				ExporterURL: "tcp://localhost:123",
			},
			errAssertion: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err, i...)
				require.True(t, trace.IsBadParameter(err), i...)
			},
			exporterAssertion: require.Nil,
		},
		{
			name: "connection timeout",
			config: Config{
				Service:     "test",
				ExporterURL: "localhost:123",
				DialTimeout: time.Millisecond,
			},
			errAssertion: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err, i...)
				require.True(t, trace.IsConnectionProblem(err), i...)
			},
			exporterAssertion: require.Nil,
		},
		{
			name: "successful explicit grpc exporter",
			config: Config{
				Service:     "test",
				ExporterURL: c.GRPCAddr(),
				DialTimeout: time.Second,
			},
			errAssertion:      require.NoError,
			exporterAssertion: require.NotNil,
		},
		{
			name: "successful inferred grpc exporter",
			config: Config{
				Service:     "test",
				ExporterURL: c.GRPCAddr()[len("grpc://"):],
				DialTimeout: time.Second,
			},
			errAssertion:      require.NoError,
			exporterAssertion: require.NotNil,
		},
		{
			name: "successful http exporter",
			config: Config{
				Service:     "test",
				ExporterURL: c.HTTPAddr(),
				DialTimeout: time.Second,
			},
			errAssertion:      require.NoError,
			exporterAssertion: require.NotNil,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			exporter, err := NewExporter(context.Background(), tt.config)
			tt.errAssertion(t, err)
			tt.exporterAssertion(t, exporter)
		})
	}
}

func TestTraceProvider(t *testing.T) {
	t.Parallel()
	const spansCreated = 4

	tlsCertificate, err := generateTLSCertificate()
	require.NoError(t, err)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCertificate},
	}

	cases := []struct {
		name              string
		config            func(c *Collector) Config
		errAssertion      require.ErrorAssertionFunc
		providerAssertion require.ValueAssertionFunc
		collectedLen      int
		tlsConfig         *tls.Config
	}{
		{
			name: "not sampling prevents exporting",
			config: func(c *Collector) Config {
				return Config{
					Service:     "test",
					ExporterURL: c.GRPCAddr(),
					DialTimeout: time.Second,
					TLSConfig:   c.ClientTLSConfig(),
				}
			},
			errAssertion:      require.NoError,
			providerAssertion: require.NotNil,
			collectedLen:      0,
		},
		{
			name: "spans exported with gRPC+TLS",
			config: func(c *Collector) Config {
				return Config{
					Service:      "test",
					SamplingRate: 1.0,
					ExporterURL:  c.GRPCAddr(),
					DialTimeout:  time.Second,
					TLSConfig:    c.ClientTLSConfig(),
				}
			},
			errAssertion:      require.NoError,
			providerAssertion: require.NotNil,
			collectedLen:      spansCreated,
			tlsConfig:         tlsConfig,
		},
		{
			name: "spans exported with gRPC",
			config: func(c *Collector) Config {
				return Config{
					Service:      "test",
					SamplingRate: 0.5,
					ExporterURL:  c.GRPCAddr(),
					DialTimeout:  time.Second,
					TLSConfig:    c.ClientTLSConfig(),
				}
			},
			errAssertion:      require.NoError,
			providerAssertion: require.NotNil,
			collectedLen:      spansCreated / 2,
		},
		{
			name: "spans exported with HTTP",
			config: func(c *Collector) Config {
				return Config{
					Service:      "test",
					SamplingRate: 1.0,
					ExporterURL:  c.HTTPAddr(),
					DialTimeout:  time.Second,
					TLSConfig:    c.ClientTLSConfig(),
				}
			},
			errAssertion:      require.NoError,
			providerAssertion: require.NotNil,
			collectedLen:      spansCreated,
		},
		{
			name: "spans exported with HTTPS",
			config: func(c *Collector) Config {
				return Config{
					Service:      "test",
					SamplingRate: 1.0,
					ExporterURL:  c.HTTPSAddr(),
					DialTimeout:  time.Second,
					TLSConfig:    c.ClientTLSConfig(),
				}
			},
			errAssertion:      require.NoError,
			providerAssertion: require.NotNil,
			collectedLen:      spansCreated,
			tlsConfig:         tlsConfig,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			collector, err := NewCollector(CollectorConfig{
				TLSConfig: tt.tlsConfig,
			})
			require.NoError(t, err)

			t.Cleanup(func() {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer shutdownCancel()
				require.NoError(t, collector.Shutdown(shutdownCtx))
			})
			go func() {
				collector.Start()
			}()

			ctx := context.Background()
			provider, err := NewTraceProvider(ctx, tt.config(collector))
			tt.errAssertion(t, err)
			tt.providerAssertion(t, provider)

			if err != nil {
				return
			}

			for i := 0; i < spansCreated; i++ {
				_, span := provider.Tracer("test").Start(ctx, fmt.Sprintf("test%d", i))
				span.End()
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, provider.Shutdown(shutdownCtx))
			require.LessOrEqual(t, len(collector.Spans), tt.collectedLen)
			require.GreaterOrEqual(t, len(collector.Spans), 0)
		})
	}
}

func TestConfig_CheckAndSetDefaults(t *testing.T) {
	cases := []struct {
		name           string
		cfg            Config
		errorAssertion require.ErrorAssertionFunc
		expectedCfg    Config
		expectedURL    *url.URL
	}{
		{
			name: "valid config",
			cfg: Config{
				Service:      "test",
				SamplingRate: 1.0,
				ExporterURL:  "http://localhost:8080",
				DialTimeout:  time.Millisecond,
			},
			errorAssertion: require.NoError,
			expectedCfg: Config{
				Service:      "test",
				ExporterURL:  "http://localhost:8080",
				SamplingRate: 1.0,
				DialTimeout:  time.Millisecond,
			},
			expectedURL: &url.URL{
				Scheme: "http",
				Host:   "localhost:8080",
			},
		},
		{
			name: "invalid service",
			cfg: Config{
				Service:      "",
				SamplingRate: 1.0,
				ExporterURL:  "http://localhost:8080",
			},
			errorAssertion: require.Error,
			expectedCfg: Config{
				Service:      "test",
				ExporterURL:  "http://localhost:8080",
				SamplingRate: 1.0,
				DialTimeout:  time.Millisecond,
			},
		},
		{
			name: "invalid exporter url",
			cfg: Config{
				Service:     "test",
				ExporterURL: "",
			},
			errorAssertion: require.Error,
		},
		{
			name: "network address defaults to grpc",
			cfg: Config{
				Service:      "test",
				SamplingRate: 1.0,
				ExporterURL:  "localhost:8080",
				DialTimeout:  time.Millisecond,
			},
			errorAssertion: require.NoError,
			expectedCfg: Config{
				Service:      "test",
				ExporterURL:  "localhost:8080",
				SamplingRate: 1.0,
				DialTimeout:  time.Millisecond,
			},
			expectedURL: &url.URL{
				Scheme: "grpc",
				Host:   "localhost:8080",
			},
		},
		{
			name: "empty scheme defaults to grpc",
			cfg: Config{
				Service:      "test",
				SamplingRate: 1.0,
				ExporterURL:  "exporter.example.com:4317",
				DialTimeout:  time.Millisecond,
			},
			errorAssertion: require.NoError,
			expectedCfg: Config{
				Service:      "test",
				ExporterURL:  "exporter.example.com:4317",
				SamplingRate: 1.0,
				DialTimeout:  time.Millisecond,
			},
			expectedURL: &url.URL{
				Scheme: "grpc",
				Host:   "exporter.example.com:4317",
			},
		},
		{
			name: "timeout defaults to DefaultExporterDialTimeout",
			cfg: Config{
				Service:      "test",
				SamplingRate: 1.0,
				ExporterURL:  "https://localhost:8080",
			},
			errorAssertion: require.NoError,
			expectedCfg: Config{
				Service:      "test",
				ExporterURL:  "https://localhost:8080",
				SamplingRate: 1.0,
				DialTimeout:  DefaultExporterDialTimeout,
			},
			expectedURL: &url.URL{
				Scheme: "https",
				Host:   "localhost:8080",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.CheckAndSetDefaults()
			tt.errorAssertion(t, err)
			if err != nil {
				return
			}
			require.Empty(t, cmp.Diff(tt.expectedCfg, tt.cfg,
				cmpopts.IgnoreUnexported(Config{}),
				cmpopts.IgnoreInterfaces(struct{ logrus.FieldLogger }{})),
			)
			require.NotNil(t, tt.cfg.Logger)
		})
	}
}
