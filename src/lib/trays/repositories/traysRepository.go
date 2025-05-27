package repositories

import (
	"cattery/lib/trays"
	"sync"
)

type MemTrayRepository struct {
	ITrayRepository
	trays map[string]*trays.Tray
	mutex sync.RWMutex
}

func NewMemTrayRepository() *MemTrayRepository {
	return &MemTrayRepository{
		trays: make(map[string]*trays.Tray),
		mutex: sync.RWMutex{},
	}
}

func (r *MemTrayRepository) Get(trayId string) (*trays.Tray, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	tray, exists := r.trays[trayId]
	if !exists {
		return nil, nil
	}

	return tray, nil
}

func (r *MemTrayRepository) Save(tray *trays.Tray) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.trays[tray.Id()] = tray
	return nil
}

func (r *MemTrayRepository) Delete(trayId string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	delete(r.trays, trayId)
	return nil
}

func (r *MemTrayRepository) Len() int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	return len(r.trays)
}
