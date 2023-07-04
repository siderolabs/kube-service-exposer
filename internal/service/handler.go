// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package service

import (
	"fmt"
	"strconv"

	"github.com/siderolabs/gen/slices"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// IPMapper is an interface for creating or removing port mappings for services.
type IPMapper interface {
	Add(svcName string, hostPort, svcPort int) error
	Remove(svcName string)
}

// Handler is a handler for services.
type Handler struct {
	ipMapper      IPMapper
	logger        *zap.Logger
	annotationKey string
}

// NewHandler returns a new Handler.
func NewHandler(annotationKey string, ipMapper IPMapper, logger *zap.Logger) (*Handler, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	errs := validation.ValidateAnnotations(map[string]string{annotationKey: "65535"}, field.NewPath("metadata", "annotations"))
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid annotation key: %w", errs.ToAggregate())
	}

	if ipMapper == nil {
		return nil, fmt.Errorf("ipMapper must not be nil")
	}

	return &Handler{
		annotationKey: annotationKey,
		ipMapper:      ipMapper,
		logger:        logger,
	}, nil
}

// Handle handles a service.
func (s *Handler) Handle(svc *corev1.Service) error {
	if svc == nil {
		return fmt.Errorf("service must not be nil")
	}

	svcName := svc.Name + "." + svc.Namespace

	logger := s.logger.With(zap.String("svc-name", svcName))

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

		s.ipMapper.Remove(svcName)

		return nil
	}

	logger.Debug("found annotation", zap.String("key", s.annotationKey), zap.String("value", hostPortStr))

	hostPort, err := strconv.Atoi(hostPortStr)
	if err != nil {
		return fmt.Errorf("invalid host port %q: %w", hostPortStr, err)
	}

	svcTCPPorts := slices.Filter(svc.Spec.Ports, func(port corev1.ServicePort) bool {
		return port.Protocol == corev1.ProtocolTCP
	})

	if len(svcTCPPorts) == 0 {
		logger.Debug("no TCP ports on Service")

		s.ipMapper.Remove(svcName)

		return nil
	}

	svcPort := int(svcTCPPorts[0].Port)

	if len(svcTCPPorts) > 1 {
		logger.Info("more than one TCP port on Service, using the first one", zap.Int("svc-port", svcPort))
	}

	if err = s.ipMapper.Add(svcName, hostPort, svcPort); err != nil {
		return fmt.Errorf("failed to register host port: %w", err)
	}

	return nil
}

// HandleDelete handles a service deletion.
func (s *Handler) HandleDelete(svcName string) error {
	if svcName == "" {
		return fmt.Errorf("svcName must not be empty")
	}

	s.logger.Debug("handle Service delete", zap.String("svc-name", svcName))

	s.ipMapper.Remove(svcName)

	return nil
}
