// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer

import (
	"fmt"
	"net/netip"

	"go.uber.org/zap"

	"github.com/siderolabs/kube-service-exposer/internal/cidrs"
)

// IPSetProvider in an interface for getting a set of IP addresses.
type IPSetProvider interface {
	Get() (map[string]struct{}, error)
}

// FilteringIPSetProvider is an IPSetProvider that filters the underlying IPSetProvider by a list of CIDRs.
//
// If the list of CIDRs is empty, it will return "0.0.0.0" as the only IP address.
type FilteringIPSetProvider struct {
	ipCache          IPSetProvider
	logger           *zap.Logger
	bindCIDRPrefixes []netip.Prefix
}

// NewFilteringIPSetProvider returns a new FilteringIPSetProvider.
func NewFilteringIPSetProvider(bindCIDRs []string, underlyingProvider IPSetProvider, logger *zap.Logger) (*FilteringIPSetProvider, error) {
	bindCIDRPrefixes := make([]netip.Prefix, 0, len(bindCIDRs))

	for _, bindCIDR := range bindCIDRs {
		prefix, err := netip.ParsePrefix(bindCIDR)
		if err != nil {
			return nil, fmt.Errorf("failed to parse bindCIDR: %w", err)
		}

		bindCIDRPrefixes = append(bindCIDRPrefixes, prefix)
	}

	if underlyingProvider == nil {
		return nil, fmt.Errorf("underlyingProvider must not be nil")
	}

	return &FilteringIPSetProvider{
		logger:           logger,
		bindCIDRPrefixes: bindCIDRPrefixes,
		ipCache:          underlyingProvider,
	}, nil
}

// Get implements the ipmapper.IPSetProvider interface.
//
// It returns the set of host IP addresses to bind the load balancer to.
func (e *FilteringIPSetProvider) Get() (map[string]struct{}, error) {
	if len(e.bindCIDRPrefixes) == 0 {
		return map[string]struct{}{"0.0.0.0": {}}, nil
	}

	allIPsSet, err := e.ipCache.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get all IP addresses: %w", err)
	}

	return cidrs.FilterIPSet(e.bindCIDRPrefixes, allIPsSet, func(ip string, err error) {
		e.logger.Debug("failed to parse IP address", zap.String("ip", ip), zap.Error(err))
	}), nil
}
