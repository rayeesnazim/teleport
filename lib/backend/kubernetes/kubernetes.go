package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	applyconfigv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	secretIdentifierName   = "state"
	namespaceEnv           = "POD_NAMESPACE"
	releaseNameEnv         = "RELEASE_NAME"
	teleportReplicaNameEnv = "TELEPORT_REPLICA_NAME"
)

// InKubeCluster detemines if the agent is running inside a Kubernetes cluster and has access to
// service account token and cluster CA.
func InKubeCluster() bool {
	_, err := rest.InClusterConfig()
	return err == nil
}

type Backend struct {
	k8sClientSet *kubernetes.Clientset
	namespace    string
	secretName   string

	mu *sync.Mutex
}

func New() (*Backend, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	restClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &Backend{
		k8sClientSet: restClient,
		namespace:    os.Getenv(namespaceEnv),
		secretName: fmt.Sprintf("%s-%s-%s",
			os.Getenv(releaseNameEnv),
			secretIdentifierName,
			os.Getenv(teleportReplicaNameEnv),
		),
		mu: &sync.Mutex{},
	}, nil
}

// Close is a no-op but has to satisfy the interface.
func (b *Backend) Close() error {
	return nil
}

// Exists checks if the secret already exists in Kubernetes.
func (b *Backend) Exists(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, err := b.getSecret(ctx)
	return err == nil
}

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

func (b *Backend) getSecret(ctx context.Context) (*corev1.Secret, error) {
	return b.k8sClientSet.
		CoreV1().
		Secrets(b.namespace).
		Get(ctx, b.secretName, metav1.GetOptions{})
}

func (b *Backend) readSecretData(ctx context.Context, key []byte) (*backend.Item, error) {
	secret, err := b.getSecret(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	data, ok := secret.StringData[string(key)]
	if !ok {
		return nil, errors.New("key not found")
	}
	return &backend.Item{
		Key:   key,
		Value: []byte(data),
	}, nil
}

func (b *Backend) updateSecretContent(ctx context.Context, i backend.Item) (*backend.Lease, error) {
	// TODO: add retry if someone changed the secret in the meanwhile
	secret, err := b.getSecret(ctx)
	if err != nil {
		// TODO: create secret
	}

	if secret.StringData == nil {
		secret.StringData = map[string]string{}
	}

	secret.StringData[string(i.Key)] = string(i.Value)

	if err := b.updateSecret(ctx, secret); err != nil {
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
	return err
}
