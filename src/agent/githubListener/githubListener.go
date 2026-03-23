package githubListener

import (
	"cattery/lib/messages"
	"os"
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

type ShutdownEvent struct {
	Reason  messages.UnregisterReason
	Message string
}

type GithubListener struct {
	listenerPath string
	process      *os.Process

	mut sync.Mutex
}

func NewGithubListener(listenerPath string) *GithubListener {
	return &GithubListener{
		listenerPath: listenerPath,
	}
}

func (l *GithubListener) Start(jitConfig *string, shutdownCh chan<- ShutdownEvent) {
	var commandRun = exec.Command(l.listenerPath, "run", "--jitconfig", *jitConfig)
	commandRun.Stdout = os.Stdout
	commandRun.Stderr = os.Stderr

	go func() {
		var msg = "Listener finished"
		var reason = messages.UnregisterReasonDone

		err := commandRun.Start()
		if err != nil {
			msg = "Listener failed to start: " + err.Error()
			log.Error(msg)
			shutdownCh <- ShutdownEvent{Reason: messages.UnregisterReasonUnknown, Message: msg}
			return
		}

		l.mut.Lock()
		l.process = commandRun.Process
		l.mut.Unlock()

		err = commandRun.Wait()
		if err != nil {
			msg = "Runner failed: " + err.Error()
			log.Error(msg)
		}

		shutdownCh <- ShutdownEvent{Reason: reason, Message: msg}
	}()
}

func (l *GithubListener) Stop() {
	l.mut.Lock()
	defer l.mut.Unlock()

	if l.process == nil {
		return
	}

	err := l.kill()
	if err != nil {
		log.Error("Failed to kill process: ", err)
	}

	l.process = nil
}

func (l *GithubListener) kill() error {
	return kill(l)
}
