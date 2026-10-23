package trayManager

import (
	"cattery/lib/githubClient"
	"sync"
	"time"
)

// DefaultRunnerStateTTL is how long a cached busy state stays usable.
//
// Both callers run on roughly a one-minute cadence (the stale loop ticks every
// pollInterval, each agent pings every 60s), and during an incident every
// registered tray hits them at once. A TTL around half that cadence collapses
// the duplicate lookups — notably the reaper and the ping handler asking about
// the same tray — while staying far shorter than any threshold it feeds.
//
// Only busy states are cached, so the risk of a stale entry is bounded to
// keeping a finished tray for up to one extra TTL. The next lookup deletes it.
const DefaultRunnerStateTTL = 30 * time.Second

// CachedRunnerStatus memoises *busy* runner state for a short window.
//
// Entries are keyed by org and runner ID. Two kinds of answer are deliberately
// never cached:
//
//   - Transient errors. A failed check keeps the tray alive, so retrying
//     promptly is what resolves it; caching the failure would extend every
//     outage by the TTL. A 403 is the exception — see ForbiddenTTL.
//   - Idle runners. Caching "idle" is unsafe and nearly pointless. Unsafe,
//     because a tray can be read idle, pick up a job moments later, and — if
//     its listener is blind, so the tray status never moves — be deleted
//     against the cached verdict while that job runs. That is precisely the
//     failure this guard exists to prevent. Nearly pointless, because an idle
//     verdict leads to deletion, so the tray stops being queried; the repeat
//     lookups all come from busy trays, which stay candidates for as long as
//     their job runs.
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
	err     error
	expires time.Time
}

// ForbiddenTTL is how long a 403 is remembered.
//
// Unlike a timeout, a 403 is deterministic: the GitHub App is missing
// organization "Self-hosted runners: read" and will keep saying no until
// someone changes the App. A forbidden check keeps the tray, so without this
// every affected tray would be re-queried by both the reaper and the ping
// path every minute for the full hard threshold — hours of guaranteed-failing
// calls. It is still re-checked often enough to notice the permission being
// granted.
const ForbiddenTTL = 5 * time.Minute

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
		return entry.state, entry.err
	}
	c.mu.Unlock()

	// Deliberately not holding the lock across the HTTP call: a slow GitHub
	// must not serialise every other tray's lookup. A concurrent duplicate
	// call is cheaper than that contention.
	state, err := c.inner.GetRunnerState(orgName, runnerId)
	if err != nil {
		if githubClient.IsForbidden(err) {
			c.mu.Lock()
			c.entries[key] = cachedRunnerState{err: err, expires: now.Add(ForbiddenTTL)}
			c.mu.Unlock()
		}
		return githubClient.RunnerState{}, err
	}

	c.mu.Lock()
	if state.Busy {
		c.entries[key] = cachedRunnerState{state: state, expires: now.Add(c.ttl)}
	} else {
		// Drop any entry we are superseding, so a runner that goes from busy
		// to idle is not answered from the stale busy entry.
		delete(c.entries, key)
	}
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

// expireAll ages every cached entry out immediately. Test-only: it lets a
// test exercise TTL expiry without sleeping.
func (c *CachedRunnerStatus) expireAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	past := time.Now().Add(-time.Nanosecond)
	for k, v := range c.entries {
		v.expires = past
		c.entries[k] = v
	}
}
