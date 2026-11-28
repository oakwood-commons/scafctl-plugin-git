//go:build !windows

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPreparedGit_DetachedChildKilledOnCancel(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh command not available")
	}

	coordDir := t.TempDir()
	pidFile := filepath.Join(coordDir, "child.pid")
	childReadyFile := filepath.Join(coordDir, "child.ready")

	// Background child ignores SIGTERM and redirects stdio to /dev/null so the foreground
	// shell exits immediately on SIGTERM without keeping stdout/stderr pipes open.
	script := fmt.Sprintf(
		"(trap '' TERM; exec >/dev/null 2>&1 </dev/null; touch %q; exec sleep 30) & printf '%%s\\n' \"$!\" > %q && sleep 30\n",
		childReadyFile,
		pidFile,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var childPID int
	doneCoord := make(chan struct{})
	go func() {
		defer close(doneCoord)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, readyErr := os.Stat(childReadyFile); readyErr != nil {
					continue
				}
				rawPID, readErr := os.ReadFile(pidFile) //nolint:gosec // G304: test temp path
				if readErr != nil || !strings.Contains(string(rawPID), "\n") {
					continue
				}
				parsedPID, parseErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
				if parseErr != nil || parsedPID <= 0 {
					continue
				}
				childPID = parsedPID
				cancel()
				return
			}
		}
	}()

	cmd := exec.CommandContext(ctx, "sh")
	cmd.Stdin = strings.NewReader(script)
	_, _, runErr := runPreparedGit(cmd, coordDir, nil)
	<-doneCoord

	t.Cleanup(func() {
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})

	require.Error(t, runErr)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Greater(t, childPID, 0, "expected subprocess script to record detached child PID")

	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH)
	}, 2*time.Second, 10*time.Millisecond, "detached SIGTERM-ignoring descendant must be terminated on cancellation")
}
