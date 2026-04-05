//go:build integration

package handlers

import (
	"bytes"
	"cattery/lib/agents"
	"cattery/lib/config"
	"cattery/lib/messages"
	"cattery/lib/restarter"
	restarterRepos "cattery/lib/restarter/repositories"
	"cattery/lib/scaleSetClient"
	"cattery/lib/scaleSetPoller"
	"cattery/lib/trays"
	"cattery/lib/trays/providers"
	"cattery/lib/trays/repositories"
	"cattery/lib/trayManager"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// --- Test infrastructure ---

// mockJitConfigGenerator implements scaleSetClient.JitConfigGenerator
type mockJitConfigGenerator struct {
	runnerID int
}

func (m *mockJitConfigGenerator) GenerateJitRunnerConfig(_ context.Context, runnerName string) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	return &scaleset.RunnerScaleSetJitRunnerConfig{
		Runner: &scaleset.RunnerReference{
			ID:   m.runnerID,
			Name: runnerName,
		},
		EncodedJITConfig: "dGVzdC1qaXQtY29uZmln", // base64("test-jit-config")
	}, nil
}

var _ scaleSetClient.JitConfigGenerator = (*mockJitConfigGenerator)(nil)

// mockProviderFactory implements providers.TrayProviderFactory
type integrationMockProvider struct{}

func (m *integrationMockProvider) GetProviderName() string       { return "mock" }
func (m *integrationMockProvider) RunTray(_ *trays.Tray) error   { return nil }
func (m *integrationMockProvider) CleanTray(_ *trays.Tray) error { return nil }

type integrationMockProviderFactory struct{}

func (m *integrationMockProviderFactory) GetProvider(_ string) (providers.TrayProvider, error) {
	return &integrationMockProvider{}, nil
}
func (m *integrationMockProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.TrayProvider, error) {
	return &integrationMockProvider{}, nil
}

// testHarness holds all the components needed for integration tests
type testHarness struct {
	handlers       *Handlers
	mux            *http.ServeMux
	trayRepo       *repositories.MongodbTrayRepository
	restarterRepo  *restarterRepos.MongodbRestarterRepository
	tm             *trayManager.TrayManager
	db             *mongo.Database
}

func setupIntegrationTest(t *testing.T) *testHarness {
	t.Helper()

	// Set up config
	config.SetForTest(t, &config.CatteryConfig{
		Server: config.ServerConfig{
			ListenAddress: ":0",
			AdvertiseUrl:  "http://localhost:0",
		},
		TrayTypes: []*config.TrayType{
			{
				Name:      "test-type",
				Provider:  "mock",
				GitHubOrg: "test-org",
				MaxTrays:  10,
				RunnerGroupId: 1,
			},
		},
	})

	// Connect to MongoDB
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI("mongodb://localhost").SetServerAPIOptions(serverAPI)

	client, err := mongo.Connect(opts)
	require.NoError(t, err)

	require.NoError(t, client.Ping(context.Background(), nil))

	db := client.Database("cattery_integration_test")

	// Clean up collections
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
	})

	// Set up tray repository
	trayRepo := repositories.NewMongodbTrayRepository()
	trayRepo.Connect(db.Collection("trays"))

	// Set up restarter repository
	restartRepo := restarterRepos.NewMongodbRestarterRepository()
	restartRepo.Connect(db.Collection("restarters"))

	// Set up managers
	tm := trayManager.NewTrayManager(trayRepo, &integrationMockProviderFactory{})
	rm := restarter.NewWorkflowRestarter(restartRepo)

	// Set up scale set manager with mock JIT client
	ssm := scaleSetPoller.NewManager()
	mockJit := &mockJitConfigGenerator{runnerID: 42}
	poller := scaleSetPoller.NewPollerWithJitClient(mockJit, config.Get().TrayTypes[0], tm)
	ssm.Register("test-type", poller)

	h := &Handlers{
		TrayManager:     tm,
		RestartManager:  rm,
		ScaleSetManager: ssm,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/register/{id}", h.AgentRegister)
	mux.HandleFunc("POST /agent/unregister/{id}", h.AgentUnregister)
	mux.HandleFunc("POST /agent/ping/{id}", h.AgentPing)
	mux.HandleFunc("POST /agent/interrupt/{id}", h.AgentInterrupt)

	return &testHarness{
		handlers:      h,
		mux:           mux,
		trayRepo:      trayRepo,
		restarterRepo: restartRepo,
		tm:            tm,
		db:            db,
	}
}

// createTray is a helper that creates a tray in the database via the TrayManager
func (th *testHarness) createTray(t *testing.T) *trays.Tray {
	t.Helper()
	trayType := config.Get().TrayTypes[0]
	err := th.tm.CreateTray(context.Background(), trayType)
	require.NoError(t, err)

	// Find the created tray (there should be exactly one)
	active, err := th.trayRepo.CountActive(context.Background(), trayType.Name)
	require.NoError(t, err)
	require.Equal(t, 1, active)

	// Get it by listing stale with very long duration (returns all)
	allTrays, err := th.trayRepo.GetStale(context.Background(), 999*time.Hour)
	if err != nil || len(allTrays) == 0 {
		// GetStale might not return Creating trays; use a direct approach
		// Create tray directly
		t.Fatal("Could not retrieve created tray")
	}
	return allTrays[0]
}

// insertTray inserts a tray directly into MongoDB, preserving the StatusChanged value.
// (trayRepo.Save overwrites StatusChanged with time.Now(), which breaks stale tray tests.)
func (th *testHarness) insertTray(t *testing.T, tray *trays.Tray) {
	t.Helper()
	_, err := th.db.Collection("trays").InsertOne(context.Background(), tray)
	require.NoError(t, err)
}

// --- Integration Tests ---

func TestIntegration_HappyPath_Register_Ping_Unregister(t *testing.T) {
	th := setupIntegrationTest(t)

	// 1. Create a tray (simulates what ScaleForDemand does)
	tray := &trays.Tray{
		Id:            "test-type-abc123",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusCreating,
		StatusChanged: time.Now(),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	// 2. Register agent
	req := httptest.NewRequest("GET", "/agent/register/test-type-abc123", nil)
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var registerResp messages.RegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&registerResp))
	assert.Equal(t, "test-type-abc123", registerResp.Agent.AgentId)
	assert.Equal(t, int64(42), registerResp.Agent.RunnerId)
	assert.NotEmpty(t, registerResp.JitConfig)

	// Verify tray is now Registered in DB
	dbTray, err := th.trayRepo.GetById(context.Background(), "test-type-abc123")
	require.NoError(t, err)
	assert.Equal(t, trays.TrayStatusRegistered, dbTray.Status)
	assert.Equal(t, int64(42), dbTray.GitHubRunnerId)

	// 3. Ping — tray is recently registered, should not terminate
	req = httptest.NewRequest("POST", "/agent/ping/test-type-abc123", nil)
	w = httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var pingResp messages.PingResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&pingResp))
	assert.False(t, pingResp.Terminate)

	// 4. Unregister
	unregBody, _ := json.Marshal(messages.UnregisterRequest{
		Agent:   registerResp.Agent,
		Reason:  messages.UnregisterReasonDone,
		Message: "job done",
	})
	req = httptest.NewRequest("POST", "/agent/unregister/test-type-abc123", bytes.NewReader(unregBody))
	w = httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify tray is deleted from DB
	dbTray, err = th.trayRepo.GetById(context.Background(), "test-type-abc123")
	require.NoError(t, err)
	assert.Nil(t, dbTray)
}

func TestIntegration_PingTerminatesStaleAgent(t *testing.T) {
	th := setupIntegrationTest(t)

	// Insert a tray that's been in Registered state for 5 minutes (stale)
	tray := &trays.Tray{
		Id:            "test-type-stale1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRegistered,
		StatusChanged: time.Now().Add(-5 * time.Minute),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	// Ping should return terminate=true
	req := httptest.NewRequest("POST", "/agent/ping/test-type-stale1", nil)
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var pingResp messages.PingResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&pingResp))
	assert.True(t, pingResp.Terminate)
	assert.Contains(t, pingResp.Message, "not changed in 2 minutes")
}

func TestIntegration_PingDoesNotTerminateRunningTray(t *testing.T) {
	th := setupIntegrationTest(t)

	// A running tray, even if StatusChanged is old, should NOT be terminated
	tray := &trays.Tray{
		Id:            "test-type-running1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRunning,
		StatusChanged: time.Now().Add(-10 * time.Minute),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	req := httptest.NewRequest("POST", "/agent/ping/test-type-running1", nil)
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var pingResp messages.PingResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&pingResp))
	assert.False(t, pingResp.Terminate)
}

func TestIntegration_AgentPreemption(t *testing.T) {
	th := setupIntegrationTest(t)

	tray := &trays.Tray{
		Id:            "test-type-preempt1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRunning,
		StatusChanged: time.Now(),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	// Agent sends unregister with Preempted reason
	unregBody, _ := json.Marshal(messages.UnregisterRequest{
		Agent:   agents.Agent{AgentId: "test-type-preempt1"},
		Reason:  messages.UnregisterReasonPreempted,
		Message: "VM preempted",
	})
	req := httptest.NewRequest("POST", "/agent/unregister/test-type-preempt1", bytes.NewReader(unregBody))
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify tray is deleted
	dbTray, err := th.trayRepo.GetById(context.Background(), "test-type-preempt1")
	require.NoError(t, err)
	assert.Nil(t, dbTray)
}

func TestIntegration_InterruptSavesRestartRequest(t *testing.T) {
	th := setupIntegrationTest(t)

	tray := &trays.Tray{
		Id:            "test-type-interrupt1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRunning,
		WorkflowRunId: 12345,
		Repository:    "test-org/test-repo",
		StatusChanged: time.Now(),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	req := httptest.NewRequest("POST", "/agent/interrupt/test-type-interrupt1", nil)
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify restart request saved in MongoDB
	requests, err := th.restarterRepo.GetAllPendingRestartRequests(context.Background())
	require.NoError(t, err)
	require.Len(t, requests, 1)
	assert.Equal(t, int64(12345), requests[0].WorkflowRunId)
	assert.Equal(t, "test-org", requests[0].OrgName)
	assert.Equal(t, "test-org/test-repo", requests[0].RepoName)
}

func TestIntegration_RegisterUnknownTray(t *testing.T) {
	th := setupIntegrationTest(t)

	req := httptest.NewRequest("GET", "/agent/register/nonexistent", nil)
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestIntegration_StaleTrayCleanup(t *testing.T) {
	th := setupIntegrationTest(t)

	// Insert a stale tray (status unchanged for 5 minutes)
	tray := &trays.Tray{
		Id:            "test-type-stale-cleanup",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRegistering,
		StatusChanged: time.Now().Add(-5 * time.Minute),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	// Verify tray appears in stale query
	stale, err := th.trayRepo.GetStale(context.Background(), 2*time.Minute)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "test-type-stale-cleanup", stale[0].Id)

	// Delete it (simulates what HandleStale does)
	_, err = th.tm.DeleteTray(context.Background(), tray.Id)
	require.NoError(t, err)

	// Verify tray gone
	dbTray, err := th.trayRepo.GetById(context.Background(), "test-type-stale-cleanup")
	require.NoError(t, err)
	assert.Nil(t, dbTray)
}

func TestIntegration_AuthWithSecret(t *testing.T) {
	th := setupIntegrationTest(t)

	// Override config with a secret
	config.SetForTest(t, &config.CatteryConfig{
		Server: config.ServerConfig{
			AgentSecret:   "test-secret",
			AdvertiseUrl:  "http://localhost:0",
			ListenAddress: ":0",
		},
		TrayTypes: []*config.TrayType{
			{
				Name:      "test-type",
				Provider:  "mock",
				GitHubOrg: "test-org",
				MaxTrays:  10,
				RunnerGroupId: 1,
			},
		},
	})

	tray := &trays.Tray{
		Id:            "test-type-auth1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRunning,
		StatusChanged: time.Now(),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	// Request without token — should fail
	req := httptest.NewRequest("POST", "/agent/ping/test-type-auth1", nil)
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Request with wrong token — should fail
	req = httptest.NewRequest("POST", "/agent/ping/test-type-auth1", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Request with correct token — should succeed
	req = httptest.NewRequest("POST", "/agent/ping/test-type-auth1", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	w = httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestIntegration_DoubleUnregister(t *testing.T) {
	th := setupIntegrationTest(t)

	tray := &trays.Tray{
		Id:            "test-type-double1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRunning,
		StatusChanged: time.Now(),
		ProviderData:  make(map[string]string),
	}
	th.insertTray(t, tray)

	unregBody, _ := json.Marshal(messages.UnregisterRequest{
		Reason: messages.UnregisterReasonDone,
	})

	// First unregister — should succeed
	req := httptest.NewRequest("POST", "/agent/unregister/test-type-double1", bytes.NewReader(unregBody))
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Second unregister — tray gone, should get 404
	req = httptest.NewRequest("POST", fmt.Sprintf("/agent/unregister/test-type-double1"), bytes.NewReader(unregBody))
	w = httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
