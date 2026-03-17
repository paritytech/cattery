package scaleSetPoller

import "sync"

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
