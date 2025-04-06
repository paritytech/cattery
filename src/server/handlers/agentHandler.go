package handlers

import (
	"cattery/lib/agents"
	"cattery/lib/config"
	"cattery/lib/githubClient"
	"cattery/lib/messages"
	"cattery/lib/trays/providers"
	"encoding/json"
	"errors"
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

	var tray, _ = traysStore.Get(agentId)
	if tray == nil {
		var err = errors.New(fmt.Sprintf("tray '%s' not found", agentId))
		logger.Errorf(err.Error())
		http.Error(responseWriter, err.Error(), http.StatusNotFound)
		return
	}

	var org = config.AppConfig.GetGitHubOrg(tray.GitHubOrgName())
	if org != nil {
		var errMsg = fmt.Sprintf("Organization '%s' not found in jitRunnerConfig", tray.GitHubOrgName())
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
		return
	}

	client := githubClient.NewGithubClient(org)
	jitRunnerConfig, err := client.CreateJITConfig(
		tray.Id(),
		tray.RunnerGroupId(),
		tray.Labels(),
	)

	if err != nil {
		logger.Errorln(err)
		http.Error(responseWriter, "Failed to generate JIT jitRunnerConfig", http.StatusInternalServerError)
		return
	}

	var jitConfig = jitRunnerConfig.GetEncodedJITConfig()

	var newAgent = agents.Agent{
		AgentId:  agentId,
		RunnerId: jitRunnerConfig.GetRunner().GetID(),
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

	var tray, _ = traysStore.Get(trayId)
	if tray == nil {
		var errMsg = fmt.Sprintf("tray '%s' not found", trayId)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusNotFound)
		return
	}

	var org = config.AppConfig.GetGitHubOrg(tray.GitHubOrgName())
	if org != nil {
		var errMsg = fmt.Sprintf("Organization '%s' not found in jitRunnerConfig", tray.GitHubOrgName())
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

	//unregisterRequest.Reason
	logger.Debugf("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)

	provider, err := providers.GetProvider(tray.Provider())
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to get provider '%s' for tray %s: %v", tray.Provider(), tray.Id(), err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	err = provider.CleanTray(tray)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to clean tray %s: %v", tray.Id(), err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
		return
	}

	_ = traysStore.Delete(trayId)

	logger.Infof("Agent %s unregistered, reason: %d", unregisterRequest.Agent.AgentId, unregisterRequest.Reason)
}
