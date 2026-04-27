// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"

	"github.com/siderolabs/kube-service-exposer/internal/exposer"
)

func TestNewRejectsNonPositiveRefreshPeriodWithBindCIDRs(t *testing.T) {
	t.Parallel()

	_, err := exposer.New(exposer.Options{
		AnnotationKey:   "test",
		BindCIDRs:       []string{"10.0.0.0/8"},
		IPRefreshPeriod: 0,
	}, zaptest.NewLogger(t))
	assert.ErrorContains(t, err, "ip-refresh-period must be positive")

	_, err = exposer.New(exposer.Options{
		AnnotationKey:   "test",
		BindCIDRs:       []string{"10.0.0.0/8"},
		IPRefreshPeriod: -1,
	}, zaptest.NewLogger(t))
	assert.ErrorContains(t, err, "ip-refresh-period must be positive")
}
