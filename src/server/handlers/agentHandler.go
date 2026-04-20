package handlers

import (
	"cattery/lib/agents"
	"cattery/lib/config"
	"cattery/lib/messages"
	"cattery/lib/metrics"
	"cattery/lib/trays"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
)

// AgentRegister is a handler for agent registration requests
func (h *Handlers) AgentRegister(responseWriter http.ResponseWriter, r *http.Request) {

	logger := log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentRegister",
	})

	logger.Tracef("AgentRegister: %v", r)

	_, code, errMsg := h.authenticateAgent(r)
	if code != 0 {
		logger.Warn(errMsg)
		http.Error(responseWriter, errMsg, code)
		return
	}

	agentId := r.PathValue("id")

	logger = logger.WithFields(log.Fields{
		"agentId": agentId,
	})

	logger.Debug("Agent registration request")

	tray, err := h.TrayManager.Registering(r.Context(), agentId)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to update tray status for agent '%s': %v", agentId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	trayType := config.Get().GetTrayType(tray.TrayTypeName)
	if trayType == nil {
		errMsg := fmt.Sprintf("Tray type '%s' not found", tray.TrayTypeName)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}
	logger = logger.WithFields(log.Fields{"trayType": trayType.Name})

	logger.Debugf("Found tray %s for agent %s, with organization %s", tray.Id, agentId, tray.GitHubOrgName)

	poller := h.ScaleSetManager.GetPoller(trayType.Name)
	if poller == nil {
		errMsg := fmt.Sprintf("No scale set poller found for tray type '%s'", trayType.Name)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	jitRunnerConfig, err := poller.Client().GenerateJitRunnerConfig(r.Context(), tray.Id)
	if err != nil {
		logger.Errorf("Failed to generate jitRunnerConfig: %v", err)
		http.Error(responseWriter, "Failed to generate jitRunnerConfig", http.StatusInternalServerError)
		return
	}

	jitConfig := jitRunnerConfig.EncodedJITConfig

	newAgent := agents.Agent{
		AgentId:  agentId,
		RunnerId: int64(jitRunnerConfig.Runner.ID),
		Shutdown: trayType.Shutdown,
	}

	registerResponse := messages.RegisterResponse{
		Agent:     newAgent,
		JitConfig: jitConfig,
	}

	responseWriter.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(responseWriter).Encode(registerResponse); err != nil {
		// Response headers already sent; can only log, not send http.Error
		logger.Errorf("Failed to encode response: %v", err)
		return
	}

	_, err = h.TrayManager.Registered(r.Context(), agentId, int64(jitRunnerConfig.Runner.ID))
	if err != nil {
		logger.Errorf("%v", err)
	}

	logger.Infof("Agent %s registered with runner ID %d", agentId, newAgent.RunnerId)
}

// authenticateAgent checks the optional Bearer token and verifies the tray exists.
// Returns the tray on success, or (nil, statusCode, errorMessage) on failure.
func (h *Handlers) authenticateAgent(r *http.Request) (*trays.Tray, int, string) {
	secret := config.Get().Server.AgentSecret
	if secret != "" {
		header := r.Header.Get("Authorization")
		if header == "" {
			return nil, http.StatusUnauthorized, "missing Authorization header"
		}

		const prefix = "Bearer "
		if len(header) < len(prefix) || header[:len(prefix)] != prefix {
			return nil, http.StatusUnauthorized, "invalid Authorization header format"
		}

		if header[len(prefix):] != secret {
			return nil, http.StatusUnauthorized, "invalid agent secret"
		}
	}

	agentId := r.PathValue("id")
	if agentId != "" {
		tray, err := h.TrayManager.GetTrayById(r.Context(), agentId)
		if err != nil {
			return nil, http.StatusInternalServerError, "failed to look up agent"
		}
		if tray == nil {
			return nil, http.StatusNotFound, "unknown agent"
		}
		return tray, 0, ""
	}

	return nil, 0, ""
}

// AgentUnregister is a handler for agent unregister requests
func (h *Handlers) AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	logger := log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentUnregister",
	})

	logger.Tracef("AgentUnregister: %v", r)

	tray, code, errMsg := h.authenticateAgent(r)
	if code != 0 {
		logger.Warn(errMsg)
		http.Error(responseWriter, errMsg, code)
		return
	}

	unregisterRequest := messages.UnregisterRequest{}
	if err := json.NewDecoder(r.Body).Decode(&unregisterRequest); err != nil {
		decodeErr := fmt.Sprintf("Failed to decode unregister request for trayId '%s': %v", tray.Id, err)
		logger.Error(decodeErr)
		http.Error(responseWriter, decodeErr, http.StatusBadRequest)
		return
	}

	logger = logger.WithFields(log.Fields{
		"trayId": tray.Id,
	})

	logger.Tracef("Agent unregister request")

	_, deleteErr := h.TrayManager.DeleteTray(r.Context(), tray.Id)

	if deleteErr != nil {
		logger.Errorf("Failed to delete tray: %v", deleteErr)
		http.Error(responseWriter, "Failed to delete tray", http.StatusInternalServerError)
		return
	}

	logger.Infof("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)

	if unregisterRequest.Reason == messages.UnregisterReasonPreempted {
		metrics.PreemptedTraysInc(tray.GitHubOrgName, tray.TrayTypeName)
	}

	responseWriter.WriteHeader(http.StatusOK)
}

func AgentDownloadBinary(responseWriter http.ResponseWriter, r *http.Request) {
	execPath, err := os.Executable()
	if err != nil {
		http.Error(responseWriter, "Failed to get binary path", http.StatusInternalServerError)
		return
	}

	responseWriter.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(execPath)))
	http.ServeFile(responseWriter, r, execPath)
}

func (h *Handlers) AgentPing(responseWriter http.ResponseWriter, r *http.Request) {
	logger := log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentPing",
	})

	logger.Tracef("AgentPing: %v", r)

	tray, code, authErr := h.authenticateAgent(r)
	if code != 0 {
		logger.Warn(authErr)
		http.Error(responseWriter, authErr, code)
		return
	}

	pingResponse := &messages.PingResponse{
		Terminate: false,
		Message:   "",
	}

	if tray.Status == trays.TrayStatusRunning {
		writeResponse(responseWriter, pingResponse, logger)
		return
	}

	if time.Now().UTC().Sub(tray.StatusChanged) > time.Minute*15 {
		errMsg := fmt.Sprintf("Tray '%s' status not changed in 15 minutes", tray.Id)
		logger.Error(errMsg)

		pingResponse.Terminate = true
		pingResponse.Message = errMsg
		writeResponse(responseWriter, pingResponse, logger)
		return
	}

	writeResponse(responseWriter, pingResponse, logger)
}

func writeResponse(responseWriter http.ResponseWriter, pingResponse any, logger *log.Entry) {
	responseWriter.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(responseWriter).Encode(pingResponse); err != nil {
		logger.Errorf("Failed to encode ping response: %v", err)
		http.Error(responseWriter, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func (h *Handlers) AgentInterrupt(responseWriter http.ResponseWriter, r *http.Request) {
	logger := log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentRestart",
	})

	logger.Tracef("AgentRestart: %v", r)

	tray, code, errMsg := h.authenticateAgent(r)
	if code != 0 {
		logger.Warn(errMsg)
		http.Error(responseWriter, errMsg, code)
		return
	}

	logger = logger.WithFields(log.Fields{
		"agentId": tray.Id,
	})

	logger.Debug("Agent restart request with id " + tray.Id)

	workflowRunId := tray.WorkflowRunId
	if err := h.RestartManager.RequestRestart(r.Context(), workflowRunId, tray.GitHubOrgName, tray.Repository); err != nil {
		logger.Errorf("Failed to request restart for workflow %d: %v", workflowRunId, err)
		http.Error(responseWriter, "Failed to request restart", http.StatusInternalServerError)
		return
	}
}
