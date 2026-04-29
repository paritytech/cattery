package githubListener

import (
	"fmt"
	"os"
	"os/exec"
)

// interrupt asks the runner to exit gracefully. Direct SIGINT to the process
// doesn't behave as expected (TODO: investigate), so we pkill by name.
func interrupt(_ *os.Process) error {
	if err := exec.Command("pkill", "--signal", "SIGINT", "Runner.Listener").Run(); err != nil {
		return fmt.Errorf("pkill Runner.Listener: %w", err)
	}
	return nil
}
