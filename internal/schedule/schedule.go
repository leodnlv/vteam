// Package schedule runs agents and the merge ensurer on their cron schedules (vteam cron start).
package schedule

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leodnlv/vteam/internal/ansi"
	"github.com/leodnlv/vteam/internal/cron"
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

// FindJobs returns the enabled jobs (see Enabled), sorted by name. Jobs whose agent no longer loads are skipped.
func FindJobs(cwd string) []Job {
	enabled, err := Enabled(cwd)
	if err != nil {
		return nil
	}
	var jobs []Job
	for _, name := range slices.Sorted(maps.Keys(enabled)) {
		if checkJobName(cwd, name) == nil {
			jobs = append(jobs, Job{Name: name, Cron: enabled[name], Command: JobCommand(name)})
		}
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
func RenderScheduler(cwd string, now time.Time) []string {
	lines := []string{log.FormatSection("Scheduler")}
	jobs := FindJobs(cwd)
	switch pid := ReadPID(cwd); {
	case pid != 0:
		lines = append(lines, log.FormatListItem(ansi.Green("✓"), "running "+ansi.Gray(fmt.Sprintf("(pid %d)", pid))))
	case len(jobs) > 0:
		lines = append(lines, log.FormatListItem(ansi.Yellow("!"), ansi.Yellow("not running, crons paused (start it with 'vteam cron start')")))
	default:
		lines = append(lines, log.FormatListItem(ansi.Gray("·"), ansi.Dim("not running")))
	}
	if len(jobs) == 0 {
		return append(lines, log.FormatListItem(ansi.Gray("·"), ansi.Dim("no crons enabled (enable one in the vteam TUI or with 'vteam cron enable <agent>')")))
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

// PollInterval is the longest the scheduler sleeps before rereading the enabled jobs.
const PollInterval = 2 * time.Second

type Scheduler struct {
	Cwd  string
	Log  *log.Logger
	Now  func() time.Time
	Poll time.Duration
	// Sleep waits for d or until ctx is done.
	Sleep func(ctx context.Context, d time.Duration) error
	// Command builds the child process for a job; it runs with VTEAM_RUN_LOG set to logPath.
	Command func(job Job, logPath string) (*exec.Cmd, error)
}

func New(cwd string, logger *log.Logger) *Scheduler {
	return &Scheduler{Cwd: cwd, Log: logger, Now: time.Now, Poll: PollInterval, Sleep: sleep, Command: selfCommand}
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

func (r *runningJobs) empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cmds) == 0
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

// Run schedules the enabled jobs until ctx is canceled, then kills running children and removes the pid file.
// It rereads the enabled jobs at least every Poll, and stops on its own once none are enabled and none run.
func (s *Scheduler) Run(ctx context.Context) error {
	s.Log.UseWallClock()
	s.Log.Banner("cron start", fmt.Sprintf("pid %d", os.Getpid()))
	if len(FindJobs(s.Cwd)) == 0 {
		s.Log.Println("")
		s.Log.Warn("No crons enabled. Enable one in the vteam TUI or with 'vteam cron enable <agent>'.")
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
	defer func() { _ = os.Remove(PIDPath(s.Cwd)) }()

	s.Log.Println(strings.Join(RenderScheduler(s.Cwd, s.Now()), "\n"))
	s.Log.Section("Activity")
	s.Log.Step("waiting for jobs " + ansi.Gray("(Ctrl+C to stop)"))

	running := &runningJobs{cmds: map[string]*exec.Cmd{}}
	var children sync.WaitGroup
	timers := map[string]*jobTimer{}
	first := true
	for {
		now := s.Now()
		jobs := FindJobs(s.Cwd)
		s.syncTimers(timers, jobs, now, first)
		first = false
		if len(jobs) == 0 && running.empty() {
			s.Log.Step("no crons enabled, stopping")
			break
		}
		wait := s.Poll
		for _, t := range timers {
			if !now.Before(t.next) {
				s.tick(t.job, running, &children)
				t.next = t.schedule.Next(now)
			}
			if !t.next.IsZero() {
				wait = min(wait, t.next.Sub(now))
			}
		}
		if s.Sleep(ctx, max(0, wait)) != nil {
			break
		}
	}

	if ctx.Err() != nil {
		s.Log.Println("")
		s.Log.Step("stopping scheduler")
		for name, cmd := range running.snapshot() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			s.Log.Warn("killed " + name)
		}
	}
	children.Wait()
	s.Log.OK("scheduler stopped")
	return nil
}

type jobTimer struct {
	job      Job
	schedule *cron.Schedule
	next     time.Time
}

// syncTimers makes timers match jobs: new and changed jobs get their next fire time from now, disabled ones are dropped.
func (s *Scheduler) syncTimers(timers map[string]*jobTimer, jobs []Job, now time.Time, quiet bool) {
	seen := map[string]bool{}
	for _, job := range jobs {
		seen[job.Name] = true
		if t, ok := timers[job.Name]; ok && t.job.Cron == job.Cron {
			continue
		}
		schedule, err := cron.Parse(job.Cron)
		if err != nil {
			continue
		}
		_, changed := timers[job.Name]
		t := &jobTimer{job: job, schedule: schedule, next: schedule.Next(now)}
		timers[job.Name] = t
		if quiet {
			continue
		}
		verb := "cron enabled"
		if changed {
			verb = "cron changed"
		}
		s.Log.Step(ansi.Bold(job.Name) + " " + verb + " " + ansi.Gray(job.Cron+" · next "+log.FormatTimeOfDay(t.next, now)))
	}
	for name := range timers {
		if !seen[name] {
			delete(timers, name)
			s.Log.Step(ansi.Bold(name) + " cron disabled")
		}
	}
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
