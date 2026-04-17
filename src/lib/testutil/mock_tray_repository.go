package testutil

import (
	"cattery/lib/trays"
	"cattery/lib/trays/repositories"
	"context"
	"sync"
	"time"
)

// Compile-time interface check.
var _ repositories.TrayRepository = (*MockTrayRepository)(nil)

// MockTrayRepository is a test double for repositories.TrayRepository.
type MockTrayRepository struct {
	mu          sync.Mutex
	Trays       map[string]*trays.Tray
	CountResult int
	CountErr    error
	SaveErr     error
	UpdateErr   error
	DeleteErr   error
	GetErr      error
	StaleTrays  []*trays.Tray
	StaleErr    error
}

func NewMockTrayRepository() *MockTrayRepository {
	return &MockTrayRepository{
		Trays: make(map[string]*trays.Tray),
	}
}

func (m *MockTrayRepository) GetById(_ context.Context, trayId string) (*trays.Tray, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	return m.Trays[trayId], nil
}

func (m *MockTrayRepository) Save(_ context.Context, tray *trays.Tray) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SaveErr != nil {
		return m.SaveErr
	}
	m.Trays[tray.Id] = tray
	return nil
}

func (m *MockTrayRepository) Delete(_ context.Context, trayId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DeleteErr != nil {
		return m.DeleteErr
	}
	delete(m.Trays, trayId)
	return nil
}

func (m *MockTrayRepository) UpdateStatus(_ context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string, jobName string, workflowName string) (*trays.Tray, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.UpdateErr != nil {
		return nil, m.UpdateErr
	}
	tray, ok := m.Trays[trayId]
	if !ok {
		return nil, nil
	}
	tray.Status = status
	if ghRunnerId != 0 {
		tray.GitHubRunnerId = ghRunnerId
	}
	if jobRunId != 0 {
		tray.JobRunId = jobRunId
	}
	if workflowRunId != 0 {
		tray.WorkflowRunId = workflowRunId
	}
	if repository != "" {
		tray.Repository = repository
	}
	if jobName != "" {
		tray.JobName = jobName
	}
	if workflowName != "" {
		tray.WorkflowName = workflowName
	}
	tray.StatusChanged = time.Now()
	return tray, nil
}

func (m *MockTrayRepository) CountActive(_ context.Context, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CountErr != nil {
		return 0, m.CountErr
	}
	return m.CountResult, nil
}

func (m *MockTrayRepository) List(_ context.Context) ([]*trays.Tray, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*trays.Tray, 0, len(m.Trays))
	for _, t := range m.Trays {
		result = append(result, t)
	}
	return result, nil
}

func (m *MockTrayRepository) GetStale(_ context.Context, _ time.Duration) ([]*trays.Tray, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StaleErr != nil {
		return nil, m.StaleErr
	}
	return m.StaleTrays, nil
}
