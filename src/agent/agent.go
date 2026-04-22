package agent

import (
	"cattery/agent/catteryClient"
	"cattery/agent/githubListener"
	"cattery/agent/tools"
	"cattery/lib/agents"
	"cattery/lib/messages"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var RunnerFolder string
var CatteryServerUrl string
var Id string

// shutdownCause is returned by watchers to report why shutdown was requested.
// Satisfies error so it can propagate through errgroup.
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

	registerCtx, cancelRegister := context.WithTimeout(context.Background(), 30*time.Second)
	agent, jitConfig, err := a.catteryClient.RegisterAgent(registerCtx, a.agentId)
	cancelRegister()
	if err != nil {
		a.logger.Errorf("Failed to register agent: %v", err)
		return
	}
	a.agent = agent

	a.logger.Info("Agent registered, starting Listener")

	// File watcher setup is synchronous so startup fails fast and doesn't
	// leak a goroutine or half-initialized fsnotify state.
	watcher, err := a.setupFileWatcher()
	if err != nil {
		a.logger.Errorf("Failed to start file watcher: %v", err)
		a.unregisterAndShutdown(messages.UnregisterReasonDone, "file watcher setup: "+err.Error())
		return
	}
	defer watcher.Close()

	g, ctx := errgroup.WithContext(context.Background())

	g.Go(func() error { return a.watchSignal(ctx) })
	g.Go(func() error { return a.watchFile(ctx, watcher) })
	g.Go(func() error { return a.watchPing(ctx) })
	g.Go(func() error {
		listener := githubListener.NewGithubListener(a.listenerExecPath)
		err := listener.Run(ctx, jitConfig)
		// Listener exit (clean or otherwise) must cancel the group. Translate
		// into a shutdownCause so Wait() returns an error and errgroup signals
		// the other watchers.
		if err == nil {
			return &shutdownCause{reason: messages.UnregisterReasonDone, message: "Listener finished"}
		}
		return &shutdownCause{reason: messages.UnregisterReasonDone, message: "Listener exited: " + err.Error()}
	})

	reason, msg := a.resolveShutdownCause(g.Wait())
	a.logger.Infof("Shutdown: reason=%d, message=%s", reason, msg)

	a.unregisterAndShutdown(reason, msg)
}

// resolveShutdownCause unwraps the first error returned by the errgroup.
// Watchers wrap their shutdown reasons in *shutdownCause; anything else is
// treated as a listener-finished signal.
func (a *CatteryAgent) resolveShutdownCause(err error) (messages.UnregisterReason, string) {
	var sc *shutdownCause
	if errors.As(err, &sc) {
		return sc.reason, sc.message
	}
	if err == nil {
		return messages.UnregisterReasonDone, "Listener finished"
	}
	return messages.UnregisterReasonDone, err.Error()
}

func (a *CatteryAgent) unregisterAndShutdown(reason messages.UnregisterReason, msg string) {
	log.Infof("Stopping Cattery Agent with reason: %d, message: `%s`", reason, msg)

	unregisterCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.catteryClient.UnregisterAgent(unregisterCtx, a.agent, reason, msg); err != nil {
		a.logger.Errorf("Failed to unregister agent: %v", err)
	}

	if a.agent.Shutdown {
		a.logger.Debugf("Shutdown now")
		tools.Shutdown()
	}
}

func (a *CatteryAgent) watchSignal(ctx context.Context) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case sig := <-sigs:
		a.logger.Info("Got signal ", sig)
		return &shutdownCause{
			reason:  messages.UnregisterReasonSigTerm,
			message: "Got signal " + sig.String(),
		}
	}
}

func (a *CatteryAgent) setupFileWatcher() (*fsnotify.Watcher, error) {
	const shutdownFile = "./shutdown_file"

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create file watcher: %w", err)
	}

	f, err := os.OpenFile(shutdownFile, os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("create shutdown file: %w", err)
	}
	f.Close()

	if err := watcher.Add(shutdownFile); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("watch shutdown file: %w", err)
	}

	return watcher, nil
}

func (a *CatteryAgent) watchFile(ctx context.Context, watcher *fsnotify.Watcher) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case event := <-watcher.Events:
		msg := "Shutdown file changed: " + event.Name
		a.logger.Info(msg)
		return &shutdownCause{reason: messages.UnregisterReasonPreempted, message: msg}
	case watchErr := <-watcher.Errors:
		msg := "File watcher error: " + watchErr.Error()
		a.logger.Error(msg)
		return &shutdownCause{reason: messages.UnregisterReasonPreempted, message: msg}
	}
}

func (a *CatteryAgent) watchPing(ctx context.Context) error {
	const pingInterval = 60 * time.Second
	const pingTimeout = 15 * time.Second

	for {
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		pingResponse, err := a.catteryClient.Ping(pingCtx)
		cancel()

		// If ctx was cancelled while Ping was in flight, exit without logging
		// a spurious transport error.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			a.logger.Errorf("Error pinging controller: %v", err)
		} else if pingResponse.Terminate {
			msg := "Controller requested termination: " + pingResponse.Message
			a.logger.Info(msg)
			return &shutdownCause{
				reason:  messages.UnregisterReasonControllerKill,
				message: msg,
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pingInterval):
		}
	}
}
