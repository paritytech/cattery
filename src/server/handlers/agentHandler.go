package handlers

import (
	"cattery/lib/agents"
	"cattery/lib/githubClient"
	"cattery/lib/messages"
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

	var hostname = r.PathValue("hostname")
	var agentId = getAgentId(hostname)

	logger = log.WithFields(log.Fields{
		"hostname": hostname,
		"agentId":  agentId,
	})

	logger.Debugln("Agent registration request, ", hostname)

	var tray, ok = traysStore[hostname]

	if !ok {
		var err = errors.New(fmt.Sprintf("tray '%s' not found", hostname))
		logger.Errorf(err.Error())
		http.Error(responseWriter, err.Error(), http.StatusNotFound)
		return
	}

	client := githubClient.NewGithubClient("paritytech-stg")
	config, err := client.CreateJITConfig(
		tray.Name,
		3,
		tray.Labels,
	)

	if err != nil {
		logger.Errorln(err)
		http.Error(responseWriter, "Failed to generate JIT config", http.StatusInternalServerError)
		return
	}

	var jitConfig = config.GetEncodedJITConfig()

	var newAgent = agents.Agent{
		AgentId:  agentId,
		Hostname: hostname,
		RunnerId: config.GetRunner().GetID(),
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

	logger.Infof("Agent %s, %s registered with runner ID %d", hostname, agentId, newAgent.RunnerId)
}

// getAgentId returns the agent ID for the given hostname
func getAgentId(hostname string) string {
	return hostname
}

// AgentUnregister is a handler for agent unregister requests
func AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	var logger = log.WithField("action", "AgentUnregister")

	logger.Tracef("AgentUnregister: %v", r)

	if r.Method != http.MethodPost {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var hostname = r.PathValue("hostname")

	var unregisterRequest messages.UnregisterRequest
	err := json.NewDecoder(r.Body).Decode(&unregisterRequest)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to decode unregister request for hostname '%s': %v", hostname, err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusBadRequest)
	}

	logger = logger.WithFields(log.Fields{
		"action":   "AgentRegister",
		"hostname": hostname,
		"agentId":  unregisterRequest.Agent.AgentId,
	})

	logger.Debugf("Agent unregister request")

	client := githubClient.NewGithubClient("paritytech-stg")
	err = client.RemoveRunner(unregisterRequest.Agent.RunnerId)
	if err != nil {
		var errMsg = fmt.Sprintf("Failed to remove runner %s: %v", unregisterRequest.Agent.AgentId, err)
		logger.Errorf(errMsg)
		http.Error(responseWriter, errMsg, http.StatusInternalServerError)
	}

	logger.Infof("Agent %s, %s unregistered", hostname, unregisterRequest.Agent.AgentId)

	// TODO remove tray
}
