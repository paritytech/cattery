//go:build !linux

package githubListener

import (
	"fmt"
	"os"
)

// interrupt asks the process to exit gracefully. On non-linux platforms we
// don't have a separate graceful signal in use here, so we fall through to
// os.Kill — the caller's grace-period + SIGKILL escalation still applies.
func interrupt(process *os.Process) error {
	if err := process.Signal(os.Interrupt); err != nil {
		return fmt.Errorf("signal process: %w", err)
	}
	return nil
}
