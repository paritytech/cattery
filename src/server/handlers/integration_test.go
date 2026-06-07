//go:build integration_mongo

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
	"sync"
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

func (m *integrationMockProvider) GetProviderName() string                             { return "mock" }
func (m *integrationMockProvider) StartDeploy(_ context.Context, _ *trays.Tray) error { return nil }
func (m *integrationMockProvider) WaitDeploy(_ context.Context, _ *trays.Tray) error  { return nil }
func (m *integrationMockProvider) CleanTray(_ context.Context, _ *trays.Tray) error   { return nil }

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
	return setupIntegrationTestWithFactory(t, &integrationMockProviderFactory{})
}

func setupIntegrationTestWithFactory(t *testing.T, factory providers.TrayProviderFactory) *testHarness {
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
	tm := trayManager.NewTrayManager(trayRepo, factory)
	rm := restarter.NewWorkflowRestarter(restartRepo)

	// Scale set manager holds no pollers here; the agent register handler gets
	// its JIT generator from the registry, decoupled from the poller.
	ssm := scaleSetPoller.NewManager()
	jitRegistry := scaleSetClient.NewJitRegistry()
	jitRegistry.Register("test-type", &mockJitConfigGenerator{runnerID: 42})

	h := &Handlers{
		TrayManager:     tm,
		RestartManager:  rm,
		ScaleSetManager: ssm,
		JitRegistry:     jitRegistry,
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

	// Get it by listing stale with a tiny threshold (matches everything in those statuses).
	allTrays, err := th.trayRepo.GetStale(context.Background(), allStatusesThresholds(time.Nanosecond))
	if err != nil || len(allTrays) == 0 {
		t.Fatal("Could not retrieve created tray")
	}
	return allTrays[0]
}

// allStatusesThresholds builds a thresholds map covering every non-running
// status with the same duration. Used in tests to fetch trays regardless of status.
func allStatusesThresholds(d time.Duration) map[trays.TrayStatus]time.Duration {
	return map[trays.TrayStatus]time.Duration{
		trays.TrayStatusCreating:    d,
		trays.TrayStatusRegistering: d,
		trays.TrayStatusRegistered:  d,
		trays.TrayStatusDeleting:    d,
	}
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

	// Insert a tray that's been in Registered state for 20 minutes (stale)
	tray := &trays.Tray{
		Id:            "test-type-stale1",
		TrayTypeName:  "test-type",
		ProviderName:  "mock",
		GitHubOrgName: "test-org",
		Status:        trays.TrayStatusRegistered,
		StatusChanged: time.Now().Add(-20 * time.Minute),
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
	assert.Contains(t, pingResp.Message, "not changed in 15 minutes")
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
	stale, err := th.trayRepo.GetStale(context.Background(), map[trays.TrayStatus]time.Duration{
		trays.TrayStatusRegistering: 2 * time.Minute,
	})
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

// --- CreateTray lifecycle tests ---

// observingProvider populates ProviderData during StartDeploy and reports
// what it saw to the test. Used to verify the row is queryable from Mongo
// at the moment StartDeploy runs (the original race fix) and that the data
// it populates is persisted via SetProviderData afterward.
type observingProvider struct {
	repo            *repositories.MongodbTrayRepository
	observedTrayId  string
	rowVisibleInDB  bool
	providerDataSet map[string]string
}

func (p *observingProvider) GetProviderName() string { return "mock" }
func (p *observingProvider) StartDeploy(ctx context.Context, tray *trays.Tray) error {
	p.observedTrayId = tray.Id
	got, err := p.repo.GetById(ctx, tray.Id)
	if err == nil && got != nil {
		p.rowVisibleInDB = true
	}
	tray.ProviderData["zone"] = "us-east-1"
	tray.ProviderData["instanceId"] = "i-test-12345"
	p.providerDataSet = map[string]string{
		"zone":       tray.ProviderData["zone"],
		"instanceId": tray.ProviderData["instanceId"],
	}
	return nil
}
func (p *observingProvider) WaitDeploy(_ context.Context, _ *trays.Tray) error { return nil }
func (p *observingProvider) CleanTray(_ context.Context, _ *trays.Tray) error  { return nil }

type observingProviderFactory struct {
	provider *observingProvider
}

func (f *observingProviderFactory) GetProvider(_ string) (providers.TrayProvider, error) {
	return f.provider, nil
}
func (f *observingProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.TrayProvider, error) {
	return f.provider, nil
}

func TestIntegration_CreateTray_RowSavedBeforeStartDeploy(t *testing.T) {
	// The original bug: agent boots and registers before CreateTray finishes.
	// Verify that with save-before-deploy, the row is queryable from Mongo by
	// the time StartDeploy runs — and that ProviderData populated inside
	// StartDeploy survives the round-trip via SetProviderData.
	prov := &observingProvider{}
	factory := &observingProviderFactory{provider: prov}
	th := setupIntegrationTestWithFactory(t, factory)
	prov.repo = th.trayRepo

	err := th.tm.CreateTray(context.Background(), config.Get().TrayTypes[0])
	require.NoError(t, err)

	require.NotEmpty(t, prov.observedTrayId, "provider must have run")
	assert.True(t, prov.rowVisibleInDB,
		"row must be present in Mongo when StartDeploy runs (original race fix)")

	// ProviderData populated inside StartDeploy must round-trip via Mongo.
	final, err := th.trayRepo.GetById(context.Background(), prov.observedTrayId)
	require.NoError(t, err)
	require.NotNil(t, final, "row must still exist after CreateTray completes")
	assert.Equal(t, trays.TrayStatusCreating, final.Status,
		"CreateTray itself doesn't change status — agent register does")
	assert.Equal(t, "us-east-1", final.ProviderData["zone"])
	assert.Equal(t, "i-test-12345", final.ProviderData["instanceId"])
}

// gatedProvider blocks StartDeploy on a release channel so the test can
// orchestrate concurrent operations during deploy.
type gatedProvider struct {
	startEntered chan struct{}
	startRelease chan struct{}

	mu      sync.Mutex
	cleaned []string
}

func (p *gatedProvider) GetProviderName() string { return "mock" }
func (p *gatedProvider) StartDeploy(_ context.Context, tray *trays.Tray) error {
	close(p.startEntered)
	<-p.startRelease
	tray.ProviderData["zone"] = "us-east-1"
	return nil
}
func (p *gatedProvider) WaitDeploy(_ context.Context, _ *trays.Tray) error { return nil }
func (p *gatedProvider) CleanTray(_ context.Context, tray *trays.Tray) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cleaned = append(p.cleaned, tray.Id)
	return nil
}

type gatedProviderFactory struct {
	provider *gatedProvider
}

func (f *gatedProviderFactory) GetProvider(_ string) (providers.TrayProvider, error) {
	return f.provider, nil
}
func (f *gatedProviderFactory) GetProviderForTray(_ *trays.Tray) (providers.TrayProvider, error) {
	return f.provider, nil
}

func TestIntegration_CreateTray_ConcurrentUnregister(t *testing.T) {
	// Concurrency at integration level: while StartDeploy is in flight, an
	// unregister HTTP request fires. The unregister handler marks the row
	// as deleting, runs CleanTray, and removes the row. CreateTray then
	// proceeds, finds the row gone on its post-StartDeploy SetProviderData,
	// and exits cleanly. End state: no row, no leaked provider resources.
	prov := &gatedProvider{
		startEntered: make(chan struct{}),
		startRelease: make(chan struct{}),
	}
	th := setupIntegrationTestWithFactory(t, &gatedProviderFactory{provider: prov})

	createDone := make(chan error, 1)
	go func() {
		createDone <- th.tm.CreateTray(context.Background(), config.Get().TrayTypes[0])
	}()

	// Wait for StartDeploy to be entered — at this point the row is in Mongo.
	<-prov.startEntered

	// Find the trayId via the repo (it was Saved before StartDeploy).
	list, err := th.trayRepo.List(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	trayId := list[0].Id

	// Fire the unregister HTTP request from the agent.
	unregBody, _ := json.Marshal(messages.UnregisterRequest{
		Reason: messages.UnregisterReasonDone,
	})
	req := httptest.NewRequest("POST", "/agent/unregister/"+trayId, bytes.NewReader(unregBody))
	w := httptest.NewRecorder()
	th.mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "unregister must succeed even mid-deploy")

	// Release StartDeploy. CreateTray completes its post-deploy bookkeeping.
	close(prov.startRelease)
	require.NoError(t, <-createDone, "CreateTray must not error after concurrent unregister")

	// Final state: row gone (cleaned up by the unregister path).
	final, err := th.trayRepo.GetById(context.Background(), trayId)
	require.NoError(t, err)
	assert.Nil(t, final, "row must be cleaned up after unregister")

	// CleanTray ran exactly once — the unregister path. CreateTray's
	// post-WaitDeploy SetProviderData saw the row was missing and skipped
	// its own cleanup attempt. No double-clean.
	prov.mu.Lock()
	defer prov.mu.Unlock()
	assert.Equal(t, []string{trayId}, prov.cleaned, "exactly one CleanTray call expected")
}
