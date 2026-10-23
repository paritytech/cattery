package scaleSetPoller

import (
	"sort"
	"sync"
	"time"
)

type Manager struct {
	mu      sync.RWMutex
	pollers map[string]*Poller
}

func NewManager() *Manager {
	return &Manager{
		pollers: make(map[string]*Poller),
	}
}

func (m *Manager) Register(trayTypeName string, poller *Poller) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollers[trayTypeName] = poller
}

func (m *Manager) GetPoller(trayTypeName string) *Poller {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pollers[trayTypeName]
}

// LastSuccessfulPoll reports when the listener for a tray type last completed
// a message poll.
//
// The second return is false when this replica has no answer: either no poller
// is registered for the tray type (with leader election the poller may be on
// another replica), or its first poll of the session has not finished.
// "No answer" is not "unhealthy" — callers must not infer a blind listener
// from it.
func (m *Manager) LastSuccessfulPoll(trayTypeName string) (time.Time, bool) {
	p := m.GetPoller(trayTypeName)
	if p == nil {
		return time.Time{}, false
	}
	return p.LastSuccessfulPoll()
}

// MessageHistory returns all recent messages across all pollers, newest first.
func (m *Manager) MessageHistory() []*Message {
	m.mu.RLock()
	pollers := make([]*Poller, 0, len(m.pollers))
	for _, p := range m.pollers {
		pollers = append(pollers, p)
	}
	m.mu.RUnlock()

	var all []*Message
	for _, p := range pollers {
		all = append(all, p.History().Recent()...)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Time.After(all[j].Time)
	})
	return all
}
