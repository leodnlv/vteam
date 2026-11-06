package runstate

import (
	"fmt"
	"syscall"
)

// StopSignal stops a run, which then releases its task and removes its worktree.
// StopKeepWorktreeSignal stops it the same way but leaves the worktree for inspection.
const (
	StopSignal             = syscall.SIGTERM
	StopKeepWorktreeSignal = syscall.SIGUSR1
)

// Stop signals the live run of name to stop and returns its pid. The run stops on its own shortly after.
func Stop(cwd, name string, keepWorktree bool) (int, error) {
	for _, run := range FindLive(cwd) {
		if run.Name != name {
			continue
		}
		sig := StopSignal
		if keepWorktree {
			sig = StopKeepWorktreeSignal
		}
		if err := syscall.Kill(run.Lock.PID, sig); err != nil {
			return 0, fmt.Errorf("could not stop %s (pid %d): %w", name, run.Lock.PID, err)
		}
		return run.Lock.PID, nil
	}
	return 0, fmt.Errorf("%s is not running", name)
}
