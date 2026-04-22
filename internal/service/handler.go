// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package service

import (
	"fmt"
	"strconv"

	"github.com/siderolabs/gen/xslices"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
)

// IPMapper is an interface for creating or removing port mappings for services.
type IPMapper interface {
	Add(mapping ip.Mapping) error
	Remove(svcKey client.ObjectKey)
	Sync(mappings []ip.Mapping) error
}

// Handler is a handler for services.
type Handler struct {
	ipMapper             IPMapper
	logger               *zap.Logger
	annotationKey        string
	disallowedPortRanges []*net.PortRange
}

// NewHandler returns a new Handler.
func NewHandler(annotationKey string, ipMapper IPMapper, disallowedHostPortRanges []string, logger *zap.Logger) (*Handler, error) {
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

	if ipMapper == nil {
		return nil, fmt.Errorf("ipMapper must not be nil")
	}

	return &Handler{
		annotationKey:        annotationKey,
		ipMapper:             ipMapper,
		disallowedPortRanges: portRanges,
		logger:               logger,
	}, nil
}

// Handle handles a service.
func (s *Handler) Handle(svc *corev1.Service) error {
	if svc == nil {
		return fmt.Errorf("service must not be nil")
	}

	svcKey := client.ObjectKey{
		Namespace: svc.Namespace,
		Name:      svc.Name,
	}

	if mapping, ok := s.serviceToMapping(svc); ok {
		return s.ipMapper.Add(mapping)
	}

	s.ipMapper.Remove(svcKey)

	return nil
}

// HandleDelete handles a service deletion.
func (s *Handler) HandleDelete(svcKey client.ObjectKey) error {
	if svcKey.Name == "" || svcKey.Namespace == "" {
		return fmt.Errorf("svc name and namespace must not be empty")
	}

	s.logger.Debug("handle Service delete", zap.String("ns", svcKey.Namespace), zap.String("name", svcKey.Name))

	s.ipMapper.Remove(svcKey)

	return nil
}

// HandleAll handles all services, treating the provided list as the full state
// by removing mappings not in the list and adding mappings from the list that do not yet exist.
func (s *Handler) HandleAll(services []corev1.Service) error {
	var mappings []ip.Mapping

	for i := range services {
		if mapping, ok := s.serviceToMapping(&services[i]); ok {
			mappings = append(mappings, mapping)
		}
	}

	if err := s.ipMapper.Sync(mappings); err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	return nil
}

func (s *Handler) serviceToMapping(svc *corev1.Service) (mapping ip.Mapping, ok bool) {
	svcKey := client.ObjectKey{
		Namespace: svc.Namespace,
		Name:      svc.Name,
	}

	logger := s.logger.With(zap.Stringer("svc-key", svcKey))

	logger.Debug("handle Service")

	annotationIsSet := false
	hostPortStr := ""

	for key, val := range svc.GetAnnotations() {
		if key == s.annotationKey {
			annotationIsSet = true
			hostPortStr = val

			break
		}
	}

	if !annotationIsSet {
		logger.Debug("annotation is not set on service")

		return ip.Mapping{}, false
	}

	logger.Debug("found annotation", zap.String("key", s.annotationKey), zap.String("value", hostPortStr))

	hostPort, err := strconv.Atoi(hostPortStr)
	if err != nil {
		logger.Warn("invalid host port", zap.String("value", hostPortStr))

		return ip.Mapping{}, false
	}

	for _, portRange := range s.disallowedPortRanges {
		if portRange.Contains(hostPort) {
			logger.Warn("disallowed host port", zap.Int("host-port", hostPort), zap.String("disallowed-port-range", portRange.String()))

			return ip.Mapping{}, false
		}
	}

	svcTCPPorts := xslices.Filter(svc.Spec.Ports, func(port corev1.ServicePort) bool {
		return port.Protocol == corev1.ProtocolTCP
	})

	if len(svcTCPPorts) == 0 {
		logger.Debug("no TCP ports on Service")

		return ip.Mapping{}, false
	}

	svcPort := int(svcTCPPorts[0].Port)

	if len(svcTCPPorts) > 1 {
		logger.Info("more than one TCP port on Service, using the first one", zap.Int("svc-port", svcPort))
	}

	return ip.Mapping{
		ServiceKey: svcKey,
		HostPort:   hostPort,
		SvcPort:    svcPort,
	}, true
}
