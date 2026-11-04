// Package claude runs `claude -p` with streaming JSON output and renders its progress.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leodnlv/vteam/internal/log"
)

const DefaultTimeout = 30 * time.Minute

type Options struct {
	SystemPrompt    string
	UserPrompt      string
	Dir             string
	Model           string
	Timeout         time.Duration
	AllowedTools    []string
	DisallowedTools []string
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// ResultText is the final result message, "" when Claude produced none.
	ResultText string
	CostUSD    *float64
	Duration   *time.Duration
}

type Runner interface {
	Run(ctx context.Context, opts Options) (Result, error)
}

type CLI struct {
	Binary string
	Log    *log.Logger
	// KillDelay is how long a timed-out claude gets between SIGTERM and SIGKILL.
	KillDelay time.Duration
}

func New(logger *log.Logger) *CLI {
	return &CLI{Binary: "claude", Log: logger, KillDelay: 10 * time.Second}
}

type streamEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Result       string   `json:"result"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	DurationMS   *int64   `json:"duration_ms"`
	NumTurns     *int     `json:"num_turns"`
}

func (e streamEvent) blocks() []ContentBlock {
	if e.Message == nil {
		return nil
	}
	var blocks []ContentBlock
	_ = json.Unmarshal(e.Message.Content, &blocks)
	return blocks
}

func Args(systemPromptFile string, opts Options) []string {
	args := []string{"-p", "--append-system-prompt-file", systemPromptFile, "--output-format", "stream-json", "--verbose", "--no-session-persistence"}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if len(opts.AllowedTools) > 0 {
		args = append(append(args, "--allowedTools"), opts.AllowedTools...)
	}
	if len(opts.DisallowedTools) > 0 {
		args = append(append(args, "--disallowedTools"), opts.DisallowedTools...)
	}
	return args
}

type stream struct {
	log   *log.Logger
	dir   string
	mu    sync.Mutex
	turns *int
	res   Result
}

func (s *stream) record(e streamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Result != "" {
		s.res.ResultText = e.Result
	}
	s.res.CostUSD = e.TotalCostUSD
	s.turns = e.NumTurns
	s.res.Duration = nil
	if e.DurationMS != nil {
		d := time.Duration(*e.DurationMS) * time.Millisecond
		s.res.Duration = &d
	}
}

func (s *stream) render(e streamEvent) {
	switch e.Type {
	case "assistant":
		for _, b := range e.blocks() {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				s.log.ClaudeText(b.Text)
			}
			if b.Type == "tool_use" && b.Name != "" {
				s.log.ToolCall(b.Name, DescribeTool(b.Name, b.Input, s.dir))
			}
		}
	case "user":
		for _, b := range e.blocks() {
			if msg := ToolErrorText(b); msg != "" {
				s.log.ToolError(msg)
			}
		}
	}
}

// readLines calls fn with each complete line and returns the trailing line without a newline.
func readLines(r io.Reader, raw *strings.Builder, fn func(line string)) string {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		raw.WriteString(line)
		if err != nil {
			return line
		}
		fn(strings.TrimSuffix(line, "\n"))
	}
}

func (s *stream) consumeStdout(r io.Reader, raw *strings.Builder) {
	trailing := readLines(r, raw, func(line string) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return
		}
		var e streamEvent
		if err := json.Unmarshal([]byte(trimmed), &e); err != nil {
			s.log.Step(trimmed)
			return
		}
		s.render(e)
		if e.Type == "result" {
			s.record(e)
		}
	})
	var e streamEvent
	if json.Unmarshal([]byte(strings.TrimSpace(trailing)), &e) == nil && e.Type == "result" {
		s.record(e)
	}
}

func (s *stream) consumeStderr(r io.Reader, raw *strings.Builder) {
	trailing := readLines(r, raw, func(line string) {
		if strings.TrimSpace(line) != "" {
			s.log.Warn("claude: " + line)
		}
	})
	if strings.TrimSpace(trailing) != "" {
		s.log.Warn("claude: " + trailing)
	}
}

func (c *CLI) Run(ctx context.Context, opts Options) (Result, error) {
	tmp, err := os.MkdirTemp("", "vteam-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	systemFile := filepath.Join(tmp, "system-prompt.md")
	userFile := filepath.Join(tmp, "user-prompt.md")
	if err := os.WriteFile(systemFile, []byte(opts.SystemPrompt), 0o600); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(userFile, []byte(opts.UserPrompt), 0o600); err != nil {
		return Result{}, err
	}
	c.Log.Field("system", systemFile)
	c.Log.Field("prompt", userFile)
	c.Log.Println("")

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Binary, Args(systemFile, opts)...)
	cmd.Dir = opts.Dir
	cmd.Cancel = func() error {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			c.Log.Fail(fmt.Sprintf("Claude timed out after %s, sending SIGTERM", log.FormatDuration(timeout)))
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = c.KillDelay
	stdin, err := os.Open(userFile)
	if err != nil {
		return Result{}, err
	}
	defer stdin.Close()
	cmd.Stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("failed to spawn claude: %w", err)
	}

	s := &stream{log: c.Log, dir: opts.Dir}
	var rawOut, rawErr strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.consumeStdout(stdout, &rawOut) }()
	go func() { defer wg.Done(); s.consumeStderr(stderr, &rawErr) }()
	wg.Wait()
	_ = cmd.Wait()

	res := s.res
	res.Stdout, res.Stderr = rawOut.String(), rawErr.String()
	res.ExitCode = exitCode(cmd)
	c.Log.AgentExit(log.ExitSummary{ExitCode: res.ExitCode, Duration: res.Duration, Turns: s.turns, CostUSD: res.CostUSD})
	return res, nil
}

func exitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return 1
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
