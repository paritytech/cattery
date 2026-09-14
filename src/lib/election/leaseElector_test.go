package election

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeStore is a controllable LeaseStore. acquireFn decides each Acquire result
// by 1-based call number; nil means "always acquired".
type fakeStore struct {
	mu           sync.Mutex
	acquireCalls int
	releaseCalls int
	lastRelKey   string
	lastRelHold  string
	acquireFn    func(call int) (bool, error)
	// acquireCtxFn, when set, takes precedence over acquireFn and receives the
	// per-call ctx so a test can simulate a store that stalls.
	acquireCtxFn func(ctx context.Context, call int) (bool, error)
}

func (f *fakeStore) Acquire(ctx context.Context, _, _ string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	f.acquireCalls++
	n := f.acquireCalls
	fn := f.acquireFn
	ctxFn := f.acquireCtxFn
	f.mu.Unlock()
	if ctxFn != nil {
		return ctxFn(ctx, n)
	}
	if fn != nil {
		return fn(n)
	}
	return true, nil
}

func (f *fakeStore) Release(_ context.Context, key, holder string) error {
	f.mu.Lock()
	f.releaseCalls++
	f.lastRelKey = key
	f.lastRelHold = holder
	f.mu.Unlock()
	return nil
}

func (f *fakeStore) counts() (acquire, release int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquireCalls, f.releaseCalls
}

func (f *fakeStore) lastRelease() (key, holder string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastRelKey, f.lastRelHold
}

// fast cadence so tests run in tens of ms.
func newTestElector(store LeaseStore) Elector {
	return NewLeaseElector(store, "holder-1", LeaseConfig{
		TTL:           100 * time.Millisecond,
		RenewInterval: 5 * time.Millisecond,
		RetryInterval: 5 * time.Millisecond,
	})
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

// Acquiring leadership invokes onElected with a live leaderCtx; a graceful
// shutdown (parent ctx cancel) cancels leaderCtx and releases the lease.
func TestLeaseElector_AcquiresAndReleasesOnShutdown(t *testing.T) {
	store := &fakeStore{} // always acquired
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	stopped := make(chan struct{})
	runDone := make(chan struct{})
	var leaderCtx context.Context

	go func() {
		_ = elector.Run(ctx, "type-a", func(lctx context.Context) {
			leaderCtx = lctx
			close(started)
			<-lctx.Done()
			close(stopped)
		})
		close(runDone)
	}()

	waitClosed(t, started, "leadership start")
	assert.NoError(t, leaderCtx.Err(), "leaderCtx live while leading")

	cancel()
	waitClosed(t, stopped, "onElected stop")
	waitClosed(t, runDone, "Run return")

	assert.Error(t, leaderCtx.Err(), "leaderCtx cancelled after shutdown")
	_, releases := store.counts()
	assert.Equal(t, 1, releases, "graceful shutdown releases the lease")
	key, holder := store.lastRelease()
	assert.Equal(t, "type-a", key)
	assert.Equal(t, "holder-1", holder)
}

// Losing the lease (a renew that no longer acquires) must cancel leaderCtx but
// must NOT release — another replica may already own the key.
func TestLeaseElector_LostLeadershipCancelsButDoesNotRelease(t *testing.T) {
	store := &fakeStore{
		acquireFn: func(call int) (bool, error) {
			return call == 1, nil // win once, then lose forever
		},
	}
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	var leaderCtx context.Context

	go elector.Run(ctx, "type-a", func(lctx context.Context) {
		leaderCtx = lctx
		close(started)
		<-lctx.Done()
		close(stopped)
	})

	waitClosed(t, started, "leadership start")
	waitClosed(t, stopped, "leadership loss cancels onElected")
	assert.Error(t, leaderCtx.Err())

	time.Sleep(30 * time.Millisecond) // let several retry cycles run
	_, releases := store.counts()
	assert.Equal(t, 0, releases, "losing the lease must not release it")
}

// A renew that errors while the lease is still within its TTL must not end the
// term: the lease is still ours, and flapping on one transient store error
// would tear down the poller for nothing.
func TestLeaseElector_TransientRenewErrorKeepsLeadership(t *testing.T) {
	store := &fakeStore{
		acquireFn: func(call int) (bool, error) {
			if call == 1 {
				return true, nil
			}
			// Every renew fails "transiently"; the TTL (100ms) is what bounds
			// the term, not the error.
			return false, errors.New("store unavailable")
		},
	}
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	var termStart, termEnd time.Time

	go elector.Run(ctx, "type-a", func(lctx context.Context) {
		termStart = time.Now()
		close(started)
		<-lctx.Done()
		termEnd = time.Now()
		close(stopped)
	})

	waitClosed(t, started, "leadership start")
	// The term must end once the TTL lapses without a successful renew...
	waitClosed(t, stopped, "step-down at lease expiry")

	// ...but not before: with a 5ms renew interval, the old behaviour ended the
	// term on the first failed renew (~5ms). Allow slack below the 100ms TTL
	// for timer granularity; anything under half the TTL means a transient
	// error ended the term.
	term := termEnd.Sub(termStart)
	assert.GreaterOrEqual(t, term, 50*time.Millisecond, "transient renew errors ended the term early (term lasted %s)", term)
	acquires, releases := store.counts()
	assert.Greater(t, acquires, 3, "renews kept being attempted during the term")
	assert.Equal(t, 0, releases, "an expired lease must not be released")
}

// A store that stalls on renew must not keep leaderCtx alive past the lease's
// expiry: another replica may take the key the moment the TTL lapses, and two
// live leaders would double-poll the same GitHub session.
func TestLeaseElector_StalledRenewStepsDownAtExpiry(t *testing.T) {
	store := &fakeStore{
		acquireCtxFn: func(ctx context.Context, call int) (bool, error) {
			if call == 1 {
				return true, nil
			}
			<-ctx.Done() // hang until the elector gives up on this call
			return false, ctx.Err()
		},
	}
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan struct{})

	go elector.Run(ctx, "type-a", func(lctx context.Context) {
		close(started)
		<-lctx.Done()
		close(stopped)
	})

	waitClosed(t, started, "leadership start")
	termStart := time.Now()
	waitClosed(t, stopped, "step-down after stalled renew")

	// TTL is 100ms; allow generous slack for scheduling but reject anything
	// that looks like the stall being waited out indefinitely.
	assert.Less(t, time.Since(termStart), 500*time.Millisecond, "leaderCtx outlived the lease TTL")
	_, releases := store.counts()
	assert.Equal(t, 0, releases, "an expired lease must not be released")
}

// If onElected returns on its own (the poller exited), the lease is released so
// another replica can take the key immediately.
func TestLeaseElector_OnElectedReturnReleasesLease(t *testing.T) {
	store := &fakeStore{
		acquireFn: func(call int) (bool, error) {
			return call == 1, nil // lead once; don't re-lead after self-exit
		},
	}
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go elector.Run(ctx, "type-a", func(lctx context.Context) {
		// return immediately — simulate the poller exiting
	})

	assert.Eventually(t, func() bool {
		_, r := store.counts()
		return r == 1
	}, time.Second, 5*time.Millisecond, "self-exit releases exactly once")
}

// A replica that cannot acquire keeps retrying until the key is free.
func TestLeaseElector_RetriesUntilAcquired(t *testing.T) {
	store := &fakeStore{
		acquireFn: func(call int) (bool, error) {
			return call >= 3, nil // fail the first two attempts
		},
	}
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})

	go elector.Run(ctx, "type-a", func(lctx context.Context) {
		close(started)
		<-lctx.Done()
	})

	waitClosed(t, started, "leadership after retries")
	acquires, _ := store.counts()
	assert.GreaterOrEqual(t, acquires, 3)
}

// Even under rapid lose/regain flapping, the same replica never runs two
// leadership terms concurrently (the <-done wait in lead serializes them).
func TestLeaseElector_NeverRunsOverlappingTerms(t *testing.T) {
	store := &fakeStore{
		acquireFn: func(call int) (bool, error) {
			return call%2 == 1, nil // win, lose, win, lose, ...
		},
	}
	elector := newTestElector(store)

	ctx, cancel := context.WithCancel(context.Background())

	var concurrent, maxConcurrent, terms int32
	go elector.Run(ctx, "type-a", func(lctx context.Context) {
		c := atomic.AddInt32(&concurrent, 1)
		for {
			m := atomic.LoadInt32(&maxConcurrent)
			if c <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, c) {
				break
			}
		}
		<-lctx.Done()
		atomic.AddInt32(&concurrent, -1)
		atomic.AddInt32(&terms, 1)
	})

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, int32(1), atomic.LoadInt32(&maxConcurrent), "terms must not overlap")
	assert.Greater(t, atomic.LoadInt32(&terms), int32(1), "should have flapped through several terms")
}
