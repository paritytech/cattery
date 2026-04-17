package githubListener

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	log "github.com/sirupsen/logrus"
)

// gracePeriod is how long the runner is given to exit after an interrupt
// before we escalate to SIGKILL.
const gracePeriod = 10 * time.Second

type GithubListener struct {
	listenerPath string
}

func NewGithubListener(listenerPath string) *GithubListener {
	return &GithubListener{listenerPath: listenerPath}
}

// Run starts the GitHub runner listener and blocks until either the process
// exits or ctx is cancelled. On cancellation the process is interrupted and,
// if it doesn't exit within gracePeriod, forcefully killed with SIGKILL.
func (l *GithubListener) Run(ctx context.Context, jitConfig *string) error {
	cmd := exec.Command(l.listenerPath, "run", "--jitconfig", *jitConfig)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start listener: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownProcess(cmd.Process, done)
		return ctx.Err()
	}
}

// shutdownProcess asks the process to exit, escalating to SIGKILL if the
// grace period elapses. Blocks until cmd.Wait() observes the exit.
func shutdownProcess(p *os.Process, done <-chan error) {
	if err := interrupt(p); err != nil {
		log.Errorf("Failed to interrupt listener: %v", err)
	}

	select {
	case <-done:
		return
	case <-time.After(gracePeriod):
		log.Warnf("Listener did not exit within %s, sending SIGKILL", gracePeriod)
		if err := p.Kill(); err != nil {
			log.Errorf("Failed to kill listener: %v", err)
		}
		<-done
	}
}
