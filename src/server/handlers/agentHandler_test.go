package handlers

import (
	"bytes"
	"cattery/lib/messages"
	"cattery/lib/restarter"
	restarterRepo "cattery/lib/restarter/repositories"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trays"
	"cattery/lib/trays/repositories"
	"cattery/lib/trayManager"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// --- Mock tray repository ---

type mockTrayRepository struct {
	trays     map[string]*trays.Tray
	saveErr   error
	getErr    error
	updateErr error
	deleteErr error
}

func newMockTrayRepo() *mockTrayRepository {
	return &mockTrayRepository{
		trays: make(map[string]*trays.Tray),
	}
}

func (m *mockTrayRepository) GetById(_ context.Context, trayId string) (*trays.Tray, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.trays[trayId], nil
}

func (m *mockTrayRepository) Save(_ context.Context, tray *trays.Tray) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.trays[tray.Id] = tray
	return nil
}

func (m *mockTrayRepository) Delete(_ context.Context, trayId string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.trays, trayId)
	return nil
}

func (m *mockTrayRepository) UpdateStatus(_ context.Context, trayId string, status trays.TrayStatus, _ int64, _ int64, _ int64, _ string) (*trays.Tray, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	tray, ok := m.trays[trayId]
	if !ok {
		return nil, nil
	}
	tray.Status = status
	tray.StatusChanged = time.Now()
	return tray, nil
}

func (m *mockTrayRepository) CountActive(_ context.Context, _ string) (int, error) {
	return len(m.trays), nil
}

func (m *mockTrayRepository) GetStale(_ context.Context, _ time.Duration) ([]*trays.Tray, error) {
	return nil, nil
}

// Verify interface compliance
var _ repositories.ITrayRepository = (*mockTrayRepository)(nil)

// --- Mock restarter repository ---

type mockRestarterRepository struct {
	requests []restarterRepo.RestartRequest
	saveErr  error
}

func (m *mockRestarterRepository) SaveRestartRequest(_ context.Context, workflowRunId int64, orgName string, repoName string) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.requests = append(m.requests, restarterRepo.RestartRequest{
		WorkflowRunId: workflowRunId,
		OrgName:       orgName,
		RepoName:      repoName,
		CreatedAt:     time.Now(),
	})
	return nil
}

func (m *mockRestarterRepository) DeleteRestartRequest(_ context.Context, _ int64) error {
	return nil
}

func (m *mockRestarterRepository) GetAllPendingRestartRequests(_ context.Context) ([]restarterRepo.RestartRequest, error) {
	return m.requests, nil
}

var _ restarterRepo.IRestarterRepository = (*mockRestarterRepository)(nil)

// --- Helper to create test handlers ---

func setupHandlers(repo *mockTrayRepository) *Handlers {
	return &Handlers{
		TrayManager:     trayManager.NewTrayManager(repo),
		RestartManager:  restarter.NewWorkflowRestarter(&mockRestarterRepository{}),
		ScaleSetManager: scaleSetPoller.NewManager(),
	}
}

func setupHandlersWithRestarter(repo *mockTrayRepository, restarterRepo *mockRestarterRepository) *Handlers {
	return &Handlers{
		TrayManager:     trayManager.NewTrayManager(repo),
		RestartManager:  restarter.NewWorkflowRestarter(restarterRepo),
		ScaleSetManager: scaleSetPoller.NewManager(),
	}
}

// --- AgentPing tests ---

func TestAgentPing_TrayNotFound(t *testing.T) {
	repo := newMockTrayRepo()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/nonexistent/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp messages.PingResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.True(t, resp.Terminate)
	assert.Contains(t, resp.Message, "not found")
}

func TestAgentPing_TrayRunning(t *testing.T) {
	repo := newMockTrayRepo()
	repo.trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		Status:        trays.TrayStatusRunning,
		StatusChanged: time.Now(),
	}
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp messages.PingResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.False(t, resp.Terminate)
}

func TestAgentPing_StaleNonRunningTray(t *testing.T) {
	repo := newMockTrayRepo()
	repo.trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		Status:        trays.TrayStatusRegistered,
		StatusChanged: time.Now().Add(-5 * time.Minute), // stale
	}
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp messages.PingResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.True(t, resp.Terminate)
	assert.Contains(t, resp.Message, "not changed in 2 minutes")
}

func TestAgentPing_RecentNonRunningTray(t *testing.T) {
	repo := newMockTrayRepo()
	repo.trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		Status:        trays.TrayStatusRegistered,
		StatusChanged: time.Now(), // just changed
	}
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp messages.PingResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.False(t, resp.Terminate)
}

// --- AgentUnregister tests ---

func TestAgentUnregister_TrayNotFound(t *testing.T) {
	repo := newMockTrayRepo()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/unregister", h.AgentUnregister)

	req := httptest.NewRequest("POST", "/agent/nonexistent/unregister", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAgentUnregister_InvalidBody(t *testing.T) {
	repo := newMockTrayRepo()
	repo.trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRunning}
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/unregister", h.AgentUnregister)

	req := httptest.NewRequest("POST", "/agent/tray-1/unregister", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- AgentInterrupt tests ---

func TestAgentInterrupt_TrayNotFound(t *testing.T) {
	repo := newMockTrayRepo()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/interrupt", h.AgentInterrupt)

	req := httptest.NewRequest("POST", "/agent/nonexistent/interrupt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestAgentInterrupt_Success(t *testing.T) {
	repo := newMockTrayRepo()
	repo.trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		WorkflowRunId: 123,
		GitHubOrgName: "test-org",
		Repository:    "test-org/repo",
	}
	restarterRepo := &mockRestarterRepository{}
	h := setupHandlersWithRestarter(repo, restarterRepo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/interrupt", h.AgentInterrupt)

	req := httptest.NewRequest("POST", "/agent/tray-1/interrupt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, restarterRepo.requests, 1)
	assert.Equal(t, int64(123), restarterRepo.requests[0].WorkflowRunId)
}

// --- writeResponse tests ---

func TestWriteResponse(t *testing.T) {
	w := httptest.NewRecorder()
	resp := &messages.PingResponse{Terminate: false, Message: "ok"}

	logger := log.WithField("test", true)
	writeResponse(w, resp, logger)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var decoded messages.PingResponse
	err := json.NewDecoder(w.Body).Decode(&decoded)
	assert.NoError(t, err)
	assert.False(t, decoded.Terminate)
	assert.Equal(t, "ok", decoded.Message)
}
