/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/gravitational/teleport/lib/backend"
	kubeutils "github.com/gravitational/teleport/lib/kube/utils"
	"github.com/gravitational/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	applyconfigv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	secretIdentifierName   = "state"
	namespaceEnv           = "POD_NAMESPACE"
	teleportReplicaNameEnv = "TELEPORT_REPLICA_NAME"
	releaseNameEnv         = "RELEASE_NAME"
)

// InKubeCluster detemines if the agent is running inside a Kubernetes cluster and has access to
// service account token and cluster CA. Besides, it also validates the presence of `POD_NAMESPACE`
// and `TELEPORT_REPLICA_NAME` environment variables.
func InKubeCluster() bool {
	_, _, err := kubeutils.GetKubeClient("")

	return err == nil &&
		len(os.Getenv(namespaceEnv)) > 0 &&
		len(os.Getenv(teleportReplicaNameEnv)) > 0

}

// Backend uses Kubernetes Secrets to store identities.
type Backend struct {
	k8sClientSet *kubernetes.Clientset
	namespace    string
	secretName   string

	mu *sync.Mutex
}

// New returns a new instance of Kubernetes Secret identity backend storage.
func New() (*Backend, error) {

	restClient, _, err := kubeutils.GetKubeClient("")
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &Backend{
		k8sClientSet: restClient,
		namespace:    os.Getenv(namespaceEnv),
		secretName: fmt.Sprintf(
			"%s-%s",
			os.Getenv(teleportReplicaNameEnv),
			secretIdentifierName,
		),
		mu: &sync.Mutex{},
	}, nil
}

// Exists checks if the secret already exists in Kubernetes.
// It's used to determine if the agent never created a secret and can be upgrading
// from local SQLite database. In that case. the agent reads local database and
// creates a copy of the keys in Kube Secret.
func (b *Backend) Exists(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, err := b.getSecret(ctx)
	return err == nil
}

// Get reads the secret and extracts the key from it.
// If key not found it returns an error.
func (b *Backend) Get(ctx context.Context, key []byte) (*backend.Item, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.readSecretData(ctx, key)
}

// Create creates item if it does not exist
func (b *Backend) Create(ctx context.Context, i backend.Item) (*backend.Lease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.updateSecretContent(ctx, i)
}

// Put puts value into backend (creates if it does not
// exists, updates it otherwise)
func (b *Backend) Put(ctx context.Context, i backend.Item) (*backend.Lease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.updateSecretContent(ctx, i)
}

// PutItems receives multiple items and upserts them into the Kubernetes Secret.
// This function is only used when the Agent's Secret does not exist, but local SQLite database
// has identity credentials.
// TODO(tigrato): remove this once the compatibility layer between local storage and
// Kube secret storage is no longer required!
func (b *Backend) PutItems(ctx context.Context, items ...backend.Item) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, err := b.updateSecretContent(ctx, items...)
	return err
}

// getSecret reads the secret from K8S API.
func (b *Backend) getSecret(ctx context.Context) (*corev1.Secret, error) {
	return b.k8sClientSet.
		CoreV1().
		Secrets(b.namespace).
		Get(ctx, b.secretName, metav1.GetOptions{})
}

// readSecretData reads the secret content and extracts the content for key.
// returns an error if the key does not exist or the data is empty.
func (b *Backend) readSecretData(ctx context.Context, key []byte) (*backend.Item, error) {
	secret, err := b.getSecret(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	data, ok := secret.StringData[string(key)]
	if !ok || len(data) == 0 {
		return nil, errors.New("key not found")
	}

	return &backend.Item{
		Key:   key,
		Value: []byte(data),
	}, nil
}

func (b *Backend) updateSecretContent(ctx context.Context, items ...backend.Item) (*backend.Lease, error) {
	// TODO: add retry if someone changed the secret in the meanwhile
	var (
		err    error
		secret *corev1.Secret
	)
	for i := 0; i < 3; i++ {

		secret, err = b.getSecret(ctx)
		if err != nil {
			secret, err = b.createSecret(ctx)
			if err != nil {
				return nil, trace.Wrap(err)
			}
		}

		if secret.StringData == nil {
			secret.StringData = map[string]string{}
		}

		for _, item := range items {
			secret.StringData[string(item.Key)] = string(item.Value)
		}

		err = b.updateSecret(ctx, secret)
		if err == nil {
			break
		}

	}

	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &backend.Lease{}, nil
}

func (b *Backend) updateSecret(ctx context.Context, secret *corev1.Secret) error {
	secretApply := applyconfigv1.Secret(b.secretName, b.namespace).
		WithResourceVersion(secret.ResourceVersion).
		WithStringData(secret.StringData).
		WithLabels(secret.GetLabels()).
		WithAnnotations(secret.GetAnnotations())

	_, err := b.k8sClientSet.
		CoreV1().
		Secrets(b.namespace).
		Apply(ctx, secretApply, metav1.ApplyOptions{})

	return trace.Wrap(err)
}

func (b *Backend) createSecret(ctx context.Context) (*corev1.Secret, error) {
	const (
		helmReleaseNameAnnotation     = "meta.helm.sh/release-name"
		helmReleaseNamesaceAnnotation = "meta.helm.sh/release-namespace"
		helmK8SManaged                = "app.kubernetes.io/managed-by"
		helmResourcePolicy            = "helm.sh/resource-policy"
	)
	secretApply := applyconfigv1.Secret(b.secretName, b.namespace).
		WithStringData(map[string]string{}).
		WithLabels(map[string]string{
			helmK8SManaged: "Helm",
		}).
		WithAnnotations(map[string]string{
			helmReleaseNameAnnotation:     os.Getenv(releaseNameEnv),
			helmReleaseNamesaceAnnotation: os.Getenv(namespaceEnv),
			//	helmResourcePolicy:            "keep",
		})

	return b.k8sClientSet.
		CoreV1().
		Secrets(b.namespace).
		Apply(ctx, secretApply, metav1.ApplyOptions{})

}
