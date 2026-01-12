package githubListener

import (
	"errors"
	"os"
)

func kill(l *GithubListener) error {
	err := l.process.Signal(os.Kill)
	if err != nil {
		return errors.New("Failed to kill process: " + err.Error())
	}

	return nil
}
