package githubListener

import (
	"fmt"
	"os/exec"
)

func kill(l *GithubListener) error {
	var commandInterruptRun = exec.Command("pkill", "--signal", "SIGINT", "Runner.Listener")
	err := commandInterruptRun.Run()
	if err != nil {
		return fmt.Errorf("failed to interrupt runner: %w", err)
	}

	return nil

	// TODO: debug why SIGINT does not work correctly
	// err := runnerProcess.Signal(syscall.SIGINT)
	// if err != nil {
	// 	var errMsg = "Failed to interrupt runner: " + err.Error()
	// 	a.logger.Error(errMsg)
	// }
}
