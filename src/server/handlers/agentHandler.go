package handlers

import (
	"cattery/lib/agents"
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/messages"
	"cattery/lib/trays"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"net/http"
)

// AgentRegister is a handler for agent registration requests
func AgentRegister(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithField("action", "AgentRegister")
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

	var tray, err = QueueManager.TraysStore.UpdateStatus(agentId, trays.TrayStatusIdle, 0)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to update tray status for agent '%s': %v", agentId, err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	var org = config.AppConfig.GetGitHubOrg(tray.GitHubOrgName())
	if org == nil {
		var errMsg = fmt.Sprintf("Organization '%s' not found in config", tray.GitHubOrgName())
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}

	logger.Debugf("Found tray %s for agent %s, with organization %s", tray.Id(), agentId, tray.GitHubOrgName())

	client := githubClient.NewGithubClient(org)
	jitRunnerConfig, err := client.CreateJITConfig(
		tray.Id(),
		tray.RunnerGroupId(),
		[]string{tray.TrayType()},
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
		Shutdown: tray.Shutdown(),
	}

	var registerResponse = messages.RegisterResponse{
		Agent:         newAgent,
		JitConfig:     jitConfig,
		GitHubOrgName: tray.GitHubOrgName(),
	}

	err = json.NewEncoder(responseWriter).Encode(registerResponse)
	if err != nil {
		logger.Errorln(err)
		http.Error(responseWriter, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	logger.Infof("Agent %s registered with runner ID %d", agentId, newAgent.RunnerId)
}

// validateAgentId validates the agent ID
func validateAgentId(agentId string) string {
	return agentId
}

// AgentUnregister is a handler for agent unregister requests
func AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithField("action", "AgentUnregister")

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
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
	}

	logger = logger.WithFields(log.Fields{
		"action": "AgentRegister",
		"trayId": unregisterRequest.Agent.AgentId,
	})

	logger.Tracef("Agent unregister request")

	var org = config.AppConfig.GetGitHubOrg(unregisterRequest.GitHubOrgName)
	if org == nil {
		var errMsg = fmt.Sprintf("Organization '%s' not found in config", unregisterRequest.GitHubOrgName)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}

	client := githubClient.NewGithubClient(org)
	err = client.RemoveRunner(unregisterRequest.Agent.RunnerId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to remove runner %s: %v", unregisterRequest.Agent.AgentId, err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
	}

	logger.Infof("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)
}
