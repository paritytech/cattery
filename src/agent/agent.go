package agent

import (
	"cattery/agent/catteryClient"
	"cattery/agent/githubListener"
	"cattery/agent/tools"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"context"
	"errors"
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

// shutdownCause is used as context.Cause to carry the termination reason.
type shutdownCause struct {
	reason  messages.UnregisterReason
	message string
}

func (s *shutdownCause) Error() string { return s.message }

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

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	a.watchSignal(ctx, cancel)
	a.watchFile(ctx, cancel)
	a.watchPing(ctx, cancel)

	var ghListener = githubListener.NewGithubListener(a.listenerExecPath)
	ghListener.Start(ctx, cancel, jitConfig)

	// Block until any source triggers cancellation
	<-ctx.Done()

	// Determine what happened
	reason, msg := a.resolveShutdownCause(ctx)
	a.logger.Infof("Shutdown: reason=%d, message=%s", reason, msg)

	// Kill listener if it wasn't the one that finished
	if reason != messages.UnregisterReasonDone {
		ghListener.Stop()
	}

	a.unregisterAndShutdown(reason, msg)
}

// resolveShutdownCause extracts the termination reason from the context cause.
// - shutdownCause: a watcher triggered shutdown (signal, file, ping)
// - nil cause: listener exited cleanly
// - other error: listener exited with error
func (a *CatteryAgent) resolveShutdownCause(ctx context.Context) (messages.UnregisterReason, string) {
	cause := context.Cause(ctx)

	var sc *shutdownCause
	if errors.As(cause, &sc) {
		return sc.reason, sc.message
	}

	// Listener finished (cancel was called with nil or a process error)
	if cause == nil {
		return messages.UnregisterReasonDone, "Listener finished"
	}
	return messages.UnregisterReasonDone, "Listener exited: " + cause.Error()
}

func (a *CatteryAgent) unregisterAndShutdown(reason messages.UnregisterReason, msg string) {
	log.Infof("Stopping Cattery Agent with reason: %d, message: `%s`", reason, msg)

	err := a.catteryClient.UnregisterAgent(a.agent, reason, msg)
	if err != nil {
		a.logger.Errorf("Failed to unregister agent: %v", err)
	}

	if a.agent.Shutdown {
		a.logger.Debugf("Shutdown now")
		tools.Shutdown()
	}
}

func (a *CatteryAgent) watchSignal(ctx context.Context, cancel context.CancelCauseFunc) {
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		select {
		case <-ctx.Done():
			return
		case sig := <-sigs:
			a.logger.Info("Got signal ", sig)
			cancel(&shutdownCause{
				reason:  messages.UnregisterReasonSigTerm,
				message: "Got signal " + sig.String(),
			})
		}
	}()
}

func (a *CatteryAgent) watchFile(ctx context.Context, cancel context.CancelCauseFunc) {
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
		case <-ctx.Done():
			return
		case event := <-watcher.Events:
			msg := "Shutdown file changed: " + event.Name
			a.logger.Info(msg)
			cancel(&shutdownCause{
				reason:  messages.UnregisterReasonPreempted,
				message: msg,
			})
		case watchErr := <-watcher.Errors:
			msg := "File watcher error: " + watchErr.Error()
			a.logger.Error(msg)
			cancel(&shutdownCause{
				reason:  messages.UnregisterReasonPreempted,
				message: msg,
			})
		}
	}()
}

func (a *CatteryAgent) watchPing(ctx context.Context, cancel context.CancelCauseFunc) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			pingResponse, err := a.catteryClient.Ping()
			if err != nil {
				a.logger.Errorf("Error pinging controller: %v", err)
				time.Sleep(60 * time.Second)
				continue
			}

			if pingResponse.Terminate {
				msg := "Controller requested termination: " + pingResponse.Message
				a.logger.Info(msg)
				cancel(&shutdownCause{
					reason:  messages.UnregisterReasonControllerKill,
					message: msg,
				})
				return
			}

			time.Sleep(60 * time.Second)
		}
	}()
}
