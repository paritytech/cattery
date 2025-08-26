package handlers

import (
	"cattery/lib/agents"
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/messages"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

// AgentRegister is a handler for agent registration requests
func AgentRegister(responseWriter http.ResponseWriter, r *http.Request) {

	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentRegister",
	})

	logger.Tracef("AgentRegister: %v", r)

	if r.Method != http.MethodGet {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var id = r.PathValue("id")
	var agentId = validateAgentId(id)

	logger = logger.WithFields(log.Fields{
		"agentId": agentId,
	})

	logger.Debug("Agent registration request")

	var tray, err = TrayManager.Registering(agentId)
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

	// TODO handle
	client, err := githubClient.NewGithubClientWithOrgName(tray.GetGitHubOrgName())
	if err != nil {
		var errMsg = fmt.Sprintf("Organization '%s' is invalid: %v", tray.GetGitHubOrgName(), err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}
	logger = logger.WithFields(log.Fields{"githubOrg": tray.GetGitHubOrgName()})

	jitRunnerConfig, err := client.CreateJITConfig(
		tray.GetId(),
		trayType.RunnerGroupId,
		[]string{trayType.Name},
	)

	if err != nil {
		logger.Errorf("Failed to generate jitRunnerConfig: %v", err)
		http.Error(responseWriter, "Failed to generate jitRunnerConfig", http.StatusInternalServerError)
		return
	}

	var jitConfig = jitRunnerConfig.GetEncodedJITConfig()

	var newAgent = agents.Agent{
		AgentId:  agentId,
		RunnerId: jitRunnerConfig.GetRunner().GetID(),
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

	_, err = TrayManager.Registered(agentId, jitRunnerConfig.GetRunner().GetID())
	if err != nil {
		logger.Errorf("%v", err)
	}

	logger.Infof("Agent %s registered with runner ID %d", agentId, newAgent.RunnerId)
}

// validateAgentId validates the agent ID
func validateAgentId(agentId string) string {
	return agentId
}

// AgentUnregister is a handler for agent unregister requests
func AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentUnregister",
	})

	logger.Tracef("AgentUnregister: %v", r)

	if r.Method != http.MethodPost {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var trayId = r.PathValue("id")

	var unregisterRequest messages.UnregisterRequest
	err := json.NewDecoder(r.Body).Decode(&unregisterRequest)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to decode unregister request for trayId '%s': %v", trayId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}

	logger = logger.WithFields(log.Fields{
		"trayId": unregisterRequest.Agent.AgentId,
	})

	logger.Tracef("Agent unregister request")

	_, err = TrayManager.DeleteTray(unregisterRequest.Agent.AgentId)

	if err != nil {
		logger.Errorf("Failed to delete tray: %v", err)
	}

	logger.Infof("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)
}

func AgentDownloadBinary(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithFields(log.Fields{
		"handler": "agent",
		"call":    "AgentDownloadBinary",
	})
	logger.Tracef("AgentDownloadBinary: %v", r)

	if r.Method != http.MethodGet {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
