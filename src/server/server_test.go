package server

import (
	"cattery/lib/agentBinaries"
	"cattery/lib/config"
	"cattery/server/handlers"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
