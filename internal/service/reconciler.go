// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package service

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
)

// IPMapper applies a desired set of port mappings for a Service.
type IPMapper interface {
	Reconcile(mappingSet ip.MappingSet) error
}

// ClientProvider is an interface for providing a Kubernetes client.
type ClientProvider interface {
	GetClient() client.Client
}

var _ reconcile.Reconciler = &Reconciler{}

// Reconciler handles reconcile.Reconcile callbacks from controller-runtime for Service
// resources. It parses the configured annotation, filters out disallowed host ports, and
// hands the resulting set of mappings to the IPMapper.
type Reconciler struct {
	clientProvider       ClientProvider
	ipMapper             IPMapper
	logger               *zap.Logger
	annotationKey        string
	disallowedPortRanges []*net.PortRange
}

// NewReconciler returns a new Reconciler.
func NewReconciler(annotationKey string, clientProvider ClientProvider, ipMapper IPMapper, disallowedHostPortRanges []string, logger *zap.Logger) (*Reconciler, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	portRanges := make([]*net.PortRange, 0, len(disallowedHostPortRanges))

	for _, disallowedHostPort := range disallowedHostPortRanges {
		portRange, err := net.ParsePortRange(disallowedHostPort)
		if err != nil {
			return nil, fmt.Errorf("invalid port range %q: %w", disallowedHostPort, err)
		}

		portRanges = append(portRanges, portRange)
	}

	errs := validation.ValidateAnnotations(map[string]string{annotationKey: "65535"}, field.NewPath("metadata", "annotations"))
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid annotation key: %w", errs.ToAggregate())
	}

	if clientProvider == nil {
		return nil, fmt.Errorf("clientProvider must not be nil")
	}

	if ipMapper == nil {
		return nil, fmt.Errorf("ipMapper must not be nil")
	}

	return &Reconciler{
		annotationKey:        annotationKey,
		clientProvider:       clientProvider,
		ipMapper:             ipMapper,
		disallowedPortRanges: portRanges,
		logger:               logger,
	}, nil
}

// Reconcile implements reconcile.Reconciler.
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	serviceKey := types.NamespacedName{Name: request.Name, Namespace: request.Namespace}
	logger := r.logger.With(zap.Stringer("svc-key", serviceKey))

	logger.Debug("reconcile request")

	svc := &corev1.Service{}

	err := r.clientProvider.GetClient().Get(ctx, request.NamespacedName, svc)
	if errors.IsNotFound(err) {
		logger.Debug("service not found in cache, remove all mappings")

		if err = r.ipMapper.Reconcile(ip.MappingSet{ServiceKey: serviceKey}); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to remove mappings for deleted service: %w", err)
		}

		return reconcile.Result{}, nil
	}

	if err != nil {
		return reconcile.Result{}, fmt.Errorf("could not fetch Service: %w", err)
	}

	logger.Debug("service fetched",
		zap.String("type", string(svc.Spec.Type)),
		zap.String("resource-version", svc.ResourceVersion),
		zap.Int("annotation-count", len(svc.GetAnnotations())),
		zap.Int("port-count", len(svc.Spec.Ports)),
	)

	desired := r.buildDesiredMappings(svc, logger)

	if err = r.ipMapper.Reconcile(ip.MappingSet{ServiceKey: serviceKey, Mappings: desired}); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile mappings: %w", err)
	}

	logger.Debug("service reconciled", zap.Int("mapping-count", len(desired)))

	return reconcile.Result{}, nil
}

func (r *Reconciler) buildDesiredMappings(svc *corev1.Service, logger *zap.Logger) []ip.Mapping {
	parsed := r.parseAnnotation(svc, logger)
	if len(parsed) == 0 {
		return nil
	}

	seen := make(map[int]string, len(parsed))
	desired := make([]ip.Mapping, 0, len(parsed))

	for _, entry := range parsed {
		entryLogger := logger.With(zap.String("mapping", entry.val))

		if entry.err != nil {
			entryLogger.Warn("invalid mapping entry, skipping", zap.Error(entry.err))

			continue
		}

		if firstSeen, dup := seen[entry.hostPort]; dup {
			entryLogger.Warn("duplicate host port in annotation, skipping",
				zap.Int("host-port", entry.hostPort),
				zap.String("first-mapping", firstSeen),
			)

			continue
		}

		if disallowed := r.firstDisallowedRange(entry.hostPort); disallowed != nil {
			entryLogger.Warn("disallowed host port, skipping",
				zap.Int("host-port", entry.hostPort),
				zap.String("disallowed-port-range", disallowed.String()),
			)

			continue
		}

		seen[entry.hostPort] = entry.val
		desired = append(desired, ip.Mapping{HostPort: entry.hostPort, ServicePort: entry.svcPort})
	}

	return desired
}

func (r *Reconciler) firstDisallowedRange(hostPort int) *net.PortRange {
	for _, portRange := range r.disallowedPortRanges {
		if portRange.Contains(hostPort) {
			return portRange
		}
	}

	return nil
}
