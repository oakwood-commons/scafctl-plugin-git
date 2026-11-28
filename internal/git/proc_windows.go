//go:build windows

package git

import (
	"os/exec"
	"time"
)

const subprocessPipeWaitDelay = 500 * time.Millisecond

// configureSubprocessCancel bounds stdout/stderr pipe draining after context
// cancellation or process exit on Windows.
func configureSubprocessCancel(cmd *exec.Cmd) func() {
	cmd.WaitDelay = subprocessPipeWaitDelay
	return func() {}
}
