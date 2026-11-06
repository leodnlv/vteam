package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/schedule"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAgentsListsAgentsLogsLocksAndMergeEnsurer(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "vteam", "vteam.config.json"),
		`{"baseBranch":"main","platform":"github","worktreeDir":".wt","tasks":{"project":"p","maxRetries":3},"mergeEnsurer":{"cron":"*/15 * * * *"}}`)
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "coder", "AGENT.md"), "code")
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "coder", "agent.json"), `{"model":"sonnet","input":"task","cron":"*/5 * * * *"}`)
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "broken", "AGENT.md"), "x")
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "broken", "agent.json"), `{"nope":true}`)
	writeFile(t, filepath.Join(cwd, "vteam", ".logs", "coder", "2026-11-05T09-00-00-000Z.log"), "b")
	writeFile(t, filepath.Join(cwd, "vteam", ".logs", "coder", "2026-11-04T09-00-00-000Z.log"), "a")
	writeFile(t, filepath.Join(cwd, "vteam", ".logs", "retired", "2026-11-01T09-00-00-000Z.log"), "old")
	info, _ := json.Marshal(lock.Info{PID: os.Getpid(), Timestamp: fixedNow.UnixMilli(), Agent: "coder"})
	writeFile(t, filepath.Join(cwd, "vteam", ".locks", "coder.lock", "info.json"), string(info))

	agents := LoadAgents(cwd, fixedNow)

	var names []string
	byName := map[string]Agent{}
	for _, a := range agents {
		names = append(names, a.Name)
		byName[a.Name] = a
	}
	if got := strings.Join(names, ","); got != "broken,coder,retired,merge-ensurer" {
		t.Fatalf("names = %s", got)
	}
	coder := byName["coder"]
	if coder.Config == nil || coder.Config.Model != "sonnet" || coder.Running == nil || coder.DefaultCron != "*/5 * * * *" || coder.Cron != "" || !coder.NextRun.IsZero() {
		t.Errorf("coder = %+v", coder)
	}
	if len(coder.Logs) != 2 || !strings.HasSuffix(coder.Logs[0], "2026-11-04T09-00-00-000Z.log") {
		t.Errorf("coder logs should be oldest first: %v", coder.Logs)
	}
	if byName["broken"].LoadError == "" {
		t.Error("broken agent.json should surface a load error")
	}
	if me := byName["merge-ensurer"]; !me.Builtin || me.DefaultCron != "*/15 * * * *" || !me.Runnable() {
		t.Errorf("merge-ensurer = %+v", me)
	}
	if byName["broken"].Runnable() || byName["retired"].Runnable() {
		t.Error("broken and logs-only agents should not be runnable")
	}

	if err := schedule.Enable(cwd, "coder", "0 * * * *"); err != nil {
		t.Fatal(err)
	}
	for _, a := range LoadAgents(cwd, fixedNow) {
		if a.Name == "coder" && (a.Cron != "0 * * * *" || !a.NextRun.Equal(fixedNow.Add(time.Hour))) {
			t.Errorf("enabled coder = %+v", a)
		}
	}
}

func TestLogStartedAtParsesRunLogFilenames(t *testing.T) {
	got, ok := logStartedAt("/x/2026-11-03T15-40-00-007Z.log")
	want := time.Date(2026, 11, 3, 15, 40, 0, 7_000_000, time.UTC)
	if !ok || !got.Equal(want) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := logStartedAt("/x/notes.log"); ok {
		t.Error("non-timestamp names should not parse")
	}
}

func TestReadLogRereadsOnlyWhenTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	writeFile(t, path, "one\ntwo\n")
	first := readLog(path, logFile{})
	if strings.Join(first.lines, "|") != "one|two" {
		t.Fatalf("lines = %v", first.lines)
	}
	first.lines[0] = "cached"
	if again := readLog(path, first); again.lines[0] != "cached" {
		t.Error("unchanged file should reuse the cached lines")
	}
	writeFile(t, path, "one\ntwo\nthree\n")
	if grown := readLog(path, first); len(grown.lines) != 3 {
		t.Errorf("grown file lines = %v", grown.lines)
	}
}

func TestWrapHangingIndentsContinuationRows(t *testing.T) {
	raw := " 00:04  ❯ " + strings.Repeat("word ", 12)
	rows := wrapHanging(raw, 30, hangingIndent(raw))
	if len(rows) < 2 {
		t.Fatalf("expected wrapping: %q", rows)
	}
	for _, r := range rows[1:] {
		if !strings.HasPrefix(r, strings.Repeat(" ", 10)) {
			t.Errorf("continuation %q should be indented under the text", r)
		}
	}
	for _, r := range rows {
		if xansi.StringWidth(r) > 30 {
			t.Errorf("row %q is wider than 30", r)
		}
	}
	for _, section := range []string{"━━ Setup " + strings.Repeat("━", 80), "▍ Setup  " + strings.Repeat("─", 80)} {
		if got := wrapHanging(section, 30, hangingIndent(section)); len(got) != 1 {
			t.Errorf("section rule %q should never wrap", section)
		}
	}
}

func TestColorizeLogKeepsTextIntact(t *testing.T) {
	for _, line := range []string{
		"━━ Setup ━━━", "▍ Setup  ───────", "vteam ▸ coder   log: x", " 00:06 │ ⚙ Read   src/a.ts", " 00:19 │ ! no finding", "       │   branch    vteam/x", "       │   ✗ FAIL x", "       │", " 00:06  ⚙ Read   src/a.ts", " 00:19  ! no finding",
		"          ✗ FAIL x", "          branch    vteam/x", "✓ coder completed in 1m", "plain",
	} {
		if got := xansi.Strip(colorizeLog(line)); got != line {
			t.Errorf("colorizeLog changed the text: %q -> %q", line, got)
		}
	}
}

func agentsModel(t *testing.T, agents []Agent) Model {
	t.Helper()
	m := newTestModel(t, nil)
	m.deps.Agents = func(time.Time) []Agent { return agents }
	m.agents = newAgentsState().reload(agents)
	return press(t, m, "2")
}

func logDir(t *testing.T, lines int) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, stamp := range []string{"2026-11-03T15-00-00-000Z", "2026-11-05T09-00-00-000Z"} {
		var b strings.Builder
		for n := range lines {
			fmt.Fprintf(&b, "run%d line %d\n", i, n)
		}
		path := filepath.Join(dir, stamp+".log")
		writeFile(t, path, b.String())
		paths = append(paths, path)
	}
	return dir, paths
}

func TestAgentsTabShowsSidebarAndNewestLog(t *testing.T) {
	_, logs := logDir(t, 3)
	m := agentsModel(t, []Agent{
		{Name: "coder", Logs: logs, Running: &lock.Info{PID: 42, Timestamp: fixedNow.Add(-3 * time.Minute).UnixMilli()}},
		{Name: "improver"},
	})
	out := screen(m)
	for _, want := range []string{"AGENTS", "● coder", "3m", "○ improver", "running · pid 42", "following", "● live", "run1 line 2", "1 running"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents tab is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "run 2/2") {
		t.Error("log title should show which run is on screen")
	}
}

func TestAgentsSidebarSelectionResetsToNewestRun(t *testing.T) {
	_, logs := logDir(t, 3)
	m := agentsModel(t, []Agent{{Name: "a", Logs: logs}, {Name: "b", Logs: logs}})
	m = press(t, m, "p")
	if out := screen(m); !strings.Contains(out, "run0 line 2") || !strings.Contains(out, "run 1/2") {
		t.Fatalf("p should show the older run and its counter:\n%s", out)
	}
	m = press(t, m, "j")
	if m.agents.selected != 1 || m.agents.runIndex != -1 || !strings.Contains(screen(m), "run1 line 2") {
		t.Fatalf("selecting another agent should show its newest run (selected %d run %d)", m.agents.selected, m.agents.runIndex)
	}
	m = press(t, m, "p", "n")
	if m.agents.runIndex != -1 {
		t.Fatal("n back to the newest run should follow new runs again")
	}
	m = press(t, m, "g", "g")
	if m.agents.selected != 0 {
		t.Fatal("gg in the sidebar should select the first agent")
	}
}

func TestLogScrollPausesAndResumesFollow(t *testing.T) {
	_, logs := logDir(t, 100)
	m := agentsModel(t, []Agent{{Name: "a", Logs: logs}})
	m = press(t, m, "l")
	if !m.agents.focusLog {
		t.Fatal("l should focus the log")
	}
	if !strings.Contains(screen(m), "run1 line 99") {
		t.Fatal("following should show the end of the log")
	}
	m = press(t, m, "k")
	if m.agents.follow || !strings.Contains(screen(m), "paused") || strings.Contains(screen(m), "run1 line 99") {
		t.Fatal("scrolling up should pause following")
	}
	m = press(t, m, "G")
	if !m.agents.follow {
		t.Fatal("G should resume following")
	}
	m = press(t, m, "g", "g")
	if m.agents.follow || !strings.Contains(screen(m), "run1 line 0") {
		t.Fatal("gg should jump to the top and pause")
	}
	m = press(t, m, "h")
	if m.agents.focusLog {
		t.Fatal("h should return to the sidebar")
	}
}

func TestWrapToggle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-11-05T09-00-00-000Z.log")
	writeFile(t, path, strings.Repeat("x", 300)+"\n")
	m := agentsModel(t, []Agent{{Name: "a", Logs: []string{path}}})
	wrapped := len(m.logRows())
	m = press(t, m, "w")
	if wrapped < 3 || len(m.logRows()) != 1 || !strings.Contains(screen(m), "nowrap") {
		t.Fatalf("wrap rows %d, nowrap rows %d", wrapped, len(m.logRows()))
	}
}

func TestAgentWithoutLogs(t *testing.T) {
	m := agentsModel(t, []Agent{{Name: "fresh"}})
	if !strings.Contains(screen(m), "no logs yet for fresh") {
		t.Fatal(screen(m))
	}
}

func TestHangingIndentFollowsTheTimelineGutter(t *testing.T) {
	cases := map[string]int{
		" 00:06 │ • step":        11,
		" 14:03:27 │ • step":     14,
		" 00:06  • old step":     10,
		"       │   branch  x":   11,
		"       │            x":  20,
		"▍ Setup  ─────────────": -1,
	}
	for line, want := range cases {
		if got := hangingIndent(line); got != want {
			t.Errorf("hangingIndent(%q) = %d, want %d", line, got, want)
		}
	}
}

func TestStretchSectionsFillsTheWidth(t *testing.T) {
	lines := stretchSections([]string{"▍ Setup  " + strings.Repeat("─", 63), "  • lock acquired"}, 100)
	if w := xansi.StringWidth(lines[0]); w != 100 {
		t.Errorf("section is %d cells wide, want 100: %q", w, xansi.Strip(lines[0]))
	}
	if !strings.HasPrefix(xansi.Strip(lines[0]), "▍ Setup  ───") {
		t.Errorf("section lost its title: %q", xansi.Strip(lines[0]))
	}
	if lines[1] != "  • lock acquired" {
		t.Errorf("other lines changed: %q", lines[1])
	}
}

func TestMouseWheelScrollsTheLogAndFollowsAtTheBottom(t *testing.T) {
	_, logs := logDir(t, 100)
	m := agentsModel(t, []Agent{{Name: "a", Logs: logs}})
	m = wheel(t, m, tea.MouseWheelUp, 1)
	if m.agents.follow || strings.Contains(screen(m), "run1 line 99") {
		t.Fatal("wheel up should pause following")
	}
	m = wheel(t, m, tea.MouseWheelDown, 1)
	if !m.agents.follow {
		t.Fatal("wheel back to the bottom should resume following")
	}
}
