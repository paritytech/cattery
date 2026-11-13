package trayManager

import (
	"cattery/lib/githubClient"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/stretchr/testify/assert"
)

func TestCachedRunnerStatus_ServesRepeatsFromCache(t *testing.T) {
	inner := &stubRunnerStatus{state: online(true)}
	c := NewCachedRunnerStatus(inner, time.Minute)

	for range 5 {
		state, err := c.GetRunnerState("paritytech", 42)
		assert.NoError(t, err)
		assert.True(t, state.Busy)
	}

	assert.Equal(t, 1, inner.callCount(), "the reaper and the ping path should share one lookup")
}

// Caching an idle verdict would let a tray that picked up a job moments after
// being read idle be deleted against the stale answer — the exact failure the
// guard exists to prevent.
func TestCachedRunnerStatus_DoesNotCacheIdle(t *testing.T) {
	inner := &stubRunnerStatus{state: online(false)}
	c := NewCachedRunnerStatus(inner, time.Minute)

	for range 3 {
		state, err := c.GetRunnerState("paritytech", 42)
		assert.NoError(t, err)
		assert.False(t, state.Busy)
	}

	assert.Equal(t, 3, inner.callCount(), "every idle verdict must be freshly fetched")
}

// A runner that finishes its job must not keep answering "busy" from the
// entry written while it was working.
func TestCachedRunnerStatus_IdleSupersedesCachedBusy(t *testing.T) {
	inner := &stubRunnerStatus{state: online(true)}
	c := NewCachedRunnerStatus(inner, time.Minute)

	state, err := c.GetRunnerState("paritytech", 42)
	assert.NoError(t, err)
	assert.True(t, state.Busy)

	inner.setState(online(false))

	// The cached busy entry is still live, so this is served from cache.
	state, err = c.GetRunnerState("paritytech", 42)
	assert.NoError(t, err)
	assert.True(t, state.Busy, "within the TTL the busy entry still answers")

	// Expire it, and the fresh idle result must evict the busy entry.
	c.expireAll()

	state, err = c.GetRunnerState("paritytech", 42)
	assert.NoError(t, err)
	assert.False(t, state.Busy)

	state, err = c.GetRunnerState("paritytech", 42)
	assert.NoError(t, err)
	assert.False(t, state.Busy, "the superseded busy entry must be gone")
}

func TestCachedRunnerStatus_SeparatesRunners(t *testing.T) {
	inner := &stubRunnerStatus{state: online(true)}
	c := NewCachedRunnerStatus(inner, time.Minute)

	_, _ = c.GetRunnerState("paritytech", 1)
	_, _ = c.GetRunnerState("paritytech", 2)
	// Repeats of each are served from cache.
	_, _ = c.GetRunnerState("paritytech", 1)
	_, _ = c.GetRunnerState("paritytech", 2)

	assert.Equal(t, 2, inner.callCount(), "each runner needs its own answer")
}

func TestCachedRunnerStatus_RefetchesAfterTTL(t *testing.T) {
	inner := &stubRunnerStatus{state: online(true)}
	c := NewCachedRunnerStatus(inner, time.Millisecond)

	_, _ = c.GetRunnerState("paritytech", 42)
	time.Sleep(5 * time.Millisecond)
	_, _ = c.GetRunnerState("paritytech", 42)

	assert.Equal(t, 2, inner.callCount())
}

// A failed check keeps the tray alive, so retrying promptly is what resolves
// it; caching the failure would extend every outage by the TTL.
func TestCachedRunnerStatus_DoesNotCacheErrors(t *testing.T) {
	inner := &stubRunnerStatus{err: errors.New("connection timed out")}
	c := NewCachedRunnerStatus(inner, time.Minute)

	for range 3 {
		_, err := c.GetRunnerState("paritytech", 42)
		assert.Error(t, err)
	}

	assert.Equal(t, 3, inner.callCount())
}

func TestCachedRunnerStatus_ErrRunnerNotFoundPropagates(t *testing.T) {
	inner := &stubRunnerStatus{err: githubClient.ErrRunnerNotFound}
	c := NewCachedRunnerStatus(inner, time.Minute)

	_, err := c.GetRunnerState("paritytech", 42)

	assert.ErrorIs(t, err, githubClient.ErrRunnerNotFound,
		"KeepStale distinguishes this from a failed check")
}

func TestCachedRunnerStatus_ConcurrentUseIsSafe(t *testing.T) {
	inner := &stubRunnerStatus{state: online(true)}
	c := NewCachedRunnerStatus(inner, time.Minute)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = c.GetRunnerState("paritytech", int64(n%5))
		}(i)
	}
	wg.Wait()
}

// A 403 means the GitHub App is missing a permission. That will not fix itself
// between ticks, and a forbidden check keeps the tray, so re-querying every
// minute for the whole hard threshold is hours of guaranteed-failing calls.
func TestCachedRunnerStatus_CachesForbidden(t *testing.T) {
	inner := &stubRunnerStatus{err: forbiddenErr()}
	c := NewCachedRunnerStatus(inner, time.Minute)

	for range 5 {
		_, err := c.GetRunnerState("paritytech", 42)
		assert.True(t, githubClient.IsForbidden(err),
			"the caller must still see a forbidden error, not a cache miss")
	}

	assert.Equal(t, 1, inner.callCount(), "a deterministic refusal is asked once per window")
}

// The permission being granted must be picked up without a restart.
func TestCachedRunnerStatus_ForbiddenIsRecheckedAfterExpiry(t *testing.T) {
	inner := &stubRunnerStatus{err: forbiddenErr()}
	c := NewCachedRunnerStatus(inner, time.Minute)

	_, err := c.GetRunnerState("paritytech", 42)
	assert.Error(t, err)

	c.expireAll()
	inner.setErr(nil)
	inner.setState(online(true))

	state, err := c.GetRunnerState("paritytech", 42)
	assert.NoError(t, err)
	assert.True(t, state.Busy)
}

// Transient failures keep the old behaviour: retry promptly, because a retry
// is what resolves them.
func TestCachedRunnerStatus_TransientErrorsStillNotCached(t *testing.T) {
	inner := &stubRunnerStatus{err: errors.New("connection timed out")}
	c := NewCachedRunnerStatus(inner, time.Minute)

	for range 3 {
		_, err := c.GetRunnerState("paritytech", 42)
		assert.Error(t, err)
	}

	assert.Equal(t, 3, inner.callCount())
}

func forbiddenErr() error {
	return &github.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusForbidden},
		Message:  "Resource not accessible by integration",
	}
}
