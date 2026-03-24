package agent

import (
	"cattery/agent/catteryClient"
	"cattery/agent/githubListener"
	"cattery/agent/tools"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

var RunnerFolder string
var CatteryServerUrl string
var Id string

func Start() {
	var catteryAgent = NewCatteryAgent(RunnerFolder, CatteryServerUrl, Id)
	catteryAgent.Start()
}

type CatteryAgent struct {
	logger        *log.Entry
	catteryClient *catteryClient.CatteryClient
	agent         *agents.Agent
	agentId       string

	listenerExecPath string
}

func NewCatteryAgent(runnerFolder string, catteryServerUrl string, agentId string) *CatteryAgent {
	return &CatteryAgent{
		logger:           log.WithFields(log.Fields{"name": "agent", "agentId": agentId}),
		catteryClient:    catteryClient.NewCatteryClient(catteryServerUrl, agentId),
		listenerExecPath: path.Join(runnerFolder, "bin", "Runner.Listener"),
		agentId:          agentId,
	}
}

func (a *CatteryAgent) Start() {
	a.logger.Info("Starting Cattery Agent")

	agent, jitConfig, err := a.catteryClient.RegisterAgent(a.agentId)
	if err != nil {
		a.logger.Errorf("Failed to register agent: %v", err)
		return
	}
	a.agent = agent

	a.logger.Info("Agent registered, starting Listener")

	shutdownCh := make(chan githubListener.ShutdownEvent, 1)

	a.watchSignal(shutdownCh)
	a.watchFile(shutdownCh)
	a.watchPing(shutdownCh)

	var ghListener = githubListener.NewGithubListener(a.listenerExecPath)
	ghListener.Start(jitConfig, shutdownCh)

	// Block until first shutdown event
	event := <-shutdownCh

	a.logger.Infof("Received shutdown event: %s, reason: %d", event.Message, event.Reason)

	ghListener.Stop()
	a.stop(event)
}

func (a *CatteryAgent) watchSignal(ch chan<- githubListener.ShutdownEvent) {
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		sig := <-sigs
		a.logger.Info("Got signal ", sig)

		ch <- githubListener.ShutdownEvent{
			Reason:  messages.UnregisterReasonSigTerm,
			Message: "Got signal " + sig.String(),
		}
	}()
}

func (a *CatteryAgent) watchFile(ch chan<- githubListener.ShutdownEvent) {
	const shutdownFile = "./shutdown_file"

	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			a.logger.Fatalf("Failed to create file watcher: %v", err)
		}
		defer watcher.Close()

		// Create the shutdown file if it doesn't exist
		f, err := os.OpenFile(shutdownFile, os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			a.logger.Fatalf("Failed to create shutdown file: %v", err)
		}
		f.Close()

		if err := watcher.Add(shutdownFile); err != nil {
			a.logger.Fatalf("Failed to watch shutdown file: %v", err)
		}

		select {
		case event := <-watcher.Events:
			msg := "Shutdown file changed: " + event.Name
			a.logger.Info(msg)
			ch <- githubListener.ShutdownEvent{
				Reason:  messages.UnregisterReasonPreempted,
				Message: msg,
			}
		case err := <-watcher.Errors:
			msg := "File watcher error: " + err.Error()
			a.logger.Error(msg)
			ch <- githubListener.ShutdownEvent{
				Reason:  messages.UnregisterReasonPreempted,
				Message: msg,
			}
		}
	}()
}

func (a *CatteryAgent) watchPing(ch chan<- githubListener.ShutdownEvent) {
	go func() {
		for {
			pingResponse, err := a.catteryClient.Ping()
			if err != nil {
				a.logger.Errorf("Error pinging controller: %v", err)
				time.Sleep(60 * time.Second)
				continue
			}

			if pingResponse.Terminate {
				msg := "Controller requested termination: " + pingResponse.Message
				a.logger.Info(msg)
				ch <- githubListener.ShutdownEvent{
					Reason:  messages.UnregisterReasonControllerKill,
					Message: msg,
				}
				return
			}

			time.Sleep(60 * time.Second)
		}
	}()
}

// stop stops the runner process
func (a *CatteryAgent) stop(event githubListener.ShutdownEvent) {
	log.Infof("Stopping Cattery Agent with reason: %d, message: `%s`", event.Reason, event.Message)

	err := a.catteryClient.UnregisterAgent(a.agent, event.Reason, event.Message)
	if err != nil {
		a.logger.Errorf("Failed to unregister agent: %v", err)
	}

	if a.agent.Shutdown {
		a.logger.Debugf("Shutdown now")
		tools.Shutdown()
	}
}
