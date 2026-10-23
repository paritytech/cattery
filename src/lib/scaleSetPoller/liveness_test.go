package scaleSetPoller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A poller that has not completed a poll must report "no opinion" rather than
// a timestamp. The stale reaper reads ok=false as "I cannot tell", which lets
// GitHub decide; a bogus timestamp would instead assert something false in
// either direction.
func TestLastSuccessfulPoll_NoOpinionBeforeFirstPoll(t *testing.T) {
	p := &Poller{}

	_, ok := p.LastSuccessfulPoll()

	assert.False(t, ok)
}

func TestLastSuccessfulPoll_ReportsAfterPoll(t *testing.T) {
	p := &Poller{}
	p.markPolled()

	at, ok := p.LastSuccessfulPoll()

	assert.True(t, ok)
	assert.WithinDuration(t, time.Now(), at, time.Second)
}

// Review finding: Run used to stamp liveness right after CreateSession. Since
// runPoller re-enters Run every 30s after a failure, that made a listener
// failing every single poll look continuously live, defeating the guard it
// exists to feed. Only a completed poll may stamp.
func TestLastSuccessfulPoll_SessionSetupDoesNotCount(t *testing.T) {
	p := &Poller{}

	// Whatever Run does before the first GetMessage returns, liveness stays
	// unset. clearPolled is what Run defers; the stamp only comes from
	// markPolled on a completed poll.
	p.clearPolled()

	_, ok := p.LastSuccessfulPoll()

	assert.False(t, ok, "session setup is not evidence that messages are flowing")
}

// Review finding: a replica that stops polling a tray type must stop
// answering, instead of freezing a timestamp that ages into a permanent
// "blind listener" verdict.
func TestLastSuccessfulPoll_ClearedWhenRunExits(t *testing.T) {
	p := &Poller{}
	p.markPolled()
	_, ok := p.LastSuccessfulPoll()
	assert.True(t, ok)

	p.clearPolled()

	_, ok = p.LastSuccessfulPoll()
	assert.False(t, ok, "a stopped poller has no opinion, not a stale one")
}

// The Manager is what the tray manager actually consults.
func TestManager_LastSuccessfulPollUnknownForUnregisteredTrayType(t *testing.T) {
	m := NewManager()

	_, ok := m.LastSuccessfulPoll("never-registered")

	assert.False(t, ok, "with leader election the poller may live on another replica")
}

func TestManager_LastSuccessfulPollReportsRegisteredPoller(t *testing.T) {
	m := NewManager()
	p := &Poller{}
	p.markPolled()
	m.Register("parity-large-persistent", p)

	at, ok := m.LastSuccessfulPoll("parity-large-persistent")

	assert.True(t, ok)
	assert.WithinDuration(t, time.Now(), at, time.Second)
}
