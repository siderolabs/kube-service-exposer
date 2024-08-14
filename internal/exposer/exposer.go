// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package exposer implements the Exposer controller.
package exposer

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
	"github.com/siderolabs/kube-service-exposer/internal/memoizer"
	"github.com/siderolabs/kube-service-exposer/internal/reconciler"
	"github.com/siderolabs/kube-service-exposer/internal/service"
	"github.com/siderolabs/kube-service-exposer/internal/version"
)

// Exposer is a controller that exposes the given services on the given host interfaces.
type Exposer struct {
	manager        manager.Manager
	controller     controller.Controller
	logger         *zap.Logger
	ipSetMemoizer  *memoizer.Memoizer[map[string]struct{}]
	serviceHandler *service.Handler
	bindCIDRs      []string
}

// New creates a new Exposer.
func New(annotationKey string, bindCIDRs, disallowedHostPortRanges []string, logger *zap.Logger) (*Exposer, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	logger = logger.With(zap.String("annotation-key", annotationKey), zap.Strings("bind-cidrs", bindCIDRs), zap.Strings("disallowed-host-port-ranges", disallowedHostPortRanges))

	ipCollector := ip.NewCollector()

	conf, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	mgr, err := manager.New(conf, manager.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create manager: %w", err)
	}

	ipSetMemoizer, err := memoizer.New(ipCollector.Get)
	if err != nil {
		return nil, fmt.Errorf("failed to create ipSetMemoizer: %w", err)
	}

	ipSetProvider, err := NewFilteringIPSetProvider(bindCIDRs, ipSetMemoizer, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create ipSetProvider: %w", err)
	}

	ipMapper, err := ip.NewMapper(ipSetProvider, &ip.TCPLoadBalancerProvider{}, logger.With(zap.String("component", "ip-mapper")))
	if err != nil {
		return nil, fmt.Errorf("failed to create ipMapper: %w", err)
	}

	serviceHandler, err := service.NewHandler(annotationKey, ipMapper, disallowedHostPortRanges, logger.With(zap.String("component", "service-handler")))
	if err != nil {
		return nil, fmt.Errorf("failed to create serviceHandler: %w", err)
	}

	rec, err := reconciler.New(mgr, serviceHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create reconciler: %w", err)
	}

	ctrller, err := controller.New(version.Name+"-controller", mgr,
		controller.Options{
			Reconciler:         rec,
			NeedLeaderElection: ptr.To(false),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller: %w", err)
	}

	exposer := &Exposer{
		bindCIDRs:      bindCIDRs,
		logger:         logger,
		ipSetMemoizer:  ipSetMemoizer,
		manager:        mgr,
		serviceHandler: serviceHandler,
		controller:     ctrller,
	}

	return exposer, nil
}

// Run runs the Exposer.
func (e *Exposer) Run(ctx context.Context) error {
	e.logger.Info("starting exposer")

	if len(e.bindCIDRs) == 0 {
		e.logger.Info("bindCIDRs are empty, mappings will listen on all interfaces")
	}

	if err := e.controller.Watch(source.Kind(e.manager.GetCache(), &corev1.Service{}, &handler.TypedEnqueueRequestForObject[*corev1.Service]{})); err != nil {
		return fmt.Errorf("failed to watch Services: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if err := e.manager.Start(ctx); err != nil {
			return fmt.Errorf("failed to start manager: %w", err)
		}

		if ctx.Err() == nil {
			// context is not canceled, but the manager has stopped
			return fmt.Errorf("manager stopped unexpectedly")
		}

		return nil
	})

	if len(e.bindCIDRs) > 0 {
		e.logger.Info("bindCIDRs are specified, start IP change listener")

		ipTracker, err := ip.NewTracker(e.ipSetMemoizer, e.manager, e.serviceHandler, 30*time.Second, nil, e.logger.With(zap.String("component", "ip-tracker")))
		if err != nil {
			return fmt.Errorf("failed to create ipTracker: %w", err)
		}

		eg.Go(func() error {
			if err = ipTracker.Run(ctx); err != nil {
				return fmt.Errorf("failed to start IP change listener: %w", err)
			}

			if ctx.Err() == nil {
				// context is not canceled, but the IP change listener has stopped
				return fmt.Errorf("IP change listener stopped unexpectedly")
			}

			return nil
		})
	} else {
		e.logger.Info("bindCIDRs are empty, IP change listener will not be started")
	}

	return eg.Wait()
}
