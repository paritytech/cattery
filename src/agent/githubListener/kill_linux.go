package githubListener

import (
	"errors"
	"os/exec"
)

func (l *GithubListener) kill() error {
	var commandInterruptRun = exec.Command("pkill", "--signal", "SIGINT", "Runner.Listener")
	err := commandInterruptRun.Run()
	if err != nil {
		var errMsg = "Failed to interrupt runner: " + err.Error()
		return errors.New(errMsg)
	}

	return nil

	// TODO: debug why SIGINT does not work correctly
	// err := runnerProcess.Signal(syscall.SIGINT)
	// if err != nil {
	// 	var errMsg = "Failed to interrupt runner: " + err.Error()
	// 	a.logger.Error(errMsg)
	// }
}
