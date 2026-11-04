package schedule

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/runlog"
)

func TestMain(m *testing.M) {
	os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
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

var cfg = &config.Config{MergeEnsurer: config.MergeEnsurer{Label: "x"}}

func TestFindJobs(t *testing.T) {
	t.Run("returns agents with cron patterns", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "improver", `{"cron":"0 */6 * * *"}`)
		writeAgent(t, cwd, "coder", `{"cron":"*/5 * * * *"}`)
		writeAgent(t, cwd, "manual", ``)
		want := []Job{{"coder", "*/5 * * * *", []string{"run", "coder"}}, {"improver", "0 */6 * * *", []string{"run", "improver"}}}
		if diff := cmp.Diff(want, FindJobs(cwd, cfg)); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("returns nothing when no agents exist or have cron", func(t *testing.T) {
		cwd := t.TempDir()
		if jobs := FindJobs(cwd, cfg); len(jobs) != 0 {
			t.Errorf("jobs = %v", jobs)
		}
		writeAgent(t, cwd, "manual", `{"model":"sonnet"}`)
		if jobs := FindJobs(cwd, cfg); len(jobs) != 0 {
			t.Errorf("jobs = %v", jobs)
		}
	})
	t.Run("skips agents with invalid config", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "broken", `{"cron":"*/5 * * * *","bogus":1}`)
		writeAgent(t, cwd, "good", `{"cron":"*/5 * * * *"}`)
		if jobs := FindJobs(cwd, cfg); len(jobs) != 1 || jobs[0].Name != "good" {
			t.Errorf("jobs = %v", jobs)
		}
	})
	t.Run("schedules the merge ensurer next to cron agents when it has a cron", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "coder", `{"cron":"*/5 * * * *"}`)
		withEnsurer := &config.Config{MergeEnsurer: config.MergeEnsurer{Cron: "*/15 * * * *"}}
		jobs := FindJobs(cwd, withEnsurer)
		if len(jobs) != 2 || !cmp.Equal(Job{"merge-ensurer", "*/15 * * * *", []string{"ensure-merge"}}, jobs[1]) {
			t.Errorf("jobs = %v", jobs)
		}
	})
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

	t.Run("says when nothing is scheduled", func(t *testing.T) {
		lines := RenderScheduler(t.TempDir(), cfg, now)
		if diff := cmp.Diff([]string{log.FormatSection("Scheduler"), "  · no agents have a cron schedule"}, lines); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("lists each job with its next run and the scheduler state", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgent(t, cwd, "coder", `{"cron":"*/5 * * * *"}`)
		writeAgent(t, cwd, "improver", `{"cron":"0 0 * * *"}`)
		lines := RenderScheduler(cwd, cfg, now)
		want := []string{
			log.FormatSection("Scheduler"),
			"  ! not running (start it with 'vteam cron start')",
			"  • coder     */5 * * * *  next 10:05 (in 3m)",
			"  • improver  0 0 * * *    next Mar 8 00:00 (in 13h 58m)",
		}
		if diff := cmp.Diff(want, lines); diff != "" {
			t.Error(diff)
		}
		_ = os.MkdirAll(filepath.Dir(PIDPath(cwd)), 0o755)
		_ = os.WriteFile(PIDPath(cwd), []byte(strconv.Itoa(os.Getpid())), 0o644)
		if got := RenderScheduler(cwd, cfg, now)[1]; got != "  ✓ running (pid "+strconv.Itoa(os.Getpid())+")" {
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

type harness struct {
	cwd   string
	out   *syncBuffer
	ticks chan string
	s     *Scheduler
	envs  chan string
}

func newHarness(t *testing.T, script map[string]string) *harness {
	cwd := t.TempDir()
	for name := range script {
		writeAgent(t, cwd, name, `{"cron":"* * * * *"}`)
	}
	h := &harness{cwd: cwd, out: &syncBuffer{}, ticks: make(chan string), envs: make(chan string, 10)}
	h.s = &Scheduler{
		Cwd: cwd, Config: cfg, Log: log.New(h.out, h.out), Now: time.Now,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			select {
			case <-h.ticks:
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

func (h *harness) start(t *testing.T) (cancel func() error) {
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx) }()
	h.waitFor(t, "waiting for jobs")
	return func() error {
		stop()
		return <-done
	}
}

func TestSchedulerRunsJobsAndReportsTheirEnd(t *testing.T) {
	h := newHarness(t, map[string]string{"coder": "exit 0"})
	stop := h.start(t)
	if ReadPID(h.cwd) != os.Getpid() {
		t.Error("pid file not written")
	}
	h.ticks <- "tick"
	h.waitFor(t, "coder finished in")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"vteam ▸ cron start   pid", "━━ Scheduler", "━━ Activity", "• coder started", "log       vteam/.logs/coder/", "scheduler stopped"} {
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
	stop := h.start(t)
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
	stop := h.start(t)
	h.ticks <- "tick"
	h.ticks <- "tick"
	h.waitFor(t, "coder exited 3 after")
	h.waitFor(t, "unstartable could not start: no binary")
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
	if !strings.Contains(h.out.String(), "No agents with cron patterns found.") {
		t.Errorf("output:\n%s", h.out.String())
	}
	if _, err := os.Stat(PIDPath(h.cwd)); !os.IsNotExist(err) {
		t.Error("pid file written")
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

