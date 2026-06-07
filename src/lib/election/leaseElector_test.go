package election

import (
	"context"
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
}

func (f *fakeStore) Acquire(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	f.acquireCalls++
	n := f.acquireCalls
	fn := f.acquireFn
	f.mu.Unlock()
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
