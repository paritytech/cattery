package handlers

import (
	"cattery/lib/config"
	"cattery/server/trays/providers"
	"github.com/google/go-github/v70/github"
	"log"
	"net/http"
)

func Webhook(w http.ResponseWriter, r *http.Request) {

	var webhookData *github.WorkflowJobEvent

	payload, err := github.ValidatePayload(r, []byte(""))
	if err != nil {
		log.Println(err)
		return
	}

	hook, err := github.ParseWebHook(r.Header.Get("X-GitHub-Event"), payload)
	if err != nil {
		log.Println(err)
		return
	}

	webhookData = hook.(*github.WorkflowJobEvent)
	log.Println(webhookData)

	// Spawn a new agent

	var trayType config.TrayType

	// find
	for _, label := range webhookData.WorkflowJob.Labels {
		if val, ok := config.AppConfig.TrayTypes[label]; ok {
			trayType = val
		}
	}

	var provider = providers.GetProvider(trayType.Provider)

	provider.CreateTray(trayType.TrayConfig)

}
