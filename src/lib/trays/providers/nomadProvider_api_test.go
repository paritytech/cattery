package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNomad is a minimal HTTP fixture for the Nomad endpoints the provider
// uses. Each handler is overridable per-test; the defaults 404 so that an
// unexpected call shows up as a test failure rather than silently passing.
type fakeNomad struct {
	t *testing.T

	onDispatch   func(jobID string, req *api.JobDispatchRequest, q url.Values) (*api.JobDispatchResponse, int)
	onEvalInfo   func(evalID string, q url.Values) (*api.Evaluation, int)
	onJobsList   func(q url.Values) ([]*api.JobListStub, int)
	onDeregister func(jobID string, q url.Values) int

	dispatchCount int
	deregCalls    []string
}

func (f *fakeNomad) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs":
		if f.onJobsList == nil {
			http.Error(w, "no onJobsList handler", http.StatusNotImplemented)
			return
		}
		stubs, code := f.onJobsList(q)
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(stubs)

	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/job/") && strings.HasSuffix(r.URL.Path, "/dispatch"):
		jobID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/job/"), "/dispatch")
		var req api.JobDispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if f.onDispatch == nil {
			http.Error(w, "no onDispatch handler", http.StatusNotImplemented)
			return
		}
		f.dispatchCount++
		resp, code := f.onDispatch(jobID, &req, q)
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/job/"):
		jobID := strings.TrimPrefix(r.URL.Path, "/v1/job/")
		f.deregCalls = append(f.deregCalls, jobID)
		if f.onDeregister == nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		code := f.onDeregister(jobID, q)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{}`))

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/evaluation/"):
		// Path is /v1/evaluation/{id}/allocations or /v1/evaluation/{id}
		rest := strings.TrimPrefix(r.URL.Path, "/v1/evaluation/")
		if strings.Contains(rest, "/") {
			http.Error(w, "unsupported eval subpath: "+r.URL.Path, http.StatusNotImplemented)
			return
		}
		if f.onEvalInfo == nil {
			http.Error(w, "no onEvalInfo handler", http.StatusNotImplemented)
			return
		}
		eval, code := f.onEvalInfo(rest, q)
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(eval)

	default:
		http.Error(w, "unexpected request: "+r.Method+" "+r.URL.String(), http.StatusNotImplemented)
	}
}

// startFake spins up the fake Nomad and a NomadProvider pointing at it. The
// provider's namespace defaults to "ns-test"; pass "" to leave it empty.
func startFake(t *testing.T, namespace string) (*fakeNomad, *NomadProvider) {
	t.Helper()
	fake := &fakeNomad{t: t}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	pc := config.ProviderConfig{
		"name":    "test-nomad",
		"type":    "nomad",
		"address": srv.URL,
	}
	if namespace != "" {
		pc["namespace"] = namespace
	}
	p := NewNomadProvider("test-nomad", pc)
	require.NotNil(t, p, "provider construction must succeed")
	return fake, p
}

// setupTrayConfig installs a CatteryConfig with the given NomadTrayConfig
// and an advertise URL the provider will plumb into the dispatched meta.
func setupTrayConfig(t *testing.T, trayTypeName string, nc config.NomadTrayConfig, advertiseURL string) {
	t.Helper()
	cfg := &config.CatteryConfig{
		Server: config.ServerConfig{
			ListenAddress: ":0",
			AdvertiseUrl:  advertiseURL,
		},
		TrayTypes: []*config.TrayType{
			{
				Name:          trayTypeName,
				Provider:      "test-nomad",
				GitHubOrg:     "org",
				RunnerGroupId: 1,
				Config:        nc,
			},
		},
	}
	config.SetForTest(t, cfg)
}

func newTestTray(trayTypeName, id string) *trays.Tray {
	return &trays.Tray{
		Id:           id,
		TrayTypeName: trayTypeName,
		ProviderData: map[string]string{},
	}
}

// ---------------------------------------------------------------------------
// StartDeploy
// ---------------------------------------------------------------------------

func TestStartDeploy_Success(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{
		JobId:        "cattery-runner-tray",
		RunnerFolder: "/cattery",
		Script:       "echo hi",
	}, "https://cattery.test")

	fake, p := startFake(t, "ci")

	var seenReq *api.JobDispatchRequest
	var seenJobID string
	var seenNS string
	fake.onDispatch = func(jobID string, req *api.JobDispatchRequest, q url.Values) (*api.JobDispatchResponse, int) {
		seenJobID = jobID
		seenReq = req
		seenNS = q.Get("namespace")
		return &api.JobDispatchResponse{
			DispatchedJobID: "cattery-runner-tray/dispatch-trayid-001-abcd",
			EvalID:          "eval-1",
		}, http.StatusOK
	}

	tray := newTestTray("tt", "trayid-001")
	err := p.StartDeploy(context.Background(), tray)
	require.NoError(t, err)

	// Response is plumbed onto ProviderData.
	assert.Equal(t, "cattery-runner-tray/dispatch-trayid-001-abcd", tray.ProviderData[nomadProviderDataDispatchedJobID])
	assert.Equal(t, "eval-1", tray.ProviderData[nomadProviderDataEvalID])
	// Pre-dispatch staging is durable on the tray.
	assert.Equal(t, "ci", tray.ProviderData[nomadProviderDataNamespace])
	assert.Equal(t, "cattery-runner-tray", tray.ProviderData[nomadProviderDataParentJobID])

	// Request shape.
	assert.Equal(t, "cattery-runner-tray", seenJobID)
	assert.Equal(t, "ci", seenNS, "namespace should be passed as a query param")
	require.NotNil(t, seenReq)
	assert.Equal(t, "trayid-001", seenReq.IdPrefixTemplate, "idPrefixTemplate must equal tray.Id for prefix-scan recovery")

	// Meta is the bootstrap contract.
	assert.Equal(t, "trayid-001", seenReq.Meta["tray_name"])
	assert.NotEmpty(t, seenReq.Meta["bootstrap_token"])
	assert.Equal(t, "https://cattery.test", seenReq.Meta["cattery_url"])

	// Payload spliced our user script in and emitted the cattery exec.
	payload := string(seenReq.Payload)
	assert.Contains(t, payload, "echo hi")
	assert.Contains(t, payload, `--runner-folder "/cattery"`)
	assert.Contains(t, payload, `curl -fsSL "$CATTERY_URL/agent/download"`)
}

func TestStartDeploy_MissingJobId(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{}, "https://cattery.test")
	fake, p := startFake(t, "ci")

	tray := newTestTray("tt", "trayid-002")
	err := p.StartDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing jobId")
	// No HTTP call should have happened.
	assert.Equal(t, 0, fake.dispatchCount)
	// And no staged keys, since we bailed before staging.
	assert.Empty(t, tray.ProviderData[nomadProviderDataParentJobID])
}

func TestStartDeploy_DispatchError_StillStagesRecoveryKeys(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{
		JobId: "cattery-runner-tray",
	}, "https://cattery.test")
	fake, p := startFake(t, "ci")

	fake.onDispatch = func(jobID string, req *api.JobDispatchRequest, q url.Values) (*api.JobDispatchResponse, int) {
		// Simulate a server-side failure. The provider's error path must
		// still leave parentJobId + namespace staged on ProviderData so
		// CleanTray can recover.
		return &api.JobDispatchResponse{}, http.StatusInternalServerError
	}

	tray := newTestTray("tt", "trayid-003")
	err := p.StartDeploy(context.Background(), tray)
	require.Error(t, err)

	assert.Equal(t, "ci", tray.ProviderData[nomadProviderDataNamespace],
		"namespace must be staged before dispatch so CleanTray can recover a leaked child")
	assert.Equal(t, "cattery-runner-tray", tray.ProviderData[nomadProviderDataParentJobID],
		"parentJobId must be staged before dispatch")
	assert.Empty(t, tray.ProviderData[nomadProviderDataDispatchedJobID],
		"dispatchedJobId must NOT be set when dispatch failed")
}

func TestStartDeploy_WrongTrayConfigType(t *testing.T) {
	// Install a Docker config under the same tray type name. The provider
	// must reject it instead of dispatching garbage.
	cfg := &config.CatteryConfig{
		Server: config.ServerConfig{ListenAddress: ":0", AdvertiseUrl: "http://x"},
		TrayTypes: []*config.TrayType{
			{
				Name:          "tt",
				Provider:      "test-nomad",
				GitHubOrg:     "org",
				RunnerGroupId: 1,
				Config:        config.DockerTrayConfig{Image: "wrong"},
			},
		},
	}
	config.SetForTest(t, cfg)
	fake, p := startFake(t, "ci")

	tray := newTestTray("tt", "trayid-004")
	err := p.StartDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected tray config type")
	assert.Equal(t, 0, fake.dispatchCount)
}

func TestStartDeploy_ExtraMetadataCannotClobberContractKeys(t *testing.T) {
	cfg := &config.CatteryConfig{
		Server: config.ServerConfig{ListenAddress: ":0", AdvertiseUrl: "https://cattery.test"},
		TrayTypes: []*config.TrayType{
			{
				Name:          "tt",
				Provider:      "test-nomad",
				GitHubOrg:     "org",
				RunnerGroupId: 1,
				Config:        config.NomadTrayConfig{JobId: "cattery-runner-tray"},
				ExtraMetadata: config.TrayExtraMetadata{
					"tray_name":       "ATTACKER",
					"bootstrap_token": "ATTACKER",
					"cattery_url":     "https://evil.example",
					"foo":             "bar",
				},
			},
		},
	}
	config.SetForTest(t, cfg)
	fake, p := startFake(t, "ci")

	var seenMeta map[string]string
	fake.onDispatch = func(jobID string, req *api.JobDispatchRequest, q url.Values) (*api.JobDispatchResponse, int) {
		seenMeta = req.Meta
		return &api.JobDispatchResponse{DispatchedJobID: "x", EvalID: "y"}, http.StatusOK
	}

	tray := newTestTray("tt", "trayid-005")
	err := p.StartDeploy(context.Background(), tray)
	require.NoError(t, err)

	assert.Equal(t, "trayid-005", seenMeta["tray_name"], "provider must overwrite operator-supplied tray_name")
	assert.NotEqual(t, "ATTACKER", seenMeta["bootstrap_token"], "bootstrap_token must be the freshly generated one")
	assert.Equal(t, "https://cattery.test", seenMeta["cattery_url"], "cattery_url must be the server's advertised URL")
	assert.Equal(t, "bar", seenMeta["foo"], "non-contract extraMetadata keys must pass through")
}

// ---------------------------------------------------------------------------
// WaitDeploy
// ---------------------------------------------------------------------------

func TestWaitDeploy_Complete(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	fake.onEvalInfo = func(evalID string, q url.Values) (*api.Evaluation, int) {
		assert.Equal(t, "eval-1", evalID)
		return &api.Evaluation{ID: "eval-1", Status: "complete"}, http.StatusOK
	}

	tray := newTestTray("tt", "t")
	tray.ProviderData[nomadProviderDataEvalID] = "eval-1"
	assert.NoError(t, p.WaitDeploy(context.Background(), tray))
}

func TestWaitDeploy_Blocked_ReturnsCapacitySentinel(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	fake.onEvalInfo = func(evalID string, q url.Values) (*api.Evaluation, int) {
		return &api.Evaluation{
			ID:     "eval-1",
			Status: "blocked",
			FailedTGAllocs: map[string]*api.AllocationMetric{
				"vm": {NodesEvaluated: 0, NodesExhausted: 0, ConstraintFiltered: map[string]int{"meta.runner_host=cattery": 3}},
			},
		}, http.StatusOK
	}

	tray := newTestTray("tt", "t")
	tray.ProviderData[nomadProviderDataEvalID] = "eval-1"
	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCapacityBlocked), "blocked eval must produce ErrCapacityBlocked")
	assert.Contains(t, err.Error(), "constraintFiltered=1", "wrapped reason must contain the formatted metric")
}

func TestWaitDeploy_Failed(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	fake.onEvalInfo = func(evalID string, q url.Values) (*api.Evaluation, int) {
		return &api.Evaluation{ID: "eval-1", Status: "failed", StatusDescription: "bad"}, http.StatusOK
	}

	tray := newTestTray("tt", "t")
	tray.ProviderData[nomadProviderDataEvalID] = "eval-1"
	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrCapacityBlocked), "failed != blocked")
	assert.Contains(t, err.Error(), "failed")
}

func TestWaitDeploy_NoEvalIDIsNoOp(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	// No handler installed. If the provider hits the API, the default
	// 501 handler will surface the bug as an error.
	tray := newTestTray("tt", "t") // no eval id staged
	assert.NoError(t, p.WaitDeploy(context.Background(), tray))
	assert.Equal(t, 0, fake.dispatchCount)
}

// ---------------------------------------------------------------------------
// CleanTray
// ---------------------------------------------------------------------------

func TestCleanTray_FastPath_DeregistersDispatchedJob(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	tray := newTestTray("tt", "t")
	tray.ProviderData[nomadProviderDataDispatchedJobID] = "j/dispatch-t-001"
	tray.ProviderData[nomadProviderDataNamespace] = "ci"

	require.NoError(t, p.CleanTray(context.Background(), tray))
	require.Len(t, fake.deregCalls, 1)
	assert.Equal(t, "j/dispatch-t-001", fake.deregCalls[0])
}

func TestCleanTray_FastPath_404IsSwallowed(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	fake.onDeregister = func(jobID string, q url.Values) int {
		// Nomad signals "already gone" via 404; provider must not surface it.
		return http.StatusNotFound
	}

	tray := newTestTray("tt", "t")
	tray.ProviderData[nomadProviderDataDispatchedJobID] = "j/dispatch-t-001"

	assert.NoError(t, p.CleanTray(context.Background(), tray))
}

func TestCleanTray_LeakedChildScan_FindsAndDeregistersByPrefix(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	fake.onJobsList = func(q url.Values) ([]*api.JobListStub, int) {
		// Provider asks for the parent's dispatch prefix.
		assert.Equal(t, "parent-job/dispatch-", q.Get("prefix"))
		return []*api.JobListStub{
			// Match: correct parent + correct tray prefix.
			{ID: "parent-job/dispatch-tray-leak-001-1700000000-aaaa", ParentID: "parent-job"},
			// Same parent, different tray — must be ignored.
			{ID: "parent-job/dispatch-some-other-tray-1700000000-bbbb", ParentID: "parent-job"},
			// Right-shaped ID but different ParentID — ignored.
			{ID: "parent-job/dispatch-tray-leak-001-x", ParentID: "different-parent"},
		}, http.StatusOK
	}

	tray := newTestTray("tt", "tray-leak-001")
	// dispatchedJobId intentionally absent — this is the leaked-child case.
	tray.ProviderData[nomadProviderDataParentJobID] = "parent-job"
	tray.ProviderData[nomadProviderDataNamespace] = "ci"

	require.NoError(t, p.CleanTray(context.Background(), tray))
	require.Len(t, fake.deregCalls, 1, "exactly the matching leaked child must be deregistered")
	assert.Equal(t, "parent-job/dispatch-tray-leak-001-1700000000-aaaa", fake.deregCalls[0])
}

func TestCleanTray_LeakedChildScan_NoMatchIsNoOp(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	fake.onJobsList = func(q url.Values) ([]*api.JobListStub, int) {
		return []*api.JobListStub{}, http.StatusOK
	}

	tray := newTestTray("tt", "tray-leak-002")
	tray.ProviderData[nomadProviderDataParentJobID] = "parent-job"
	tray.ProviderData[nomadProviderDataNamespace] = "ci"

	require.NoError(t, p.CleanTray(context.Background(), tray))
	assert.Empty(t, fake.deregCalls)
}

func TestCleanTray_NoIdsRecorded_IsNoOp(t *testing.T) {
	setupTrayConfig(t, "tt", config.NomadTrayConfig{JobId: "j"}, "http://x")
	fake, p := startFake(t, "ci")

	tray := newTestTray("tt", "t") // empty ProviderData

	require.NoError(t, p.CleanTray(context.Background(), tray))
	assert.Empty(t, fake.deregCalls)
}
