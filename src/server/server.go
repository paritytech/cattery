package server

import (
	"cattery/lib/config"
	"cattery/lib/messages"
	"cattery/server/trays/providers"
	"context"
	"encoding/json"
	"fmt"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v70/github"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

var githubClient *github.Client = nil

func createClient() *github.Client {

	if githubClient != nil {
		return githubClient
	}

	tr := http.DefaultTransport

	itr, err := ghinstallation.NewKeyFromFile(
		tr,
		config.AppConfig.AppID,
		config.AppConfig.InstallationId,
		config.AppConfig.PrivateKeyPath,
	)

	if err != nil {
		log.Fatal(err)
	}

	// Use installation transport with github.com/google/go-github
	client := github.NewClient(&http.Client{Transport: itr})

	githubClient = client
	return client
}

func Start() {

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	signal.Notify(sigs, syscall.SIGTERM)
	signal.Notify(sigs, syscall.SIGKILL)

	var webhookMux = http.NewServeMux()
	webhookMux.HandleFunc("/github", func(w http.ResponseWriter, r *http.Request) {

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

	})

	webhookMux.HandleFunc("/agent/register/{hostname}", func(responseWriter http.ResponseWriter, r *http.Request) {
		log.Println("Agent")

		if r.Method != http.MethodGet {
			http.Error(responseWriter, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		log.Println("Agent registration request, ", r.PathValue("hostname"))

		var clientId = rand.Int()

		client := createClient()
		config, _, err := client.Actions.GenerateOrgJITConfig(
			context.Background(),
			"paritytech-stg",
			&github.GenerateJITConfigRequest{
				Name:          fmt.Sprint("Test local runner ", clientId),
				RunnerGroupID: 3,
				Labels:        []string{"cattery-tiny", "cattery"},
			},
		)
		if err != nil {
			log.Println(err)
		}

		var jitConfig = config.GetEncodedJITConfig()

		var registerResponse = messages.RegisterResponse{
			AgentID:   fmt.Sprint(clientId),
			JitConfig: jitConfig,
		}

		json.NewEncoder(responseWriter).Encode(registerResponse)

		log.Println("Agent ", r.PathValue("hostname"), "registered")
	})

	var webhookServer = &http.Server{
		Addr:    config.AppConfig.ListenAddress,
		Handler: webhookMux,
	}

	go func() {
		log.Println("Starting webhook server on ", config.AppConfig.ListenAddress)
		err := webhookServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
			return
		}
	}()

	sig := <-sigs
	fmt.Println(sig)
}
