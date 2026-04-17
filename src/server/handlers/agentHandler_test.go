package handlers

import (
	"bytes"
	"cattery/lib/config"
	"cattery/lib/messages"
	"cattery/lib/restarter"
	restarterRepo "cattery/lib/restarter/repositories"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/testutil"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
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

// --- Mock provider factory ---

type mockProvider struct{}

func (m *mockProvider) GetProviderName() string                            { return "mock" }
func (m *mockProvider) RunTray(_ context.Context, _ *trays.Tray) error     { return nil }
func (m *mockProvider) CleanTray(_ context.Context, _ *trays.Tray) error   { return nil }

type mockProviderFactory struct{}

func (m *mockProviderFactory) GetProvider(_ string) (providers.TrayProvider, error) {
	return &mockProvider{}, nil
}
func (m *mockProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.TrayProvider, error) {
	return &mockProvider{}, nil
}

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

var _ restarterRepo.RestarterRepository = (*mockRestarterRepository)(nil)

// --- Helper to create test handlers ---

func setupHandlers(repo *testutil.MockTrayRepository) *Handlers {
	return &Handlers{
		TrayManager:     trayManager.NewTrayManager(repo, &mockProviderFactory{}),
		RestartManager:  restarter.NewWorkflowRestarter(&mockRestarterRepository{}),
		ScaleSetManager: scaleSetPoller.NewManager(),
	}
}

func setupHandlersWithRestarter(repo *testutil.MockTrayRepository, restarterRepo *mockRestarterRepository) *Handlers {
	return &Handlers{
		TrayManager:     trayManager.NewTrayManager(repo, &mockProviderFactory{}),
		RestartManager:  restarter.NewWorkflowRestarter(restarterRepo),
		ScaleSetManager: scaleSetPoller.NewManager(),
	}
}

// --- AgentPing tests ---

func TestAgentPing_TrayNotFound(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/nonexistent/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown agent")
}

func TestAgentPing_TrayRunning(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{
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
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		Status:        trays.TrayStatusRegistered,
		StatusChanged: time.Now().Add(-5 * time.Minute),
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
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		Status:        trays.TrayStatusRegistered,
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

// --- AgentUnregister tests ---

func TestAgentUnregister_TrayNotFound(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/unregister", h.AgentUnregister)

	req := httptest.NewRequest("POST", "/agent/nonexistent/unregister", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAgentUnregister_InvalidBody(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRunning}
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
	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/interrupt", h.AgentInterrupt)

	req := httptest.NewRequest("POST", "/agent/nonexistent/interrupt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAgentInterrupt_Success(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{
		Id:            "tray-1",
		WorkflowRunId: 123,
		GitHubOrgName: "test-org",
		Repository:    "test-org/repo",
	}
	restarterRepository := &mockRestarterRepository{}
	h := setupHandlersWithRestarter(repo, restarterRepository)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/interrupt", h.AgentInterrupt)

	req := httptest.NewRequest("POST", "/agent/tray-1/interrupt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, restarterRepository.requests, 1)
	assert.Equal(t, int64(123), restarterRepository.requests[0].WorkflowRunId)
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

// --- AgentRegister tests ---

func TestAgentRegister_UnknownTray(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/register/{id}", h.AgentRegister)

	req := httptest.NewRequest("GET", "/agent/register/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown agent")
}

// --- authenticateAgent tests ---

func TestAuthenticateAgent_NoSecretAndTrayExists(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1"}
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthenticateAgent_NoSecretAndTrayMissing(t *testing.T) {
	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/nonexistent/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAuthenticateAgent_ValidTokenAndTrayExists(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server: config.ServerConfig{AgentSecret: "test-secret-123"},
	})

	repo := testutil.NewMockTrayRepository()
	repo.Trays["tray-1"] = &trays.Tray{Id: "tray-1", Status: trays.TrayStatusRunning}
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	req.Header.Set("Authorization", "Bearer test-secret-123")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthenticateAgent_MissingHeader(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server: config.ServerConfig{AgentSecret: "test-secret-123"},
	})

	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthenticateAgent_WrongSecret(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{
		Server: config.ServerConfig{AgentSecret: "test-secret-123"},
	})

	repo := testutil.NewMockTrayRepository()
	h := setupHandlers(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/{id}/ping", h.AgentPing)

	req := httptest.NewRequest("POST", "/agent/tray-1/ping", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
