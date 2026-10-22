package trayManager

import (
	"cattery/lib/githubClient"
	"errors"
	"sync"
	"testing"
	"time"

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

func TestCachedRunnerStatus_SeparatesRunners(t *testing.T) {
	inner := &stubRunnerStatus{state: online(true)}
	c := NewCachedRunnerStatus(inner, time.Minute)

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
