// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

// PeriodicSyncer syncs all Service resources periodically.
type PeriodicSyncer struct {
	serviceSyncer *serviceSyncer
	logger        *zap.Logger
	period        time.Duration
}

// NewPeriodicSyncer returns a new PeriodicSyncer.
func NewPeriodicSyncer(clientProvider ClientProvider, serviceHandler ServiceHandler, period time.Duration, logger *zap.Logger) (*PeriodicSyncer, error) {
	syncer, err := newServiceSyncer(clientProvider, serviceHandler, logger)
	if err != nil {
		return nil, err
	}

	return &PeriodicSyncer{
		serviceSyncer: syncer,
		period:        period,
		logger:        syncer.logger,
	}, nil
}

// Run runs the PeriodicSyncer. It blocks until the context is canceled.
func (syncer *PeriodicSyncer) Run(ctx context.Context) error {
	syncer.logger.Info("starting periodic syncer", zap.Duration("period", syncer.period))

	ticker := time.NewTicker(syncer.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if err := syncer.serviceSyncer.sync(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			syncer.logger.Error("failed to sync all services", zap.Error(err))
		}
	}
}
