package agent

import (
	"cattery/agent/Watchers"
	"cattery/agent/githubListener"
	"cattery/agent/shutdownEvents"
	"cattery/agent/tools"
	"cattery/lib/agents"
	"path"
	"sync"

	"github.com/sirupsen/logrus"
)

var RunnerFolder string
var CatteryServerUrl string
var Id string

func Start() {
	var catteryAgent = NewCatteryAgent(RunnerFolder, CatteryServerUrl, Id)

	catteryAgent.Start()
}

type CatteryAgent struct {
	mutex         sync.Mutex
	logger        *logrus.Entry
	catteryClient *CatteryClient
	agent         *agents.Agent
	agentId       string

	interrupted      bool
	listenerExecPath string
}

func NewCatteryAgent(runnerFolder string, catteryServerUrl string, agentId string) *CatteryAgent {
	return &CatteryAgent{
		mutex:            sync.Mutex{},
		logger:           logrus.WithFields(logrus.Fields{"name": "agent", "agentId": agentId}),
		catteryClient:    createClient(catteryServerUrl),
		listenerExecPath: path.Join(runnerFolder, "bin", "Runner.Listener"),
		agentId:          agentId,
		interrupted:      false,
	}
}

func (a *CatteryAgent) Start() {

	a.logger.Info("Starting Cattery Agent")

	agent, jitConfig, err := a.catteryClient.RegisterAgent(a.agentId)
	if err != nil {
		errMsg := "Failed to register agent: " + err.Error()
		a.logger.Error(errMsg)
		return
	}
	a.agent = agent

	a.logger.Info("Agent registered, starting Listener")

	Watchers.WatchSignal()
	Watchers.WatchFile()

	var ghListener = githubListener.NewGithubListener(a.listenerExecPath)
	ghListener.Start(jitConfig)

	// blocking call
	var event = shutdownEvents.WaitEvent()

	ghListener.Stop()
	a.stop(event)
}

// stop stops the runner process
func (a *CatteryAgent) stop(event shutdownEvents.ShutdownEvent) {

	logrus.Infof("Stopping Cattery Agent with reason: %d, message: `%s`", event.Reason, event.Message)

	err := a.catteryClient.UnregisterAgent(a.agent, event.Reason, event.Message)
	if err != nil {
		var errMsg = "Failed to unregister agent: " + err.Error()
		a.logger.Error(errMsg)
	}

	if a.agent.Shutdown {
		a.logger.Debugf("Shutdown now")
		tools.Shutdown()
	}
}

// createClient creates a new http client
func createClient(baseUrl string) *CatteryClient {
	return NewCatteryClient(baseUrl)
}
