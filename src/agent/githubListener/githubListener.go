package githubListener

import (
	"context"
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

// Start launches the GitHub runner listener in a background goroutine.
// When the process exits, it cancels ctx with the resulting error (nil on success).
func (l *GithubListener) Start(ctx context.Context, cancel context.CancelCauseFunc, jitConfig *string) {
	var commandRun = exec.Command(l.listenerPath, "run", "--jitconfig", *jitConfig)
	commandRun.Stdout = os.Stdout
	commandRun.Stderr = os.Stderr

	go func() {
		err := commandRun.Start()
		if err != nil {
			log.Errorf("Listener failed to start: %v", err)
			cancel(err)
			return
		}

		l.mut.Lock()
		l.process = commandRun.Process
		l.mut.Unlock()

		err = commandRun.Wait()
		cancel(err) // nil means clean exit
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
