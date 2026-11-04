// Package schedule runs agents and the merge ensurer on their cron schedules (vteam cron start).
package schedule

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leodnlv/vteam/internal/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/cron"
	"github.com/leodnlv/vteam/internal/ensuremerge"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/runlog"
	"github.com/leodnlv/vteam/internal/runstate"
)

type Job struct {
	Name    string
	Cron    string
	Command []string
}

// FindJobs returns every agent with a cron schedule, plus the merge ensurer when it has one. Agents that fail to load are skipped.
func FindJobs(cwd string, cfg *config.Config) []Job {
	var jobs []Job
	for _, name := range config.ListAgentNames(cwd) {
		agent, err := config.ResolveAgent(name, cwd)
		if err == nil && agent.Cron != "" {
			jobs = append(jobs, Job{Name: name, Cron: agent.Cron, Command: []string{"run", name}})
		}
	}
	if cfg.MergeEnsurer.Cron != "" {
		jobs = append(jobs, Job{Name: ensuremerge.Name, Cron: cfg.MergeEnsurer.Cron, Command: []string{"ensure-merge"}})
	}
	return jobs
}

func PIDPath(cwd string) string {
	return filepath.Join(runstate.LocksDir(cwd), "cron.pid")
}

// ReadPID returns the pid of the running scheduler, or 0 when none is running.
func ReadPID(cwd string) int {
	data, err := os.ReadFile(PIDPath(cwd))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if !lock.IsProcessAlive(pid) {
		return 0
	}
	return pid
}

func NextRun(expr string, now time.Time) time.Time {
	s, err := cron.Parse(expr)
	if err != nil {
		return time.Time{}
	}
	return s.Next(now)
}

func padEnd(s string, n int) string { return log.PadEnd(s, n) }

// RenderScheduler renders the Scheduler section shared by vteam status, vteam cron status, and vteam cron start.
func RenderScheduler(cwd string, cfg *config.Config, now time.Time) []string {
	lines := []string{log.FormatSection("Scheduler")}
	jobs := FindJobs(cwd, cfg)
	if len(jobs) == 0 {
		return append(lines, log.FormatListItem(ansi.Gray("·"), ansi.Dim("no agents have a cron schedule")))
	}
	if pid := ReadPID(cwd); pid != 0 {
		lines = append(lines, log.FormatListItem(ansi.Green("✓"), "running "+ansi.Gray(fmt.Sprintf("(pid %d)", pid))))
	} else {
		lines = append(lines, log.FormatListItem(ansi.Yellow("!"), ansi.Yellow("not running (start it with 'vteam cron start')")))
	}
	nameWidth, cronWidth := 0, 0
	for _, j := range jobs {
		nameWidth = max(nameWidth, len(j.Name))
		cronWidth = max(cronWidth, len(j.Cron))
	}
	for _, j := range jobs {
		when := ansi.Dim("no next run")
		if next := NextRun(j.Cron, now); !next.IsZero() {
			when = "next " + log.FormatTimeOfDay(next, now) + " " + ansi.Gray("(in "+log.FormatAge(next.Sub(now))+")")
		}
		lines = append(lines, log.FormatListItem(ansi.Blue("•"),
			ansi.Bold(padEnd(j.Name, nameWidth))+"  "+ansi.Gray(padEnd(j.Cron, cronWidth))+"  "+when))
	}
	return lines
}

type Scheduler struct {
	Cwd    string
	Config *config.Config
	Log    *log.Logger
	Now    func() time.Time
	// Sleep waits for d or until ctx is done.
	Sleep func(ctx context.Context, d time.Duration) error
	// Command builds the child process for a job; it runs with VTEAM_RUN_LOG set to logPath.
	Command func(job Job, logPath string) (*exec.Cmd, error)
}

func New(cwd string, cfg *config.Config, logger *log.Logger) *Scheduler {
	return &Scheduler{Cwd: cwd, Config: cfg, Log: logger, Now: time.Now, Sleep: sleep, Command: selfCommand}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func selfCommand(job Job, _ string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(self, job.Command...), nil
}

type runningJobs struct {
	mu   sync.Mutex
	cmds map[string]*exec.Cmd
}

func (r *runningJobs) start(name string, cmd *exec.Cmd) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.cmds[name]; busy {
		return false
	}
	r.cmds[name] = cmd
	return true
}

func (r *runningJobs) done(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cmds, name)
}

func (r *runningJobs) snapshot() map[string]*exec.Cmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]*exec.Cmd{}
	for k, v := range r.cmds {
		out[k] = v
	}
	return out
}

// Run schedules every job until ctx is canceled, then kills running children and removes the pid file.
func (s *Scheduler) Run(ctx context.Context) error {
	s.Log.UseWallClock()
	s.Log.Banner("cron start", fmt.Sprintf("pid %d", os.Getpid()))
	jobs := FindJobs(s.Cwd, s.Config)
	if len(jobs) == 0 {
		s.Log.Println("")
		s.Log.Warn("No agents with cron patterns found.")
		return nil
	}
	if pid := ReadPID(s.Cwd); pid != 0 {
		s.Log.Println("")
		s.Log.Fail(fmt.Sprintf("vteam cron is already running (pid %d).", pid))
		return log.ErrReported
	}
	if err := os.MkdirAll(filepath.Dir(PIDPath(s.Cwd)), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(PIDPath(s.Cwd), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	defer os.Remove(PIDPath(s.Cwd))

	s.Log.Println(strings.Join(RenderScheduler(s.Cwd, s.Config, s.Now()), "\n"))
	s.Log.Section("Activity")
	s.Log.Step("waiting for jobs " + ansi.Gray("(Ctrl+C to stop)"))

	running := &runningJobs{cmds: map[string]*exec.Cmd{}}
	var children, tickers sync.WaitGroup
	for _, job := range jobs {
		schedule, err := cron.Parse(job.Cron)
		if err != nil {
			continue
		}
		tickers.Go(func() {
			for {
				now := s.Now()
				next := schedule.Next(now)
				if next.IsZero() || s.Sleep(ctx, next.Sub(now)) != nil {
					return
				}
				s.tick(job, running, &children)
			}
		})
	}
	tickers.Wait()

	s.Log.Println("")
	s.Log.Step("stopping scheduler")
	for name, cmd := range running.snapshot() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		s.Log.Warn("killed " + name)
	}
	children.Wait()
	s.Log.OK("scheduler stopped")
	return nil
}

func (s *Scheduler) tick(job Job, running *runningJobs, children *sync.WaitGroup) {
	logPath := runlog.Path(s.Cwd, job.Name, s.Now())
	cmd, err := s.Command(job, logPath)
	if err != nil {
		s.Log.Fail(fmt.Sprintf("%s could not start: %s", job.Name, err))
		return
	}
	if !running.start(job.Name, cmd) {
		s.Log.Warn(job.Name + " skipped (still running)")
		return
	}
	cmd.Dir = s.Cwd
	cmd.Env = append(os.Environ(), runlog.Env+"="+logPath)
	shownPath, _ := filepath.Rel(s.Cwd, logPath)
	started := s.Now()
	if err := cmd.Start(); err != nil {
		running.done(job.Name)
		s.Log.Fail(fmt.Sprintf("%s could not start: %s", job.Name, err))
		return
	}
	s.Log.Step(ansi.Bold(job.Name) + " started")
	s.Log.Field("log", shownPath)
	children.Go(func() {
		err := cmd.Wait()
		running.done(job.Name)
		elapsed := log.FormatDuration(s.Now().Sub(started))
		if err == nil {
			s.Log.OK(fmt.Sprintf("%s finished in %s", ansi.Bold(job.Name), elapsed))
			return
		}
		how := fmt.Sprintf("exited %d", cmd.ProcessState.ExitCode())
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			how = "killed by " + unixSignalName(status.Signal())
		}
		s.Log.Fail(fmt.Sprintf("%s %s after %s", job.Name, how, elapsed))
		s.Log.Field("log", shownPath)
	})
}

func unixSignalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGINT:
		return "SIGINT"
	}
	return sig.String()
}
