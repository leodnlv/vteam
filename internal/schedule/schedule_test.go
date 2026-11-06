package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/runlog"
)

// TestMain doubles as a stand-in vteam binary for the detached-spawn tests, which re-exec os.Executable.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && (os.Args[1] == "run" || os.Args[1] == "cron") {
		fakeVteam(os.Args[1:])
		return
	}
	os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
}

func fakeVteam(args []string) {
	line := strings.Join(args, " ") + " sid-leader=" + strconv.FormatBool(isSessionLeader())
	if path := os.Getenv(runlog.Env); path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(line), 0o644)
		return
	}
	fmt.Println(line)
}

func isSessionLeader() bool {
	sid, err := syscall.Getsid(0)
	return err == nil && sid == os.Getpid()
}

func waitForFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %q, want it to contain %q", path, data, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSpawnRunStartsADetachedRunWithItsLogPath(t *testing.T) {
	cwd := t.TempDir()
	path, err := SpawnRun(cwd, "improver", "", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if path != runlog.Path(cwd, "improver", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("log path = %s", path)
	}
	waitForFile(t, path, "run improver sid-leader=true")
}

func TestSpawnRunPassesTheFocus(t *testing.T) {
	cwd := t.TempDir()
	path, err := SpawnRun(cwd, "improver", "look at\nthe parser", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	waitForFile(t, path, "run improver --focus look at\nthe parser sid-leader=true")
}

func TestStartDetachedWritesToTheSchedulerLog(t *testing.T) {
	cwd := t.TempDir()
	pid, err := StartDetached(cwd)
	if err != nil || pid <= 0 {
		t.Fatalf("pid = %d, err = %v", pid, err)
	}
	waitForFile(t, LogPath(cwd), "cron start sid-leader=true")

	_ = os.MkdirAll(filepath.Dir(PIDPath(cwd)), 0o755)
	_ = os.WriteFile(PIDPath(cwd), []byte(strconv.Itoa(os.Getpid())), 0o644)
	if _, err := StartDetached(cwd); err == nil {
		t.Error("a second scheduler should not start while one is running")
	}
}

func writeAgent(t *testing.T, cwd, name, agentJSON string) {
	t.Helper()
	dir := filepath.Join(cwd, "vteam", "agents", name)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("prompt"), 0o644)
	if agentJSON != "" {
		_ = os.WriteFile(filepath.Join(dir, "agent.json"), []byte(agentJSON), 0o644)
	}
}

func enable(t *testing.T, cwd, name, expr string) {
	t.Helper()
	if err := Enable(cwd, name, expr); err != nil {
		t.Fatal(err)
	}
}

func TestFindJobs(t *testing.T) {
	t.Run("returns the enabled crons, not the agent.json defaults", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "improver", `{"cron":"0 */6 * * *"}`)
		writeAgent(t, cwd, "coder", `{"cron":"*/5 * * * *"}`)
		if jobs := FindJobs(cwd); len(jobs) != 0 {
			t.Errorf("jobs before enabling = %v", jobs)
		}
		enable(t, cwd, "improver", "0 * * * *")
		enable(t, cwd, "coder", "*/5 * * * *")
		want := []Job{{"coder", "*/5 * * * *", []string{"run", "coder"}}, {"improver", "0 * * * *", []string{"run", "improver"}}}
		if diff := cmp.Diff(want, FindJobs(cwd)); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("skips agents that no longer load", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "good", ``)
		writeAgent(t, cwd, "broken", ``)
		enable(t, cwd, "good", "*/5 * * * *")
		enable(t, cwd, "broken", "*/5 * * * *")
		writeAgent(t, cwd, "broken", `{"bogus":1}`)
		if jobs := FindJobs(cwd); len(jobs) != 1 || jobs[0].Name != "good" {
			t.Errorf("jobs = %v", jobs)
		}
	})
	t.Run("runs the merge ensurer with ensure-merge", func(t *testing.T) {
		cwd := t.TempDir()
		enable(t, cwd, "merge-ensurer", "*/15 * * * *")
		if diff := cmp.Diff([]Job{{"merge-ensurer", "*/15 * * * *", []string{"ensure-merge"}}}, FindJobs(cwd)); diff != "" {
			t.Error(diff)
		}
	})
}

func TestEnableAndDisable(t *testing.T) {
	cwd := t.TempDir()
	writeAgent(t, cwd, "coder", ``)
	if err := Enable(cwd, "coder", "every minute"); err == nil || !strings.Contains(err.Error(), "invalid cron expression") {
		t.Errorf("bad pattern: %v", err)
	}
	if err := Enable(cwd, "ghost", "* * * * *"); err == nil {
		t.Error("unknown agent enabled")
	}
	enable(t, cwd, "coder", "* * * * *")
	enable(t, cwd, "coder", "*/5 * * * *")
	if got, _ := Enabled(cwd); !cmp.Equal(map[string]string{"coder": "*/5 * * * *"}, got) {
		t.Errorf("enabled = %v", got)
	}
	ignore, _ := os.ReadFile(filepath.Join(StateDir(cwd), ".gitignore"))
	if string(ignore) != "*\n" {
		t.Errorf(".gitignore = %q", ignore)
	}
	if err := Disable(cwd, "coder"); err != nil {
		t.Fatal(err)
	}
	if err := Disable(cwd, "coder"); err == nil {
		t.Error("disabling twice should fail")
	}
	if got, _ := Enabled(cwd); len(got) != 0 {
		t.Errorf("enabled = %v", got)
	}
}

func TestDefaultCron(t *testing.T) {
	cwd := t.TempDir()
	writeAgent(t, cwd, "coder", `{"cron":"*/5 * * * *"}`)
	writeAgent(t, cwd, "manual", ``)
	_ = os.WriteFile(config.ConfigPath(cwd), []byte(`{"baseBranch":"main","platform":"github","worktreeDir":".wt","tasks":{"project":"p","maxRetries":3},"mergeEnsurer":{"cron":"0 * * * *"}}`), 0o644)
	for name, want := range map[string]string{"coder": "*/5 * * * *", "manual": "", "merge-ensurer": "0 * * * *", "ghost": ""} {
		if got := DefaultCron(cwd, name); got != want {
			t.Errorf("DefaultCron(%s) = %q, want %q", name, got, want)
		}
	}
	if err := SaveDefaultCron(cwd, "manual", "15 * * * *"); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefaultCron(cwd, "merge-ensurer", "*/30 * * * *"); err != nil {
		t.Fatal(err)
	}
	if DefaultCron(cwd, "manual") != "15 * * * *" || DefaultCron(cwd, "merge-ensurer") != "*/30 * * * *" {
		t.Errorf("saved defaults not read back: %q %q", DefaultCron(cwd, "manual"), DefaultCron(cwd, "merge-ensurer"))
	}
}

func TestReadPID(t *testing.T) {
	cwd := t.TempDir()
	if ReadPID(cwd) != 0 {
		t.Error("pid without a pid file")
	}
	_ = os.MkdirAll(filepath.Dir(PIDPath(cwd)), 0o755)
	_ = os.WriteFile(PIDPath(cwd), []byte(strconv.Itoa(os.Getpid())), 0o644)
	if ReadPID(cwd) != os.Getpid() {
		t.Error("live scheduler not found")
	}
	_ = os.WriteFile(PIDPath(cwd), []byte("999999"), 0o644)
	if ReadPID(cwd) != 0 {
		t.Error("dead scheduler reported running")
	}
}

func TestRenderScheduler(t *testing.T) {
	now := time.Date(2026, 3, 7, 10, 2, 0, 0, time.Local)

	t.Run("says when no cron is enabled", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "coder", `{"cron":"*/5 * * * *"}`)
		want := []string{
			log.FormatSection("Scheduler"),
			"  · not running",
			"  · no crons enabled (enable one in the vteam TUI or with 'vteam cron enable <agent>')",
		}
		if diff := cmp.Diff(want, RenderScheduler(cwd, now)); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("lists each enabled cron with its next run and the scheduler state", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "coder", ``)
		writeAgent(t, cwd, "improver", ``)
		enable(t, cwd, "coder", "*/5 * * * *")
		enable(t, cwd, "improver", "0 0 * * *")
		want := []string{
			log.FormatSection("Scheduler"),
			"  ! not running, crons paused (start it with 'vteam cron start')",
			"  • coder     */5 * * * *  next 10:05 (in 3m)",
			"  • improver  0 0 * * *    next Mar 8 00:00 (in 13h 58m)",
		}
		if diff := cmp.Diff(want, RenderScheduler(cwd, now)); diff != "" {
			t.Error(diff)
		}
		_ = os.MkdirAll(filepath.Dir(PIDPath(cwd)), 0o755)
		_ = os.WriteFile(PIDPath(cwd), []byte(strconv.Itoa(os.Getpid())), 0o644)
		if got := RenderScheduler(cwd, now)[1]; got != "  ✓ running (pid "+strconv.Itoa(os.Getpid())+")" {
			t.Errorf("state line = %q", got)
		}
	})
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// harness runs a Scheduler on a fake clock: each value sent on ticks ends one Sleep, advancing the clock past it.
type harness struct {
	cwd   string
	out   *syncBuffer
	ticks chan string
	s     *Scheduler
}

func newHarness(t *testing.T, script map[string]string) *harness {
	cwd := t.TempDir()
	for name := range script {
		writeAgent(t, cwd, name, ``)
		enable(t, cwd, name, "* * * * *")
	}
	clock := &fakeClock{now: time.Date(2026, 3, 7, 10, 0, 30, 0, time.Local)}
	h := &harness{cwd: cwd, out: &syncBuffer{}, ticks: make(chan string)}
	h.s = &Scheduler{
		Cwd: cwd, Log: log.New(h.out, h.out), Now: clock.Now, Poll: time.Hour,
		Sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-h.ticks:
				clock.advance(d)
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Command: func(job Job, logPath string) (*exec.Cmd, error) {
			if job.Name == "unstartable" {
				return nil, errors.New("no binary")
			}
			return exec.Command("sh", "-c", `echo "$`+runlog.Env+`" > "`+filepath.Join(cwd, job.Name+".env")+`"; `+script[job.Name]), nil
		},
	}
	return h
}

func (h *harness) waitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(h.out.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in:\n%s", text, h.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *harness) start(t *testing.T) (cancel func() error, done chan error) {
	ctx, stop := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- h.s.Run(ctx) }()
	h.waitFor(t, "waiting for jobs")
	return func() error {
		stop()
		return <-done
	}, done
}

func TestSchedulerRunsJobsAndReportsTheirEnd(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "exit 0"})
	stop, _ := h.start(t)
	if ReadPID(h.cwd) != os.Getpid() {
		t.Error("pid file not written")
	}
	h.ticks <- "tick"
	h.waitFor(t, "coder finished in")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"vteam ▸ cron start   pid", "▍ Scheduler", "▍ Activity", "• coder started", "log       vteam/.logs/coder/", "scheduler stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	env, _ := os.ReadFile(filepath.Join(h.cwd, "coder.env"))
	if !strings.HasPrefix(string(env), filepath.Join(h.cwd, "vteam", ".logs", "coder")) {
		t.Errorf("child %s = %q", runlog.Env, env)
	}
	if _, err := os.Stat(PIDPath(h.cwd)); !os.IsNotExist(err) {
		t.Error("pid file left behind")
	}
}

func TestSchedulerSkipsATickWhileTheJobStillRuns(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "sleep 30"})
	stop, _ := h.start(t)
	h.ticks <- "tick"
	h.waitFor(t, "coder started")
	h.ticks <- "tick"
	h.waitFor(t, "coder skipped (still running)")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"! killed coder", "coder killed by SIGTERM after"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, h.out.String())
		}
	}
}

func TestSchedulerReportsFailedJobs(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "exit 3", "unstartable": ""})
	stop, _ := h.start(t)
	h.ticks <- "tick"
	h.ticks <- "tick"
	h.waitFor(t, "coder exited 3 after")
	h.waitFor(t, "unstartable could not start: no binary")
	_ = stop()
}

func TestSchedulerReportsStoppedJobs(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "exit 143"})
	stop, _ := h.start(t)
	h.ticks <- "tick"
	h.waitFor(t, "! coder stopped after")
	_ = stop()
}

func TestSchedulerRefusesToRunTwice(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "exit 0"})
	_ = os.MkdirAll(filepath.Dir(PIDPath(h.cwd)), 0o755)
	_ = os.WriteFile(PIDPath(h.cwd), []byte(strconv.Itoa(os.Getpid())), 0o644)
	if err := h.s.Run(context.Background()); !errors.Is(err, log.ErrReported) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(h.out.String(), "vteam cron is already running") {
		t.Errorf("output:\n%s", h.out.String())
	}
}

func TestSchedulerWithoutJobs(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "No crons enabled.") {
		t.Errorf("output:\n%s", h.out.String())
	}
	if _, err := os.Stat(PIDPath(h.cwd)); !os.IsNotExist(err) {
		t.Error("pid file written")
	}
}

func TestSchedulerPicksUpCronChangesAndStopsWhenNoneAreLeft(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "exit 0"})
	writeAgent(t, h.cwd, "improver", ``)
	_, done := h.start(t)
	enable(t, h.cwd, "improver", "*/5 * * * *")
	enable(t, h.cwd, "coder", "*/2 * * * *")
	h.ticks <- "tick"
	h.waitFor(t, "improver cron enabled */5 * * * *")
	h.waitFor(t, "coder cron changed */2 * * * *")
	_ = Disable(h.cwd, "improver")
	_ = Disable(h.cwd, "coder")
	h.ticks <- "tick"
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("scheduler did not stop:\n%s", h.out.String())
	}
	for _, want := range []string{"improver cron disabled", "coder cron disabled", "no crons enabled, stopping", "scheduler stopped"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, h.out.String())
		}
	}
	if _, err := os.Stat(PIDPath(h.cwd)); !os.IsNotExist(err) {
		t.Error("pid file left behind")
	}
}

func TestNextRun(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)
	if got := NextRun("* * * * *", now); !got.Equal(now.Truncate(time.Minute).Add(time.Minute)) {
		t.Errorf("got %v", got)
	}
	if !NextRun("bad", now).IsZero() {
		t.Error("invalid expression has a next run")
	}
}
