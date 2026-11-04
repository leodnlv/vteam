// Package execx runs external commands (aven, git, gh, glab) behind an interface so tests can replace them.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Stdin string
}

func (c Cmd) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

type Runner interface {
	Run(ctx context.Context, cmd Cmd) (stdout string, err error)
}

// Error is a command that ran and exited non-zero.
type Error struct {
	Cmd      Cmd
	Stdout   string
	Stderr   string
	ExitCode int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s failed: %s", e.Cmd, e.Output())
}

// Output is stderr, or stdout when stderr is empty, trimmed.
func (e *Error) Output() string {
	if out := strings.TrimSpace(e.Stderr); out != "" {
		return out
	}
	return strings.TrimSpace(e.Stdout)
}

// ErrNotFound reports that the command's binary is not on PATH.
var ErrNotFound = errors.New("executable not found on PATH")

type OS struct{}

func (OS) Run(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Stdin = strings.NewReader(c.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("%s: %w", c.Name, ErrNotFound)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), &Error{Cmd: c, Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitErr.ExitCode()}
	}
	return stdout.String(), fmt.Errorf("%s: %w", c, err)
}

// Fake records every command and answers with Handler. Safe for concurrent use.
type Fake struct {
	Handler func(Cmd) (string, error)

	mu    sync.Mutex
	calls []Cmd
}

func (f *Fake) Run(_ context.Context, c Cmd) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.Handler == nil {
		return "", nil
	}
	return f.Handler(c)
}

func (f *Fake) Calls() []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Cmd(nil), f.calls...)
}

// Args returns each recorded command as name followed by its arguments.
func (f *Fake) Args() [][]string {
	var out [][]string
	for _, c := range f.Calls() {
		out = append(out, append([]string{c.Name}, c.Args...))
	}
	return out
}

// Failure builds the error a command exits with, for Fake handlers.
func Failure(c Cmd, stderr string) error {
	return &Error{Cmd: c, Stderr: stderr, ExitCode: 1}
}
