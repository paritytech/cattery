package scaleSetPoller

import (
	"sort"
	"sync"
)

type Manager struct {
	mu      sync.RWMutex
	pollers map[string]*Poller
	Wg      sync.WaitGroup
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
