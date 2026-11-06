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

// SpawnRun starts one run of a job in its own session and returns the path of the log it will write.
// A non-empty focus is passed as `--focus` and interactive as `--interactive`, which only `vteam run` accepts.
func SpawnRun(cwd, name, focus string, interactive bool, now time.Time) (string, error) {
	logPath := runlog.Path(cwd, name, now)
	args := JobCommand(name)
	if focus != "" {
		args = append(args, "--focus", focus)
	}
	if interactive {
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
