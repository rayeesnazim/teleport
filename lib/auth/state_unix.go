//go:build !windows
// +build !windows

/*
Copyright 2019 Gravitational, Inc.

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

package auth

import (
	"context"

	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/backend/kubernetes"
	"github.com/gravitational/teleport/lib/backend/lite"
	"github.com/gravitational/trace"
)

// NewProcessStorage returns a new instance of the process storage.
func NewProcessStorage(ctx context.Context, path string) (*ProcessStorage, error) {
	var (
		identityStorage stateBackend
	)

	if path == "" {
		return nil, trace.BadParameter("missing parameter path")
	}

	litebk, err := lite.NewWithConfig(ctx, lite.Config{
		Path:      path,
		EventsOff: true,
		Sync:      lite.SyncFull,
	})

	if err != nil {
		return nil, trace.Wrap(err)
	}

	if kubernetes.InKubeCluster() {
		kubeSecret, err := kubernetes.New()
		if err != nil {
			return nil, trace.Wrap(err)
		}

		if !kubeSecret.Exists(ctx) {
			compatibilityLayer(ctx, kubeSecret, litebk)
		}

		identityStorage = kubeSecret
	} else {
		identityStorage = litebk
	}

	return &ProcessStorage{Backend: litebk, stateStorage: identityStorage}, nil
}

func compatibilityLayer(ctx context.Context, stateBk stateBackend, litebk *lite.Backend) {
	copyDataFromLocalIntoKube(ctx, stateBk, litebk, idsPrefix)
	copyDataFromLocalIntoKube(ctx, stateBk, litebk, statesPrefix)

}

func copyDataFromLocalIntoKube(ctx context.Context, stateBk stateBackend, litebk *lite.Backend, prefix string) {
	results, err := litebk.GetRange(ctx, backend.Key(prefix), backend.RangeEnd(backend.Key(prefix)), backend.NoLimit)
	if err != nil {
		return
	}
	for _, item := range results.Items {
		if _, err := stateBk.Put(ctx, item); err != nil {
			// TODO: log this lines
			_ = err
		}
	}

}
