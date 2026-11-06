package schedule

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/runlog"
)

// StartDetached starts `vteam cron start` in its own session, so it outlives the process that started it.
// Its output goes to LogPath.
func StartDetached(cwd string) (int, error) {
	if pid := ReadPID(cwd); pid != 0 {
		return 0, errors.New("the scheduler is already running")
	}
	if err := ensureStateDir(cwd); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(LogPath(cwd), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = out.Close() }()
	return startDetached(cwd, []string{"cron", "start"}, out, nil)
}

// RunOptions are the `vteam run` flags of a spawned run; the merge ensurer takes none of them.
type RunOptions struct {
	// Focus is passed as --focus when set.
	Focus string
	// Task is passed as --task when set: the ref of the task to claim instead of the next one by priority.
	Task string
	// Interactive passes --interactive.
	Interactive bool
}

// SpawnRun starts one run of a job in its own session and returns the path of the log it will write.
func SpawnRun(cwd, name string, opts RunOptions, now time.Time) (string, error) {
	logPath := runlog.Path(cwd, name, now)
	args := JobCommand(name)
	if opts.Focus != "" {
		args = append(args, "--focus", opts.Focus)
	}
	if opts.Task != "" {
		args = append(args, "--task", opts.Task)
	}
	if opts.Interactive {
		args = append(args, "--interactive")
	}
	_, err := startDetached(cwd, args, nil, []string{runlog.Env + "=" + logPath})
	return logPath, err
}

// SpawnFinalize starts `vteam session finalize <runID>` in its own session, appending to the run's log, so it
// outlives the submit that started it and the tmux window it closes.
func SpawnFinalize(cwd, runID, logPath string) error {
	var env []string
	if logPath != "" {
		env = []string{runlog.Env + "=" + logPath}
	}
	_, err := startDetached(cwd, []string{"session", "finalize", runID}, nil, env)
	return err
}

func startDetached(cwd string, args []string, out *os.File, env []string) (int, error) {
	self, err := execx.Self()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}
