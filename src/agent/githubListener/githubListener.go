package githubListener

import (
	"cattery/agent/shutdownEvents"
	"cattery/lib/messages"
	"os"
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

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

func (l *GithubListener) Start(jitConfig *string) {
	var commandRun = exec.Command(l.listenerPath, "run", "--jitconfig", *jitConfig)
	commandRun.Stdout = os.Stdout
	commandRun.Stderr = os.Stderr

	go func() {
		var msg = "Listener finished"

		err := commandRun.Start()
		if err != nil {
			msg = "Listener failed to start: " + err.Error()
			log.Error(msg)
			shutdownEvents.Emit(messages.UnregisterReasonUnknown, msg)
			return
		}

		l.process = commandRun.Process
		err = commandRun.Wait()
		if err != nil {
			msg = "Runner failed: " + err.Error()
			log.Error(msg)
		}

		//TODO: check startup errors, like deprecated runner

		shutdownEvents.Emit(messages.UnregisterReasonDone, msg)
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
