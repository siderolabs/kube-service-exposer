// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer_test

import (
	"testing"

	"github.com/siderolabs/gen/maps"
	"github.com/siderolabs/gen/xslices"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/siderolabs/kube-service-exposer/internal/exposer"
)

type mockProvider struct {
	err error
	ips []string
}

func (m *mockProvider) Get() (map[string]struct{}, error) {
	return xslices.ToSet(m.ips), m.err
}

func TestFilteringIPSetProviderCreate(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	_, err := exposer.NewFilteringIPSetProvider([]string{}, nil, logger)
	assert.ErrorContains(t, err, "must not be nil")

	_, err = exposer.NewFilteringIPSetProvider([]string{"172.20.0.0/24", "invalid-cidr", "192.168.2.42/32"}, &mockProvider{}, logger)
	assert.ErrorContains(t, err, "failed to parse bindCIDR")
}

func TestFilteringIPSetProviderEmptyCIDRs(t *testing.T) {
	t.Parallel()

	provider := mockProvider{
		ips: []string{"172.20.0.42", "192.168.2.42"},
		err: nil,
	}

	logger := zaptest.NewLogger(t)

	filteringProvider, err := exposer.NewFilteringIPSetProvider([]string{}, &provider, logger)
	require.NoError(t, err)

	ips, err := filteringProvider.Get()
	require.NoError(t, err)

	assert.ElementsMatch(t, maps.Keys(ips), []string{"0.0.0.0"})
}

func TestFilteringIPSetProviderFilter(t *testing.T) {
	t.Parallel()

	provider := mockProvider{
		ips: []string{"172.20.0.42", "192.168.2.42"},
		err: nil,
	}

	logger := zaptest.NewLogger(t)

	filteringProvider, err := exposer.NewFilteringIPSetProvider([]string{"172.20.0.0/24", "192.168.3.0/24"}, &provider, logger)
	require.NoError(t, err)

	ips, err := filteringProvider.Get()
	require.NoError(t, err)

	assert.ElementsMatch(t, maps.Keys(ips), []string{"172.20.0.42"})

	filteringProvider, err = exposer.NewFilteringIPSetProvider([]string{"172.20.0.0/16", "192.168.2.0/24"}, &provider, logger)
	require.NoError(t, err)

	ips, err = filteringProvider.Get()
	require.NoError(t, err)

	assert.ElementsMatch(t, maps.Keys(ips), []string{"172.20.0.42", "192.168.2.42"})
}
