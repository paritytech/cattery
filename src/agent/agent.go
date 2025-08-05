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

	interrupted      bool
	listenerExecPath string
}

func NewCatteryAgent(runnerFolder string, catteryServerUrl string, agentId string) *CatteryAgent {
	return &CatteryAgent{
		mutex:            sync.Mutex{},
		logger:           logrus.WithField("name", "agent"),
		catteryClient:    createClient(catteryServerUrl),
		listenerExecPath: path.Join(runnerFolder, "bin", "Runner.Listener"),
		agentId:          agentId,
		interrupted:      false,
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

		a.interrupt(commandRun.Process)
	}()

	err = commandRun.Run()
	if err != nil {
		var errMsg = "Runner failed: " + err.Error()
		a.logger.Error(errMsg)
	}

	a.stop()
}

func (a *CatteryAgent) interrupt(runnerProcess *os.Process) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.interrupted {
		return
	}

	if isInterrupted {
	a.logger.Info("Interrupting runner")
	err := runnerProcess.Signal(syscall.SIGINT)
	if err != nil {
		var errMsg = "Failed to stop runner: " + err.Error()
		a.logger.Error(errMsg)
	}

	a.interrupted = true
}

// stop stops the runner process
func (a *CatteryAgent) stop() {

	a.logger.Info("Runner stopped")

	var reason messages.UnregisterReason

	if a.interrupted {
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
