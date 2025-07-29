package handlers

import (
	"cattery/lib/agents"
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/messages"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"net/http"
)

// AgentRegister is a handler for agent registration requests
func AgentRegister(responseWriter http.ResponseWriter, r *http.Request) {

	logger = log.WithFields(log.Fields{
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

	logger = log.WithFields(log.Fields{
		"agentId": agentId,
	})

	logger.Debugln("Agent registration request")

	var tray, err = TrayManager.Registering(agentId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to update tray status for agent '%s': %v", agentId, err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	var trayType = config.AppConfig.GetTrayType(tray.GetTrayTypeName())

	logger.Debugf("Found tray %s for agent %s, with organization %s", tray.GetId(), agentId, tray.GetGitHubOrgName())

	// TODO handle
	client, err := githubClient.NewGithubClientWithOrgName(tray.GetGitHubOrgName())
	if err != nil {
		var errMsg = fmt.Sprintf("Organization '%s' is invalid: %v", tray.GetGitHubOrgName(), err)
		logger.Error(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
	}

	jitRunnerConfig, err := client.CreateJITConfig(
		tray.GetId(),
		trayType.RunnerGroupId,
		[]string{trayType.Name},
	)

	if err != nil {
		logger.Errorln(err)
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
		logger.Errorln(err)
		http.Error(responseWriter, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	_, err = TrayManager.Registered(agentId, jitRunnerConfig.GetRunner().GetID())
	if err != nil {
		logger.Errorln(err)
	}

	logger.Infof("Agent %s registered with runner ID %d", agentId, newAgent.RunnerId)
}

// validateAgentId validates the agent ID
func validateAgentId(agentId string) string {
	return agentId
}

// AgentUnregister is a handler for agent unregister requests
func AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	logger = log.WithFields(log.Fields{
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
		logger.Errorln("Failed to delete tray:", err)
	}

	logger.Infof("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)
}
