// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer

import (
	"fmt"
	"maps"
	"net/netip"
	"sync"

	"go.uber.org/zap"

	"github.com/siderolabs/kube-service-exposer/internal/cidrs"
)

// IPSetProvider is an interface for getting a set of IP addresses.
type IPSetProvider interface {
	Get() (map[string]struct{}, error)
}

// FilteringIPSetProvider is an IPSetProvider that filters the underlying IPSetProvider by a list of CIDRs.
//
// If the list of CIDRs is empty, it will return "0.0.0.0" as the only IP address.
type FilteringIPSetProvider struct {
	ipSetProvider    IPSetProvider
	logger           *zap.Logger
	cachedIPSet      map[string]struct{}
	bindCIDRPrefixes []netip.Prefix
	lock             sync.Mutex
	initialized      bool
}

// NewFilteringIPSetProvider returns a new FilteringIPSetProvider.
func NewFilteringIPSetProvider(bindCIDRs []string, ipSetProvider IPSetProvider, logger *zap.Logger) (*FilteringIPSetProvider, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	bindCIDRPrefixes := make([]netip.Prefix, 0, len(bindCIDRs))

	for _, bindCIDR := range bindCIDRs {
		prefix, err := netip.ParsePrefix(bindCIDR)
		if err != nil {
			return nil, fmt.Errorf("failed to parse bindCIDR: %w", err)
		}

		bindCIDRPrefixes = append(bindCIDRPrefixes, prefix)
	}

	if ipSetProvider == nil {
		return nil, fmt.Errorf("ipSetProvider must not be nil")
	}

	return &FilteringIPSetProvider{
		logger:           logger,
		bindCIDRPrefixes: bindCIDRPrefixes,
		ipSetProvider:    ipSetProvider,
	}, nil
}

// Get returns the set of host IP addresses to bind the load balancer to.
func (e *FilteringIPSetProvider) Get() (map[string]struct{}, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	if e.initialized {
		return e.cachedIPSet, nil
	}

	ipSet, err := e.getCurrentIPSetNoLock()
	if err != nil {
		return nil, err
	}

	e.cachedIPSet = ipSet
	e.initialized = true

	return e.cachedIPSet, nil
}

// RefreshChanged refreshes the cached IP set and reports whether it changed.
func (e *FilteringIPSetProvider) RefreshChanged() (bool, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	ipSet, err := e.getCurrentIPSetNoLock()
	if err != nil {
		return false, err
	}

	if e.initialized && maps.Equal(e.cachedIPSet, ipSet) {
		return false, nil
	}

	e.cachedIPSet = ipSet
	e.initialized = true

	return true, nil
}

func (e *FilteringIPSetProvider) getCurrentIPSetNoLock() (map[string]struct{}, error) {
	if len(e.bindCIDRPrefixes) == 0 {
		return map[string]struct{}{"0.0.0.0": {}}, nil
	}

	allIPsSet, err := e.ipSetProvider.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get all IP addresses: %w", err)
	}

	return cidrs.FilterIPSet(e.bindCIDRPrefixes, allIPsSet, func(ip string, err error) {
		e.logger.Debug("failed to parse IP address", zap.String("ip", ip), zap.Error(err))
	}), nil
}
