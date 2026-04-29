package catteryClient

import (
	"cattery/lib/agents"
	"cattery/lib/messages"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient builds a client pointed at the given test server with a
// retry policy fast enough for unit tests.
func newTestClient(t *testing.T, baseURL string) *CatteryClient {
	t.Helper()
	c := NewCatteryClient(baseURL, "test-agent")
	c.maxAttempts = 3
	c.retryDelay = 1 * time.Millisecond
	return c
}

func TestRegisterAgent_Success(t *testing.T) {
	jit := "encoded-jit-config"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/agent/register/")
		_ = json.NewEncoder(w).Encode(messages.RegisterResponse{
			Agent:     agents.Agent{AgentId: "test-agent", RunnerId: 42},
			JitConfig: jit,
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	agent, jitConfig, err := c.RegisterAgent("test-agent")

	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "test-agent", agent.AgentId)
	assert.Equal(t, int64(42), agent.RunnerId)
	require.NotNil(t, jitConfig)
	assert.Equal(t, jit, *jitConfig)
}

func TestRegisterAgent_RetriesOn404UntilSuccess(t *testing.T) {
	// Simulates the race: server returns 404 until the tray row lands, then 200.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			http.Error(w, "unknown agent", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(messages.RegisterResponse{
			Agent:     agents.Agent{AgentId: "test-agent"},
			JitConfig: "ok",
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	agent, _, err := c.RegisterAgent("test-agent")

	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.EqualValues(t, 3, calls.Load())
}

func TestRegisterAgent_RetriesOn5xxUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			http.Error(w, "transient", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(messages.RegisterResponse{
			Agent: agents.Agent{AgentId: "test-agent"},
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, _, err := c.RegisterAgent("test-agent")

	require.NoError(t, err)
	assert.EqualValues(t, 2, calls.Load())
}

func TestRegisterAgent_FailsFastOn401(t *testing.T) {
	// Authentication errors are permanent: retrying won't help.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, _, err := c.RegisterAgent("test-agent")

	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "must not retry permanent 4xx")
}

func TestRegisterAgent_FailsFastOn400(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, _, err := c.RegisterAgent("test-agent")

	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load())
}

func TestRegisterAgent_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "still missing", http.StatusNotFound)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL) // maxAttempts = 3
	_, _, err := c.RegisterAgent("test-agent")

	require.Error(t, err)
	assert.EqualValues(t, 3, calls.Load())
	assert.Contains(t, err.Error(), "after 3 attempts")
}

func TestRegisterAgent_RetriesOnNetworkError(t *testing.T) {
	// Server that closes the connection without writing a response.
	listener := newRefusingServer(t)

	c := newTestClient(t, "http://"+listener.Addr().String())
	_, _, err := c.RegisterAgent("test-agent")

	require.Error(t, err)
	// The retry exhausted message wraps the last network error.
	assert.Contains(t, err.Error(), "after 3 attempts")
}

func TestRegisterAgent_RetriesOnTruncatedBody(t *testing.T) {
	// The server returns a 200 with Content-Length set but closes the
	// connection before sending the body. The client gets headers OK but
	// the body read fails — that's a transient network condition, not a
	// server bug, and must be retried.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			hj, ok := w.(http.Hijacker)
			require.True(t, ok)
			conn, _, err := hj.Hijack()
			require.NoError(t, err)
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 999\r\n\r\n"))
			_ = conn.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(messages.RegisterResponse{
			Agent: agents.Agent{AgentId: "test-agent"},
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	agent, _, err := c.RegisterAgent("test-agent")

	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.EqualValues(t, 2, calls.Load())
}

func TestRegisterAgent_FailsFastOnMalformedJSON(t *testing.T) {
	// A 200 response with a complete but non-JSON body is a server bug.
	// Retrying won't fix it.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, _, err := c.RegisterAgent("test-agent")

	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "must not retry malformed JSON")
}

func TestUnregisterAgent_Success(t *testing.T) {
	var got messages.UnregisterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	err := c.UnregisterAgent(&agents.Agent{AgentId: "test-agent"}, messages.UnregisterReasonDone, "job complete")

	require.NoError(t, err)
	assert.Equal(t, "test-agent", got.Agent.AgentId)
	assert.Equal(t, messages.UnregisterReasonDone, got.Reason)
	assert.Equal(t, "job complete", got.Message)
}

func TestUnregisterAgent_RetriesOn5xx(t *testing.T) {
	// Body must be re-read on each retry — verify the server sees the JSON
	// payload on the successful attempt.
	var calls atomic.Int32
	var lastBody messages.UnregisterRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&lastBody))
		if calls.Add(1) < 2 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	err := c.UnregisterAgent(&agents.Agent{AgentId: "test-agent"}, messages.UnregisterReasonDone, "msg")

	require.NoError(t, err)
	assert.EqualValues(t, 2, calls.Load())
	assert.Equal(t, "test-agent", lastBody.Agent.AgentId, "request body must reach server on retry")
}

func TestPing_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		_ = json.NewEncoder(w).Encode(messages.PingResponse{Terminate: false})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	resp, err := c.Ping()

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Terminate)
}

func TestPing_RetriesOn5xx(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(messages.PingResponse{Terminate: true, Message: "bye"})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	resp, err := c.Ping()

	require.NoError(t, err)
	assert.True(t, resp.Terminate)
	assert.Equal(t, "bye", resp.Message)
	assert.EqualValues(t, 2, calls.Load())
}

func TestPing_FailsFastOn401(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "no auth", http.StatusUnauthorized)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, err := c.Ping()

	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load())
}

// newRefusingServer starts a listener that accepts connections then closes
// them immediately, producing a network error on the client side.
func newRefusingServer(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return l
}
