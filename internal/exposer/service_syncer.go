// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ServiceHandler is an interface for handling Service resources.
type ServiceHandler interface {
	HandleAll(services []corev1.Service) error
}

// ClientProvider is an interface for providing a Kubernetes client.
type ClientProvider interface {
	GetClient() client.Client
}

type serviceSyncer struct {
	serviceHandler ServiceHandler
	clientProvider ClientProvider
	logger         *zap.Logger
}

func newServiceSyncer(clientProvider ClientProvider, serviceHandler ServiceHandler, logger *zap.Logger) (*serviceSyncer, error) {
	if clientProvider == nil {
		return nil, fmt.Errorf("clientProvider must not be nil")
	}

	if serviceHandler == nil {
		return nil, fmt.Errorf("serviceHandler must not be nil")
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &serviceSyncer{
		clientProvider: clientProvider,
		serviceHandler: serviceHandler,
		logger:         logger,
	}, nil
}

func (syncer *serviceSyncer) sync(ctx context.Context) error {
	svcList := &corev1.ServiceList{}

	if err := syncer.clientProvider.GetClient().List(ctx, svcList); err != nil {
		return fmt.Errorf("failed to list Services: %w", err)
	}

	syncer.logger.Info("sync all services", zap.Int("num-services", len(svcList.Items)))

	return syncer.serviceHandler.HandleAll(svcList.Items)
}
