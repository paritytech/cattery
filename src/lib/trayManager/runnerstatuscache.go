package trayManager

import (
	"cattery/lib/githubClient"
	"sync"
	"time"
)

// DefaultRunnerStateTTL is how long a cached runner state stays usable.
//
// Both callers run on roughly a one-minute cadence (the stale loop ticks every
// pollInterval, each agent pings every 60s), and during an incident every
// registered tray hits them at once. A TTL just under that cadence collapses
// the duplicate lookups — notably the reaper and the ping handler asking about
// the same tray — while staying far shorter than any threshold it feeds.
const DefaultRunnerStateTTL = 30 * time.Second

// CachedRunnerStatus memoises runner state for a short window.
//
// Entries are keyed by org and runner ID. Errors are not cached: a failed
// check keeps the tray alive, so retrying promptly is what resolves it.
type CachedRunnerStatus struct {
	inner RunnerStatusSource
	ttl   time.Duration

	mu      sync.Mutex
	entries map[runnerKey]cachedRunnerState
}

type runnerKey struct {
	org string
	id  int64
}

type cachedRunnerState struct {
	state   githubClient.RunnerState
	expires time.Time
}

// NewCachedRunnerStatus wraps source with a TTL cache. A non-positive ttl
// selects DefaultRunnerStateTTL.
func NewCachedRunnerStatus(source RunnerStatusSource, ttl time.Duration) *CachedRunnerStatus {
	if ttl <= 0 {
		ttl = DefaultRunnerStateTTL
	}
	return &CachedRunnerStatus{
		inner:   source,
		ttl:     ttl,
		entries: make(map[runnerKey]cachedRunnerState),
	}
}

func (c *CachedRunnerStatus) GetRunnerState(orgName string, runnerId int64) (githubClient.RunnerState, error) {
	key := runnerKey{org: orgName, id: runnerId}
	now := time.Now()

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.state, nil
	}
	c.mu.Unlock()

	// Deliberately not holding the lock across the HTTP call: a slow GitHub
	// must not serialise every other tray's lookup. A concurrent duplicate
	// call is cheaper than that contention.
	state, err := c.inner.GetRunnerState(orgName, runnerId)
	if err != nil {
		return githubClient.RunnerState{}, err
	}

	c.mu.Lock()
	c.entries[key] = cachedRunnerState{state: state, expires: now.Add(c.ttl)}
	// Trays are short-lived, so expired keys would otherwise accumulate for
	// the lifetime of the process.
	for k, v := range c.entries {
		if !now.Before(v.expires) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()

	return state, nil
}
