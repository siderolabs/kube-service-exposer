// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package service

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

type portMapping struct {
	err      error
	val      string
	hostPort int
	svcPort  int
}

func (r *Reconciler) parseAnnotation(svc *corev1.Service, logger *zap.Logger) []portMapping {
	annotationVal, ok := svc.Annotations[r.annotationKey]
	if !ok {
		return nil
	}

	logger.Debug("found annotation", zap.String("key", r.annotationKey), zap.String("value", annotationVal))

	hasTCPPort := slices.ContainsFunc(svc.Spec.Ports, func(port corev1.ServicePort) bool {
		return port.Protocol == corev1.ProtocolTCP
	})

	if !hasTCPPort {
		logger.Debug("no TCP ports on Service")

		return nil
	}

	var mappings []portMapping

	for mappingStr := range strings.SplitSeq(annotationVal, ",") {
		mappingStr = strings.TrimSpace(mappingStr)
		if mappingStr == "" {
			continue
		}

		hostPort, svcPort, err := parseMapping(mappingStr, svc.Spec.Ports)

		mappings = append(mappings, portMapping{
			val:      mappingStr,
			hostPort: hostPort,
			svcPort:  svcPort,
			err:      err,
		})
	}

	return mappings
}

// parseMapping parses a single annotation entry into a (host port, service port) pair.
//
// Accepted forms:
//   - "<host-port>" — service port defaults to the first TCP port on the Service.
//   - "<host-port>:<service-port-number>" — must match an existing TCP port number.
//   - "<host-port>:<service-port-name>" — must match an existing TCP port name.
//
// Only TCP ports are eligible. If a name or number matches a non-TCP port (UDP/SCTP),
// the error names that protocol explicitly so the user knows why their entry was
// rejected.
func parseMapping(mappingStr string, svcPorts []corev1.ServicePort) (hostPort, svcPort int, err error) {
	hostPortStr, svcPortStr, hasSvcPort := strings.Cut(mappingStr, ":")

	hostPort, err = strconv.Atoi(hostPortStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid host port %q: %w", hostPortStr, err)
	}

	if hostPort < 1 || hostPort > 65535 {
		return 0, 0, fmt.Errorf("host port %q out of range: must be between 1 and 65535", hostPortStr)
	}

	if !hasSvcPort {
		for _, p := range svcPorts {
			if p.Protocol == corev1.ProtocolTCP {
				return hostPort, int(p.Port), nil
			}
		}

		return 0, 0, fmt.Errorf("no TCP port on this Service")
	}

	if svcPortStr == "" {
		return 0, 0, fmt.Errorf("empty service port in mapping %q", mappingStr)
	}

	svcPortNumber, atoiErr := strconv.Atoi(svcPortStr)
	isNumericPort := atoiErr == nil

	matches := func(p corev1.ServicePort) bool {
		return (isNumericPort && int(p.Port) == svcPortNumber) || p.Name == svcPortStr
	}

	// prefer a TCP match.
	for _, p := range svcPorts {
		if p.Protocol == corev1.ProtocolTCP && matches(p) {
			return hostPort, int(p.Port), nil
		}
	}

	// no TCP match; if the requested port exists with a different protocol, name it
	// explicitly so the error explains the rejection.
	for _, p := range svcPorts {
		if p.Protocol != corev1.ProtocolTCP && matches(p) {
			return 0, 0, fmt.Errorf("port %q on this Service uses protocol %s, only TCP is supported", svcPortStr, p.Protocol)
		}
	}

	return 0, 0, fmt.Errorf("no TCP port matching %q on this Service", svcPortStr)
}
