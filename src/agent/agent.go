package agent

import (
	"cattery/agent/tools"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"sync"
	"syscall"

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

	stopped          bool
	listenerExecPath string
}

func NewCatteryAgent(runnerFolder string, catteryServerUrl string, agentId string) *CatteryAgent {
	return &CatteryAgent{
		mutex:            sync.Mutex{},
		logger:           logrus.WithField("name", "agent"),
		catteryClient:    createClient(catteryServerUrl),
		listenerExecPath: path.Join(runnerFolder, "bin", "Runner.Listener"),
		agentId:          agentId,
	}
}

func (a *CatteryAgent) Start() {

	agent, jitConfig, err := a.catteryClient.RegisterAgent(a.agentId)
	if err != nil {
		errMsg := "Failed to register agent: " + err.Error()
		a.logger.Error(errMsg)
		return
	}
	a.agent = agent

	var commandRun = exec.Command(a.listenerExecPath, "run", "--jitconfig", *jitConfig)
	commandRun.Stdout = os.Stdout
	commandRun.Stderr = os.Stderr

	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		signal.Notify(sigs, syscall.SIGTERM)
		signal.Notify(sigs, syscall.SIGKILL)

		sig := <-sigs
		a.logger.Info("Got signal ", sig)

		a.stop(commandRun.Process, true)
	}()

	err = commandRun.Run()
	if err != nil {
		var errMsg = "Runner failed: " + err.Error()
		a.logger.Error(errMsg)
	}

	a.stop(commandRun.Process, false)
}

// stop stops the runner process
func (a *CatteryAgent) stop(runnerProcess *os.Process, isInterrupted bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.stopped {
		return
	}

	if isInterrupted {
		a.logger.Info("Stopping runner")
		err := runnerProcess.Signal(syscall.SIGINT)

		if err != nil {
			var errMsg = "Failed to stop runner: " + err.Error()
			a.logger.Error(errMsg)
		}
	}

	a.logger.Info("Runner stopped")

	a.stopped = true

	var reason messages.UnregisterReason

	if isInterrupted {
		reason = messages.UnregisterReasonPreempted
	} else {
		reason = messages.UnregisterReasonDone
	}

	err := a.catteryClient.UnregisterAgent(a.agent, reason)
	if err != nil {
		var errMsg = "Failed to unregister agent: " + err.Error()
		a.logger.Error(errMsg)
	}

	if a.agent.Shutdown {
		tools.Shutdown()
	}
}

// createClient creates a new http client
func createClient(baseUrl string) *CatteryClient {
	return NewCatteryClient(baseUrl)
}
