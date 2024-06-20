// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/hashicorp/go-multierror"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetRefresher is an interface for refreshing IP sets.
type SetRefresher interface {
	Refresh() (ipSet map[string]struct{}, err error)
}

// ServiceHandler is an interface for handling Service resources.
type ServiceHandler interface {
	Handle(svc *corev1.Service) error
}

// ClientProvider is an interface for providing a Kubernetes client.
type ClientProvider interface {
	GetClient() client.Client
}

// Tracker tracks changes on the IP sets on the host and updates Service resources accordingly.
type Tracker struct {
	ipSetRefresher SetRefresher
	serviceHandler ServiceHandler
	clientProvider ClientProvider
	clock          clock.Clock
	logger         *zap.Logger
	ipSet          map[string]struct{}
	period         time.Duration
}

// NewTracker returns a new Tracker.
func NewTracker(refresher SetRefresher, clientProvider ClientProvider, serviceHandler ServiceHandler,
	period time.Duration, clck clock.Clock, logger *zap.Logger,
) (*Tracker, error) {
	if refresher == nil {
		return nil, fmt.Errorf("refresher must not be nil")
	}

	if clientProvider == nil {
		return nil, fmt.Errorf("clientProvider must not be nil")
	}

	if serviceHandler == nil {
		return nil, fmt.Errorf("serviceHandler must not be nil")
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	if clck == nil {
		clck = clock.New()
	}

	return &Tracker{
		ipSetRefresher: refresher,
		clientProvider: clientProvider,
		serviceHandler: serviceHandler,
		period:         period,
		clock:          clck,
		logger:         logger,
	}, nil
}

// Run runs the Tracker. It blocks until the context is canceled.
func (t *Tracker) Run(ctx context.Context) error {
	ticker := t.clock.Ticker(t.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if err := t.handleChanges(ctx); err != nil {
			t.logger.Error("failed to handle changes", zap.Error(err))
		}
	}
}

func (t *Tracker) handleChanges(ctx context.Context) error {
	t.logger.Debug("check for changed IPs")

	ipSet, err := t.ipSetRefresher.Refresh()
	if err != nil {
		return fmt.Errorf("failed to refresh IP set: %w", err)
	}

	if reflect.DeepEqual(ipSet, t.ipSet) {
		t.logger.Debug("IP set didn't change, skip refresh")

		return nil
	}

	t.ipSet = ipSet

	t.logger.Info("detected changes on IP set, refresh mappings")

	svcList := &corev1.ServiceList{}

	if err = t.clientProvider.GetClient().List(ctx, svcList); err != nil {
		return fmt.Errorf("failed to list Services: %w", err)
	}

	var errs error

	for _, svc := range svcList.Items {
		if err = t.serviceHandler.Handle(&svc); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("failed to handle Service %s/%s: %w", svc.Namespace, svc.Name, err))
		}
	}

	return errs
}
