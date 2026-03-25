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

	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentRegister",
	})

	logger.Tracef("AgentRegister: %v", r)

	var id = r.PathValue("id")
	var agentId = validateAgentId(id)

	logger = logger.WithFields(log.Fields{
		"agentId": agentId,
	})

	logger.Debug("Agent registration request")

	var tray, err = h.TrayManager.Registering(agentId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to update tray status for agent '%s': %v", agentId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	var trayType = config.AppConfig.GetTrayType(tray.GetTrayTypeName())
	if trayType == nil {
		var errMsg = fmt.Sprintf("Tray type '%s' not found", tray.GetTrayTypeName())
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}
	logger = logger.WithFields(log.Fields{"trayType": trayType.Name})

	logger.Debugf("Found tray %s for agent %s, with organization %s", tray.GetId(), agentId, tray.GetGitHubOrgName())

	poller := h.ScaleSetManager.GetPoller(trayType.Name)
	if poller == nil {
		var errMsg = fmt.Sprintf("No scale set poller found for tray type '%s'", trayType.Name)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	jitRunnerConfig, err := poller.Client().GenerateJitRunnerConfig(r.Context(), tray.GetId())
	if err != nil {
		logger.Errorf("Failed to generate jitRunnerConfig: %v", err)
		http.Error(responseWriter, "Failed to generate jitRunnerConfig", http.StatusInternalServerError)
		return
	}

	var jitConfig = jitRunnerConfig.EncodedJITConfig

	var newAgent = agents.Agent{
		AgentId:  agentId,
		RunnerId: int64(jitRunnerConfig.Runner.ID),
		Shutdown: trayType.Shutdown,
	}

	var registerResponse = messages.RegisterResponse{
		Agent:     newAgent,
		JitConfig: jitConfig,
	}

	err = json.NewEncoder(responseWriter).Encode(registerResponse)
	if err != nil {
		logger.Errorf("Failed to encode response: %v", err)
		http.Error(responseWriter, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	_, err = h.TrayManager.Registered(agentId, int64(jitRunnerConfig.Runner.ID))
	if err != nil {
		logger.Errorf("%v", err)
	}

	metrics.RegisteredTraysAdd(tray.GitHubOrgName, tray.TrayTypeName, 1)

	logger.Infof("Agent %s registered with runner ID %d", agentId, newAgent.RunnerId)
}

// validateAgentId validates the agent ID
func validateAgentId(agentId string) string {
	return agentId
}

// AgentUnregister is a handler for agent unregister requests
func (h *Handlers) AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentUnregister",
	})

	logger.Tracef("AgentUnregister: %v", r)

	var trayId = r.PathValue("id")

	var tray, err = h.TrayManager.GetTrayById(trayId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to get tray for agent '%s': %v", trayId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}
	if tray == nil {
		http.Error(responseWriter, "Tray does not exist", http.StatusNotFound)
		return
	}

	var unregisterRequest messages.UnregisterRequest
	err = json.NewDecoder(r.Body).Decode(&unregisterRequest)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to decode unregister request for trayId '%s': %v", trayId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}

	logger = logger.WithFields(log.Fields{
		"trayId": tray.Id,
	})

	logger.Tracef("Agent unregister request")

	_, err = h.TrayManager.DeleteTray(tray.Id)

	if err != nil {
		logger.Errorf("Failed to delete tray: %v", err)
	}

	logger.Infof("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)

	metrics.RegisteredTraysAdd(tray.GitHubOrgName, tray.TrayTypeName, -1)
	if unregisterRequest.Reason == messages.UnregisterReasonPreempted {
		metrics.PreemptedTraysInc(tray.GitHubOrgName, tray.TrayTypeName)
	}

}

func AgentDownloadBinary(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentDownloadBinary",
	})
	logger.Tracef("AgentDownloadBinary: %v", r)

	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		logger.Errorf("Failed to get executable path: %v", err)
		http.Error(responseWriter, "Failed to get binary path", http.StatusInternalServerError)
		return
	}

	// Open the binary file
	file, err := os.Open(execPath)
	if err != nil {
		logger.Errorf("Failed to open binary file: %v", err)
		http.Error(responseWriter, "Failed to open binary file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Get file info for size and name
	fileInfo, err := file.Stat()
	if err != nil {
		logger.Errorf("Failed to get file info: %v", err)
		http.Error(responseWriter, "Failed to get file info", http.StatusInternalServerError)
		return
	}

	// Set appropriate headers
	responseWriter.Header().Set("Content-Type", "application/octet-stream")
	responseWriter.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(execPath)))
	responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Serve the file
	http.ServeContent(responseWriter, r, filepath.Base(execPath), fileInfo.ModTime(), file)

	logger.Infof("Binary file served: %s (%d bytes)", execPath, fileInfo.Size())
}

func (h *Handlers) AgentPing(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentPing",
	})

	logger.Tracef("AgentPing: %v", r)

	var id = r.PathValue("id")
	var agentId = validateAgentId(id)

	var pingResponse = &messages.PingResponse{
		Terminate: false,
		Message:   "",
	}

	tray, err := h.TrayManager.GetTrayById(agentId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to get tray by id '%s': %v", agentId, err)
		logger.Error(errMsg)

		pingResponse.Message = errMsg
		pingResponse.Terminate = true
		writeResponse(responseWriter, pingResponse, logger)

		return
	}
	if tray == nil {
		var errMsg = fmt.Sprintf("Tray with id '%s' not found", agentId)
		logger.Error(errMsg)

		pingResponse.Message = errMsg
		pingResponse.Terminate = true
		writeResponse(responseWriter, pingResponse, logger)

		return
	}

	if tray.Status == trays.TrayStatusRunning {
		writeResponse(responseWriter, pingResponse, logger)
		return
	}

	if time.Now().UTC().Sub(tray.StatusChanged) > time.Minute*2 {
		var errMsg = fmt.Sprintf("Tray '%s' status not changed in 2 minutes", tray.Id)
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
	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentRestart",
	})

	logger.Tracef("AgentRestart: %v", r)

	var id = r.PathValue("id")
	var agentId = validateAgentId(id)

	logger = logger.WithFields(log.Fields{
		"agentId": agentId,
	})

	logger.Debug("Agent restart request with id " + agentId)

	tray, err := h.TrayManager.GetTrayById(agentId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to get tray by id '%s': %v", agentId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}
	if tray == nil {
		var errMsg = fmt.Sprintf("Tray with id '%s' not found", agentId)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusGone)
		return
	}
	workflowRunId := tray.WorkflowRunId
	h.RestartManager.RequestRestart(workflowRunId, tray.GitHubOrgName, tray.Repository)
}
