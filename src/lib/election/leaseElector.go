package election

import (
	"context"
	"math/rand"
	"time"

	log "github.com/sirupsen/logrus"
)

// LeaseStore is the backend primitive the generic lease loop drives. A backend
// only has to implement an atomic acquire/renew and a best-effort release; the
// renew cadence, leadership-loss detection, and leaderCtx lifecycle all live in
// LeaseElector. Mongo and the SQL lease-table backend both implement this; the
// k8s and Postgres-advisory-lock electors implement Elector directly instead,
// because they bring their own loop.
type LeaseStore interface {
	// Acquire atomically acquires or renews the lease for key on behalf of
	// holder, valid for ttl from now. It returns acquired=true iff holder owns
	// the lease after the call. Takeover must succeed only when the existing
	// lease is expired or already held by holder; a live lease held by someone
	// else must yield acquired=false (not an error). Clock is the store's, not
	// the caller's, to avoid cross-replica skew.
	Acquire(ctx context.Context, key, holder string, ttl time.Duration) (acquired bool, err error)

	// Release relinquishes the lease for key if still held by holder.
	// Best-effort: a failure here only delays takeover until the lease expires.
	Release(ctx context.Context, key, holder string) error
}

// LeaseConfig tunes the renew/retry cadence. Zero fields fall back to defaults.
type LeaseConfig struct {
	// TTL is how long an acquired lease stays valid without renewal. It bounds
	// worst-case failover latency: a dead leader's key is reclaimable after ~TTL.
	TTL time.Duration
	// RenewInterval is how often the leader renews. Must be comfortably below
	// TTL (default TTL/3) so a couple of missed renews don't drop leadership.
	RenewInterval time.Duration
	// RetryInterval is how often a non-leader retries acquisition.
	RetryInterval time.Duration
}

const (
	defaultTTL           = 30 * time.Second
	defaultRetryInterval = 5 * time.Second
)

func (c LeaseConfig) withDefaults() LeaseConfig {
	if c.TTL <= 0 {
		c.TTL = defaultTTL
	}
	if c.RenewInterval <= 0 {
		c.RenewInterval = c.TTL / 3
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = defaultRetryInterval
	}
	return c
}

// LeaseElector turns a LeaseStore into an Elector.
type LeaseElector struct {
	store  LeaseStore
	holder string
	cfg    LeaseConfig
	logger *log.Entry
}

// NewLeaseElector builds an Elector backed by store. holder must be unique per
// replica (see HolderID).
func NewLeaseElector(store LeaseStore, holder string, cfg LeaseConfig) Elector {
	return &LeaseElector{
		store:  store,
		holder: holder,
		cfg:    cfg.withDefaults(),
		logger: log.WithField("component", "election"),
	}
}

func (e *LeaseElector) Run(ctx context.Context, key string, onElected OnElected) error {
	logger := e.logger.WithField("key", key)
	for ctx.Err() == nil {
		acquired, err := e.store.Acquire(ctx, key, e.holder, e.cfg.TTL)
		if err != nil {
			logger.Warnf("Lease acquire failed: %v", err)
		}
		if !acquired {
			if !sleep(ctx, jitter(e.cfg.RetryInterval)) {
				break
			}
			continue
		}

		logger.Info("Acquired leadership")
		e.lead(ctx, key, logger, onElected)
		logger.Info("Lost leadership")
	}
	return ctx.Err()
}

// lead runs onElected for one leadership term: it renews on a ticker, cancels
// leaderCtx the instant a renew fails (or ctx ends), waits for onElected to
// return, and best-effort releases the lease. It returns when the term ends;
// Run then loops to attempt reacquisition.
func (e *LeaseElector) lead(ctx context.Context, key string, logger *log.Entry, onElected OnElected) {
	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		onElected(leaderCtx)
	}()

	ticker := time.NewTicker(e.cfg.RenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: stop the work, then hand the lease back so a
			// surviving replica can take over without waiting out the TTL.
			cancel()
			<-done
			e.release(key, logger)
			return
		case <-done:
			// onElected returned on its own (the poller errored out). Drop the
			// lease so another replica can pick the key up immediately.
			e.release(key, logger)
			return
		case <-ticker.C:
			acquired, err := e.store.Acquire(ctx, key, e.holder, e.cfg.TTL)
			if err != nil {
				logger.Warnf("Lease renew failed: %v", err)
			}
			if !acquired {
				// Lost the lease (expired before renew, or stolen). Stop the
				// work immediately; do NOT release — someone else may own it now.
				cancel()
				<-done
				return
			}
		}
	}
}

func (e *LeaseElector) release(key string, logger *log.Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.store.Release(ctx, key, e.holder); err != nil {
		logger.Warnf("Lease release failed: %v", err)
	}
}

// jitter returns d scaled by a random factor in [0.75, 1.25) to desynchronize
// replicas competing for the same key.
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.75 + rand.Float64()*0.5))
}

// sleep waits for d or ctx cancellation. It returns false if ctx was cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
