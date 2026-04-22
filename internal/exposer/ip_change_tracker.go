// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// IPSetRefresher refreshes the current IP set on the host and reports whether it changed.
type IPSetRefresher interface {
	RefreshChanged() (bool, error)
}

// IPChangeTracker watches host IP changes and syncs all services when the IP set changes.
type IPChangeTracker struct {
	ipSetRefresher IPSetRefresher
	serviceSyncer  *serviceSyncer
	logger         *zap.Logger
	period         time.Duration
}

// NewIPChangeTracker returns a new IPChangeTracker.
func NewIPChangeTracker(refresher IPSetRefresher, clientProvider ClientProvider, serviceHandler ServiceHandler,
	period time.Duration, logger *zap.Logger,
) (*IPChangeTracker, error) {
	if refresher == nil {
		return nil, fmt.Errorf("refresher must not be nil")
	}

	syncer, err := newServiceSyncer(clientProvider, serviceHandler, logger)
	if err != nil {
		return nil, err
	}

	return &IPChangeTracker{
		ipSetRefresher: refresher,
		serviceSyncer:  syncer,
		logger:         syncer.logger,
		period:         period,
	}, nil
}

// Run runs the IPChangeTracker. It blocks until the context is canceled.
func (t *IPChangeTracker) Run(ctx context.Context) error {
	t.logger.Info("starting IP change tracker", zap.Duration("period", t.period))

	ticker := time.NewTicker(t.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if err := t.handleChanges(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			t.logger.Error("failed to handle IP changes", zap.Error(err))
		}
	}
}

func (t *IPChangeTracker) handleChanges(ctx context.Context) error {
	t.logger.Debug("checking for IP changes")

	changed, err := t.ipSetRefresher.RefreshChanged()
	if err != nil {
		return fmt.Errorf("failed to refresh IP set: %w", err)
	}

	if !changed {
		t.logger.Debug("IP set did not change, skipping sync")

		return nil
	}

	t.logger.Info("detected IP set change, syncing all services")

	return t.serviceSyncer.sync(ctx)
}
