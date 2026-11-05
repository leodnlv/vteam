package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/golden"
	"github.com/leodnlv/vteam/internal/log"
)

// TestMain lets the test binary act as a fake claude when FAKE_CLAUDE is set.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CLAUDE") == "1" {
		fakeClaude()
		return
	}
	os.Exit(m.Run())
}

func fakeClaude() {
	if path := os.Getenv("FAKE_CLAUDE_ARGS_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(os.Args[1:], "\n")), 0o644)
	}
	stdin, _ := io.ReadAll(os.Stdin)
	if path := os.Getenv("FAKE_CLAUDE_STDIN_FILE"); path != "" {
		_ = os.WriteFile(path, stdin, 0o644)
	}
	if os.Getenv("FAKE_CLAUDE_HANG") == "1" {
		signal.Ignore(syscall.SIGTERM)
		if os.Getenv("FAKE_CLAUDE_HONOR_TERM") == "1" {
			signal.Reset(syscall.SIGTERM)
		}
		time.Sleep(time.Minute)
	}
	os.Stderr.WriteString("warning on stderr\n")
	if fixture := os.Getenv("FAKE_CLAUDE_FIXTURE"); fixture != "" {
		data, _ := os.ReadFile(fixture)
		os.Stdout.Write(data)
	}
	code, _ := strconv.Atoi(os.Getenv("FAKE_CLAUDE_EXIT"))
	os.Exit(code)
}

func fakeCLI(t *testing.T) (*CLI, *bytes.Buffer) {
	t.Setenv("FAKE_CLAUDE", "1")
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	logger := log.New(&out, &out, log.WithClock(func() time.Time { return clock }))
	return &CLI{Binary: os.Args[0], Log: logger, KillDelay: time.Second}, &out
}

func TestDescribeToolGolden(t *testing.T) {
	var g struct {
		Cwd   string
		Tools []struct {
			Name     string
			Input    map[string]any
			Expected string
		}
		ToolErrors []struct {
			Block    ContentBlock
			Expected *string
		}
	}
	golden.Load(t, "describe-tool", &g)
	for _, c := range g.Tools {
		if got := DescribeTool(c.Name, c.Input, g.Cwd); got != c.Expected {
			t.Errorf("DescribeTool(%s, %v) = %q, want %q", c.Name, c.Input, got, c.Expected)
		}
	}
	for _, c := range g.ToolErrors {
		want := ""
		if c.Expected != nil {
			want = *c.Expected
		}
		if got := ToolErrorText(c.Block); got != want {
			t.Errorf("ToolErrorText(%+v) = %q, want %q", c.Block, got, want)
		}
	}
}

var (
	tmpPrefix = regexp.MustCompile(`/[^\s]*vteam-[^/\s]+/`)
	clockCol  = regexp.MustCompile(`(?m)^ \d\d:\d\d(:\d\d)? `)
)

const stderrLine = " <clock>  ! claude: warning on stderr"

func TestRunGolden(t *testing.T) {
	var cases []struct {
		Fixture  string
		ExitCode int
		Args     []string
		Stdin    string
		Output   []string
		Result   struct {
			ExitCode   int
			ResultText *string
			CostUSD    *float64
			DurationMs *int64
			Stderr     string
		}
	}
	golden.Load(t, "claude-run", &cases)
	for _, c := range cases {
		t.Run(c.Fixture, func(t *testing.T) {
			cli, out := fakeCLI(t)
			dir := t.TempDir()
			fixture, _ := os.ReadFile(golden.Path(t, "claude", c.Fixture))
			served := filepath.Join(t.TempDir(), "served.jsonl")
			_ = os.WriteFile(served, bytes.ReplaceAll(fixture, []byte("/repo/wt"), []byte(dir)), 0o644)
			argsFile := filepath.Join(t.TempDir(), "args")
			stdinFile := filepath.Join(t.TempDir(), "stdin")
			t.Setenv("FAKE_CLAUDE_FIXTURE", served)
			t.Setenv("FAKE_CLAUDE_EXIT", strconv.Itoa(c.ExitCode))
			t.Setenv("FAKE_CLAUDE_ARGS_FILE", argsFile)
			t.Setenv("FAKE_CLAUDE_STDIN_FILE", stdinFile)

			res, err := cli.Run(context.Background(), Options{
				SystemPrompt: "SYSTEM", UserPrompt: "USER", Dir: dir, Model: "sonnet",
				AllowedTools: []string{"Read", "Bash(git *)"}, DisallowedTools: []string{"WebFetch"},
			})
			if err != nil {
				t.Fatal(err)
			}

			args, _ := os.ReadFile(argsFile)
			gotArgs := strings.Split(tmpPrefix.ReplaceAllString(string(args), "<tmp>/"), "\n")
			if diff := cmp.Diff(c.Args, gotArgs); diff != "" {
				t.Errorf("args (-want +got):\n%s", diff)
			}
			if stdin, _ := os.ReadFile(stdinFile); string(stdin) != c.Stdin {
				t.Errorf("stdin = %q", stdin)
			}

			rendered := strings.ReplaceAll(out.String(), dir, "/repo/wt")
			rendered = tmpPrefix.ReplaceAllString(clockCol.ReplaceAllString(rendered, " <clock> "), "<tmp>/")
			lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
			if n := strings.Count(rendered, stderrLine+"\n"); n != 1 {
				t.Errorf("stderr warning appears %d times", n)
			}
			var withoutStderr []string
			for _, l := range lines {
				if l != stderrLine {
					withoutStderr = append(withoutStderr, l)
				}
			}
			var want []string
			for _, l := range c.Output {
				if l != stderrLine {
					want = append(want, strings.Split(l, "\n")...)
				}
			}
			if diff := cmp.Diff(want, withoutStderr); diff != "" {
				t.Errorf("output (-want +got):\n%s", diff)
			}

			wantText := ""
			if c.Result.ResultText != nil {
				wantText = *c.Result.ResultText
			}
			if res.ExitCode != c.Result.ExitCode || res.ResultText != wantText || res.Stderr != c.Result.Stderr {
				t.Errorf("result = %+v", res)
			}
			if diff := cmp.Diff(c.Result.CostUSD, res.CostUSD); diff != "" {
				t.Errorf("cost: %s", diff)
			}
			if c.Result.DurationMs != nil && (res.Duration == nil || res.Duration.Milliseconds() != *c.Result.DurationMs) {
				t.Errorf("duration = %v", res.Duration)
			}
			var events int
			for _, line := range strings.Split(res.Stdout, "\n") {
				if json.Valid([]byte(line)) {
					events++
				}
			}
			if events == 0 {
				t.Error("raw stdout not captured")
			}
		})
	}
}

func TestRunWithoutOptionalFlags(t *testing.T) {
	if got := Args("/s.md", Options{}); strings.Join(got, " ") != "-p --append-system-prompt-file /s.md --output-format stream-json --verbose --no-session-persistence" {
		t.Errorf("args = %v", got)
	}
}

func TestArgsPassJSONSchema(t *testing.T) {
	got := Args("/s.md", Options{JSONSchema: `{"type":"object"}`})
	if i := slices.Index(got, "--json-schema"); i < 0 || got[i+1] != `{"type":"object"}` {
		t.Errorf("args = %v", got)
	}
}

func TestBuiltinTools(t *testing.T) {
	cases := []struct {
		allowed []string
		want    []string
		ok      bool
	}{
		{nil, nil, false},
		{[]string{"Read", "Glob", "Grep"}, []string{"Read", "Glob", "Grep"}, true},
		{[]string{"Read", "Bash(npm *)", "Bash(git *)", "Edit"}, []string{"Read", "Bash", "Edit"}, true},
		{[]string{"mcp__slack__send_message"}, nil, true},
	}
	for _, c := range cases {
		got, ok := BuiltinTools(c.allowed)
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("BuiltinTools(%v) = %v, %v; want %v, %v", c.allowed, got, ok, c.want, c.ok)
		}
	}
	if args := Args("/s.md", Options{AllowedTools: []string{"mcp__slack__send_message"}}); !slices.Contains(args, "--tools") || args[slices.Index(args, "--tools")+1] != "" {
		t.Errorf("an MCP-only allowlist should disable every built-in tool: %v", args)
	}
}

func TestRunCapturesStructuredOutput(t *testing.T) {
	cli, _ := fakeCLI(t)
	fixture := filepath.Join(t.TempDir(), "structured.jsonl")
	lines := `{"type":"assistant","message":{"content":[{"type":"text","text":"Well shoot, here it is."}]}}` + "\n" +
		`{"type":"result","result":"{\"status\":\"completed\"}","structured_output":{"status":"completed","summary":"ok"},"num_turns":2}` + "\n"
	if err := os.WriteFile(fixture, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_FIXTURE", fixture)
	res, err := cli.Run(context.Background(), Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.StructuredOutput != `{"status":"completed","summary":"ok"}` {
		t.Errorf("structured output = %q", res.StructuredOutput)
	}
}

func TestRunTimesOutWithSIGTERM(t *testing.T) {
	cli, out := fakeCLI(t)
	t.Setenv("FAKE_CLAUDE_HANG", "1")
	t.Setenv("FAKE_CLAUDE_HONOR_TERM", "1")
	start := time.Now()
	res, err := cli.Run(context.Background(), Options{Dir: t.TempDir(), Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("did not stop the process promptly")
	}
	if res.ExitCode == 0 || !strings.Contains(out.String(), "Claude timed out after 0s, sending SIGTERM") {
		t.Errorf("exit %d output:\n%s", res.ExitCode, out.String())
	}
}

func TestRunKillsAProcessThatIgnoresSIGTERM(t *testing.T) {
	cli, _ := fakeCLI(t)
	cli.KillDelay = 200 * time.Millisecond
	t.Setenv("FAKE_CLAUDE_HANG", "1")
	start := time.Now()
	res, err := cli.Run(context.Background(), Options{Dir: t.TempDir(), Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second || res.ExitCode == 0 {
		t.Errorf("exit %d after %s", res.ExitCode, time.Since(start))
	}
}

func TestRunReportsAMissingBinary(t *testing.T) {
	cli, _ := fakeCLI(t)
	cli.Binary = "vteam-no-such-claude"
	if _, err := cli.Run(context.Background(), Options{Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "failed to spawn claude") {
		t.Errorf("err = %v", err)
	}
}
