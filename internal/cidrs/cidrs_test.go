// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cidrs_test

import (
	"net/netip"
	"testing"

	"github.com/siderolabs/gen/maps"
	"github.com/siderolabs/gen/xslices"
	"github.com/stretchr/testify/assert"

	"github.com/siderolabs/kube-service-exposer/internal/cidrs"
)

func TestFilterIPSet(t *testing.T) {
	t.Parallel()

	ipSet := xslices.ToSet([]string{"127.0.0.1", "invalid1", "192.168.2.1", "172.20.0.42", "invalid2"})

	filtered := cidrs.FilterIPSet([]netip.Prefix{}, ipSet, nil)
	assert.Empty(t, filtered)

	netip.MustParsePrefix("192.168.2.0/24")

	filtered = cidrs.FilterIPSet([]netip.Prefix{netip.MustParsePrefix("192.168.2.0/24")}, ipSet, nil)
	assert.ElementsMatch(t, maps.Keys(filtered), []string{"192.168.2.1"})

	filtered = cidrs.FilterIPSet([]netip.Prefix{netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("172.20.0.42/32")}, ipSet, nil)
	assert.ElementsMatch(t, maps.Keys(filtered), []string{"192.168.2.1", "172.20.0.42"})

	var filterErrIPs []string

	var filterErrs []error

	filtered = cidrs.FilterIPSet(nil, ipSet, func(ip string, err error) {
		filterErrIPs = append(filterErrIPs, ip)
		filterErrs = append(filterErrs, err)
	})
	assert.Empty(t, filtered)

	assert.ElementsMatch(t, filterErrIPs, []string{"invalid1", "invalid2"})
	assert.Len(t, filterErrs, 2)
}
