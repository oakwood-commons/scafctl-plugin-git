//go:build !windows

package git

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	subprocessKillGrace     = 150 * time.Millisecond
	subprocessKillReapWait  = 100 * time.Millisecond
	subprocessPollInterval  = 10 * time.Millisecond
	subprocessPipeWaitDelay = 500 * time.Millisecond
)

// configureSubprocessCancel places cmd in its own process group, sends SIGTERM on
// context cancellation so Git can run its signal cleanup handlers, escalates to
// process-group SIGKILL if any descendant survives past subprocessKillGrace (even
// when Git itself exits early and closes stdio pipes), and bounds pipe draining.
func configureSubprocessCancel(cmd *exec.Cmd) func() {
	var (
		mu        sync.Mutex
		exited    bool
		canceled  bool
		sigkilled bool
		pgid      int
		cancelAt  time.Time
		timer     *time.Timer
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		mu.Lock()
		defer mu.Unlock()
		if exited || cmd.Process == nil {
			return os.ErrProcessDone
		}
		pgid = cmd.Process.Pid
		canceled = true
		cancelAt = time.Now()
		err := syscall.Kill(-pgid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		pid := pgid
		timer = time.AfterFunc(subprocessKillGrace, func() {
			mu.Lock()
			defer mu.Unlock()
			if !exited {
				sigkilled = true
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
		return err
	}
	cmd.WaitDelay = subprocessPipeWaitDelay

	return func() {
		mu.Lock()
		exited = true
		if timer != nil {
			timer.Stop()
		}
		wasCanceled := canceled
		alreadyKilled := sigkilled
		pid := pgid
		startedAt := cancelAt
		mu.Unlock()

		if !wasCanceled || pid <= 0 || !processGroupAlive(pid) {
			return
		}

		if !alreadyKilled {
			deadline := startedAt.Add(subprocessKillGrace)
			for time.Now().Before(deadline) && processGroupAlive(pid) {
				sleepDur := min(subprocessPollInterval, time.Until(deadline))
				if sleepDur > 0 {
					time.Sleep(sleepDur)
				}
			}
			if processGroupAlive(pid) {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}

		reapDeadline := time.Now().Add(subprocessKillReapWait)
		for time.Now().Before(reapDeadline) && processGroupAlive(pid) {
			time.Sleep(subprocessPollInterval / 2)
		}
	}
}

func processGroupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
