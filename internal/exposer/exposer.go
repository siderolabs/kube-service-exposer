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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
	"github.com/siderolabs/kube-service-exposer/internal/memoizer"
	"github.com/siderolabs/kube-service-exposer/internal/service"
	"github.com/siderolabs/kube-service-exposer/internal/version"
)

// Options configures the Exposer.
type Options struct {
	AnnotationKey            string
	BindCIDRs                []string
	DisallowedHostPortRanges []string
	IPRefreshPeriod          time.Duration
}

// Exposer is a controller that exposes the given services on the given host interfaces.
type Exposer struct {
	manager         manager.Manager
	controller      controller.Controller
	logger          *zap.Logger
	ipMapper        *ip.Mapper
	refreshCh       chan event.TypedGenericEvent[*corev1.Service]
	annotationKey   string
	bindCIDRs       []string
	ipRefreshPeriod time.Duration
}

// New creates a new Exposer.
func New(opts Options, logger *zap.Logger) (*Exposer, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	if len(opts.BindCIDRs) > 0 && opts.IPRefreshPeriod <= 0 {
		return nil, fmt.Errorf("ip-refresh-period must be positive when bind-cidrs is set, got %s", opts.IPRefreshPeriod)
	}

	logger = logger.With(
		zap.String("annotation-key", opts.AnnotationKey),
		zap.Strings("bind-cidrs", opts.BindCIDRs),
		zap.Strings("disallowed-host-port-ranges", opts.DisallowedHostPortRanges),
	)

	conf, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	mgr, err := manager.New(conf, manager.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create manager: %w", err)
	}

	ipSetMemoizer, err := memoizer.New(ip.NewCollector().Get)
	if err != nil {
		return nil, fmt.Errorf("failed to create ipSetMemoizer: %w", err)
	}

	ipSetProvider, err := NewFilteringIPSetProvider(opts.BindCIDRs, ipSetMemoizer, logger.Named("ip-set-provider"))
	if err != nil {
		return nil, fmt.Errorf("failed to create ipSetProvider: %w", err)
	}

	ipMapper, err := ip.NewMapper(ipSetProvider, &ip.TCPLoadBalancerProvider{}, logger.Named("ip-mapper"))
	if err != nil {
		return nil, fmt.Errorf("failed to create ipMapper: %w", err)
	}

	rec, err := service.NewReconciler(opts.AnnotationKey, mgr, ipMapper, opts.DisallowedHostPortRanges, logger.Named("service-reconciler"))
	if err != nil {
		return nil, fmt.Errorf("failed to create reconciler: %w", err)
	}

	ctrller, err := controller.New(version.Name+"-controller", mgr,
		controller.Options{
			Reconciler: rec,
			// Each replica binds host-local listeners, so every replica must reconcile every
			// Service independently. Leader election would silence followers and break that.
			NeedLeaderElection: new(false),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller: %w", err)
	}

	return &Exposer{
		annotationKey:   opts.AnnotationKey,
		bindCIDRs:       opts.BindCIDRs,
		ipRefreshPeriod: opts.IPRefreshPeriod,
		logger:          logger,
		ipMapper:        ipMapper,
		manager:         mgr,
		controller:      ctrller,
		refreshCh:       make(chan event.TypedGenericEvent[*corev1.Service], 1),
	}, nil
}

// Run runs the Exposer.
func (e *Exposer) Run(ctx context.Context) error {
	e.logger.Info("starting exposer")
	defer e.ipMapper.Close()

	if len(e.bindCIDRs) == 0 {
		e.logger.Info("bindCIDRs are empty, mappings will listen on all interfaces")
	}

	kindSource := source.Kind(
		e.manager.GetCache(),
		&corev1.Service{},
		&handler.TypedEnqueueRequestForObject[*corev1.Service]{},
		annotationPredicate(e.annotationKey),
	)

	if err := e.controller.Watch(kindSource); err != nil {
		return fmt.Errorf("failed to watch Services: %w", err)
	}

	// the refresh channel feeds the same workqueue as the K8s informer, so any concurrent
	// K8s events for the same Service get deduped against a refresh-driven event. The
	// controller's reconciler always reads the latest Service state from the cache at
	// dequeue time, so refresh never injects stale desired state.
	channelSource := source.Channel(e.refreshCh, &handler.TypedEnqueueRequestForObject[*corev1.Service]{})

	if err := e.controller.Watch(channelSource); err != nil {
		return fmt.Errorf("failed to watch refresh channel: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if err := e.manager.Start(ctx); err != nil {
			return fmt.Errorf("failed to start manager: %w", err)
		}

		return nil
	})

	if len(e.bindCIDRs) > 0 {
		e.logger.Info("bindCIDRs are specified, start IP refresh loop", zap.Duration("period", e.ipRefreshPeriod))

		eg.Go(func() error {
			return e.runRefreshLoop(ctx)
		})
	} else {
		e.logger.Info("bindCIDRs are empty, IP refresh loop will not be started")
	}

	return eg.Wait()
}

// runRefreshLoop periodically busts the IP cache and enqueues a reconcile request for
// every service the mapper currently tracks. The actual reconciliation reads fresh state
// from the K8s cache, so this never resurrects deleted services or reverts updates.
func (e *Exposer) runRefreshLoop(ctx context.Context) error {
	ticker := time.NewTicker(e.ipRefreshPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if err := e.ipMapper.RefreshIPSet(); err != nil {
			e.logger.Error("failed to refresh IP set", zap.Error(err))

			continue
		}

		for _, key := range e.ipMapper.KnownServices() {
			ev := event.TypedGenericEvent[*corev1.Service]{
				Object: &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				},
			}

			select {
			case <-ctx.Done():
				return nil
			case e.refreshCh <- ev:
			}
		}
	}
}

// annotationPredicate filters Service events to only those that have, or just lost, the
// configured annotation. Update events fire if either the old or the new state matches,
// so removing the annotation still triggers a reconcile.
func annotationPredicate(annotationKey string) predicate.TypedPredicate[*corev1.Service] {
	hasAnnotation := func(svc *corev1.Service) bool {
		if svc == nil {
			return false
		}

		_, ok := svc.GetAnnotations()[annotationKey]

		return ok
	}

	return predicate.TypedFuncs[*corev1.Service]{
		CreateFunc: func(e event.TypedCreateEvent[*corev1.Service]) bool {
			return hasAnnotation(e.Object)
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*corev1.Service]) bool {
			return hasAnnotation(e.ObjectOld) || hasAnnotation(e.ObjectNew)
		},
		DeleteFunc: func(e event.TypedDeleteEvent[*corev1.Service]) bool {
			return hasAnnotation(e.Object)
		},
		GenericFunc: func(e event.TypedGenericEvent[*corev1.Service]) bool {
			return hasAnnotation(e.Object)
		},
	}
}
