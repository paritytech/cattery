package maps

import "sync"

type ConcurrentMap[T comparable, Y interface{}] struct {
	rwMutex *sync.RWMutex
	_map    map[T]*Y
}

func NewConcurrentMap[T comparable, Y interface{}]() *ConcurrentMap[T, Y] {
	return &ConcurrentMap[T, Y]{
		rwMutex: &sync.RWMutex{},
		_map:    make(map[T]*Y),
	}
}

func (m *ConcurrentMap[T, Y]) Get(key T) *Y {
	m.rwMutex.RLock()
	defer m.rwMutex.RUnlock()

	if value, ok := m._map[key]; ok {
		return value
	}

	return nil
}

func (m *ConcurrentMap[T, Y]) Set(key T, value *Y) {
	m.rwMutex.Lock()
	defer m.rwMutex.Unlock()

	m._map[key] = value
}

func (m *ConcurrentMap[T, Y]) Delete(key T) {
	m.rwMutex.Lock()
	defer m.rwMutex.Unlock()

	delete(m._map, key)
}

func (m *ConcurrentMap[T, Y]) Len() int {
	m.rwMutex.RLock()
	defer m.rwMutex.RUnlock()

	return len(m._map)
}
