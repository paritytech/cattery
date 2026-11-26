package server

import (
	"cattery/lib/agentBinaries"
	"cattery/lib/config"
	"cattery/server/handlers"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentMux_DownloadEndpointFlag(t *testing.T) {
	h := &handlers.Handlers{
		AgentBinaries: agentBinaries.NewStore(config.AgentBinariesConfig{Dir: t.TempDir()}, "1.2.3"),
	}
	// An unknown arch makes the handler answer 400, which tells it apart from
	// the mux's 404 for a route that is not registered.
	get := func() int {
		w := httptest.NewRecorder()
		agentMux(h).ServeHTTP(w, httptest.NewRequest("GET", "/agent/download?os=linux&arch=bogus", nil))
		return w.Code
	}

	t.Run("served by default", func(t *testing.T) {
		config.SetForTest(t, &config.CatteryConfig{})
		assert.Equal(t, http.StatusBadRequest, get())
	})

	t.Run("not served when disabled", func(t *testing.T) {
		enabled := false
		config.SetForTest(t, &config.CatteryConfig{
			Server: config.ServerConfig{AgentBinaries: config.AgentBinariesConfig{Enabled: &enabled}},
		})
		assert.Equal(t, http.StatusNotFound, get())
	})
}

// A download must not be cut off by the server's write timeout. It needs a
// real server: a ResponseRecorder has no deadlines, and the handler ignores
// the error when its ResponseWriter does not support them.
func TestAgentDownload_OutlivesWriteTimeout(t *testing.T) {
	config.SetForTest(t, &config.CatteryConfig{})

	// A platform other than the host's, so the binary comes from the folder.
	goarch := "arm64"
	if runtime.GOARCH == "arm64" {
		goarch = "amd64"
	}
	// Bigger than the socket buffers, so the server is still writing while
	// the client below is not reading.
	const size = 64 << 20
	dir := t.TempDir()
	binary := filepath.Join(dir, agentBinaries.AssetName("linux", goarch, "1.2.3"))
	require.NoError(t, os.WriteFile(binary, nil, 0o644))
	require.NoError(t, os.Truncate(binary, size))

	h := &handlers.Handlers{
		AgentBinaries: agentBinaries.NewStore(config.AgentBinariesConfig{Dir: dir}, "1.2.3"),
	}
	srv := httptest.NewUnstartedServer(agentMux(h))
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/agent/download?os=linux&arch=" + goarch)
	require.NoError(t, err)
	defer resp.Body.Close()

	time.Sleep(4 * srv.Config.WriteTimeout)
	n, err := io.Copy(io.Discard, resp.Body)

	require.NoError(t, err)
	assert.EqualValues(t, size, n)
}
