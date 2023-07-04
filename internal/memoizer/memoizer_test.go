// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package memoizer_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/siderolabs/kube-service-exposer/internal/memoizer"
)

func TestCreate(t *testing.T) {
	_, err := memoizer.New[string](nil)
	assert.ErrorContains(t, err, "supplier must not be nil")
}

func TestGet(t *testing.T) {
	t.Parallel()

	called := 0

	m, err := memoizer.New[string](func() (string, error) {
		called++

		return "aaa", nil
	})
	assert.NoError(t, err)

	val, err := m.Get()
	assert.NoError(t, err)

	assert.Equal(t, 1, called)
	assert.Equal(t, "aaa", val)

	val, err = m.Get()
	assert.NoError(t, err)

	assert.Equal(t, 1, called)
	assert.Equal(t, "aaa", val)
}

func TestRefresh(t *testing.T) {
	t.Parallel()

	called := 0

	m, err := memoizer.New[string](func() (string, error) {
		called++

		if called == 1 {
			return "aaa", nil
		}

		if called == 2 || called == 3 {
			return "bbb", nil
		}

		return "", fmt.Errorf("unexpected call")
	})
	assert.NoError(t, err)

	val, err := m.Get()
	assert.NoError(t, err)

	assert.Equal(t, 1, called)
	assert.Equal(t, "aaa", val)

	newVal, err := m.Refresh()
	assert.NoError(t, err)
	assert.Equal(t, "bbb", newVal)

	newVal, err = m.Refresh()
	assert.NoError(t, err)
	assert.Equal(t, "bbb", newVal)

	_, err = m.Refresh()
	assert.ErrorContains(t, err, "unexpected call")
}
