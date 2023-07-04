// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package memoizer provides a container for a value that is initialized and cached lazily.
package memoizer

import (
	"fmt"
	"sync"
)

// Memoizer is a container for a value that is initialized and cached lazily.
type Memoizer[T any] struct {
	cached      T
	supplier    func() (T, error)
	lock        sync.Mutex
	initialized bool
}

// New returns a new Memoizer with the given supplier and thread-safety.
func New[T any](supplier func() (T, error)) (*Memoizer[T], error) {
	if supplier == nil {
		return nil, fmt.Errorf("supplier must not be nil")
	}

	return &Memoizer[T]{
		supplier: supplier,
	}, nil
}

// Get returns the value from the memoizer.
func (m *Memoizer[T]) Get() (T, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.getNoLock()
}

// Refresh refreshes the memoizer, it invalidates the existing value and re-initializes it.
func (m *Memoizer[T]) Refresh() (T, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.invalidateNoLock()

	return m.getNoLock()
}

func (m *Memoizer[T]) getNoLock() (T, error) {
	if m.initialized {
		return m.cached, nil
	}

	val, err := m.supplier()
	if err != nil {
		return val, err
	}

	m.cached = val
	m.initialized = true

	return val, nil
}

func (m *Memoizer[T]) invalidateNoLock() {
	m.initialized = false
}
