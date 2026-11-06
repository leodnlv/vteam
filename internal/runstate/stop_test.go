package runstate

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/leodnlv/vteam/internal/lock"
)

func TestStop(t *testing.T) {
	for _, tc := range []struct {
		name string
		keep bool
		want syscall.Signal
	}{
		{"sends SIGTERM to remove the worktree", false, syscall.SIGTERM},
		{"sends SIGUSR1 to keep the worktree", true, syscall.SIGUSR1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			cmd := exec.Command("sleep", "30")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			writeLock(t, cwd, "coder", lock.Info{PID: cmd.Process.Pid, Timestamp: time.Now().UnixMilli(), Agent: "coder"})

			pid, err := Stop(cwd, "coder", tc.keep)
			if err != nil || pid != cmd.Process.Pid {
				t.Fatalf("Stop = %d, %v", pid, err)
			}
			_ = cmd.Wait()
			status := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !status.Signaled() || status.Signal() != tc.want {
				t.Errorf("process ended with %v, want %v", status, tc.want)
			}
		})
	}

	t.Run("fails when the run is not live", func(t *testing.T) {
		if _, err := Stop(t.TempDir(), "coder", false); err == nil || err.Error() != "coder is not running" {
			t.Errorf("err = %v", err)
		}
	})
}
