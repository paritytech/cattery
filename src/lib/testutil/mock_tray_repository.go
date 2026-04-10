package testutil

import (
	"cattery/lib/trays"
	"cattery/lib/trays/repositories"
	"context"
	"time"
)

// Compile-time interface check.
var _ repositories.TrayRepository = (*MockTrayRepository)(nil)

// MockTrayRepository is a test double for repositories.TrayRepository.
type MockTrayRepository struct {
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
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	return m.Trays[trayId], nil
}

func (m *MockTrayRepository) Save(_ context.Context, tray *trays.Tray) error {
	if m.SaveErr != nil {
		return m.SaveErr
	}
	m.Trays[tray.Id] = tray
	return nil
}

func (m *MockTrayRepository) Delete(_ context.Context, trayId string) error {
	if m.DeleteErr != nil {
		return m.DeleteErr
	}
	delete(m.Trays, trayId)
	return nil
}

func (m *MockTrayRepository) UpdateStatus(_ context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string) (*trays.Tray, error) {
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
	tray.StatusChanged = time.Now()
	return tray, nil
}

func (m *MockTrayRepository) CountActive(_ context.Context, _ string) (int, error) {
	if m.CountErr != nil {
		return 0, m.CountErr
	}
	return m.CountResult, nil
}

func (m *MockTrayRepository) List(_ context.Context) ([]*trays.Tray, error) {
	result := make([]*trays.Tray, 0, len(m.Trays))
	for _, t := range m.Trays {
		result = append(result, t)
	}
	return result, nil
}

func (m *MockTrayRepository) GetStale(_ context.Context, _ time.Duration) ([]*trays.Tray, error) {
	if m.StaleErr != nil {
		return nil, m.StaleErr
	}
	return m.StaleTrays, nil
}
