package handlers

import (
	"cattery/lib/githubClient"
	"cattery/lib/messages"
	"cattery/server/trays"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"net/http"
)

// AgentRegister is a handler for agent registration requests
func AgentRegister(responseWriter http.ResponseWriter, r *http.Request) {
	log.Tracef("AgentRegister: %v", r)

	if r.Method != http.MethodGet {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var hostname = r.PathValue("hostname")
	var agentId = getAgentId(hostname)

	var logger = log.WithFields(log.Fields{
		"action":   "AgentRegister",
		"hostname": hostname,
		"agentId":  agentId,
	})

	logger.Debugln("Agent registration request, ", hostname)

	client := githubClient.NewGithubClient("paritytech-stg")
	config, err := client.CreateJITConfig(
		fmt.Sprint("Test local runner ", agentId),
		3,
		[]string{"cattery-tiny", "cattery"},
	)

	if err != nil {
		logger.Errorln(err)
		http.Error(responseWriter, "Failed to generate JIT config", http.StatusInternalServerError)
		return
	}

	var jitConfig = config.GetEncodedJITConfig()

	var newTray = trays.Tray{
		AgentId:  agentId,
		Hostname: hostname,
		RunnerId: config.GetRunner().GetID(),
	}

	var registerResponse = messages.RegisterResponse{
		Tray:      newTray,
		JitConfig: jitConfig,
	}

	err = json.NewEncoder(responseWriter).Encode(registerResponse)
	if err != nil {
		logger.Errorln(err)
		http.Error(responseWriter, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	logger.Infof("Agent %s, %s registered with runner ID %d", hostname, agentId, newTray.RunnerId)
}

// getAgentId returns the agent ID for the given hostname
func getAgentId(hostname string) string {
	return hostname
}

// AgentUnregister is a handler for agent unregister requests
func AgentUnregister(responseWriter http.ResponseWriter, r *http.Request) {
	log.Tracef("AgentUnregister: %v", r)

	if r.Method != http.MethodPost {
		http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var hostname = r.PathValue("hostname")

	var unregisterRequest messages.UnregisterRequest
	err := json.NewDecoder(r.Body).Decode(&unregisterRequest)
	if err != nil {
		log.Warnf("Failed to decode unregister request for hostname '%s': %v", r.PathValue("hostname"), err)
	}

	var logger = log.WithFields(log.Fields{
		"action":   "AgentRegister",
		"hostname": hostname,
		"agentId":  unregisterRequest.Tray.AgentId,
	})

	logger.Debugf("Agent unregister request")

	client := githubClient.NewGithubClient("paritytech-stg")
	err = client.RemoveRunner(unregisterRequest.Tray.RunnerId)
	if err != nil {
		logger.Errorf("Failed to remove runner %d: %v", unregisterRequest.Tray.RunnerId, err)
	}

	log.Println("Agent ", r.PathValue("hostname"), "registered")
}
