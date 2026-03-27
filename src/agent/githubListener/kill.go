//go:build !linux

package githubListener

import (
	"fmt"
	"os"
)

func kill(l *GithubListener) error {
	err := l.process.Signal(os.Kill)
	if err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}

	return nil
}
