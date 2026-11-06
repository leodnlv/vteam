// Package log renders vteam's terminal output: banner, sections, an elapsed-time gutter, and glyphs per line kind.
package log

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/leodnlv/vteam/internal/ansi"
)

type Logger struct {
	mu        sync.Mutex
	stdout    io.Writer
	stderr    io.Writer
	file      io.Writer
	now       func() time.Time
	start     time.Time
	wallClock bool
}

type Option func(*Logger)

func WithClock(now func() time.Time) Option {
	return func(l *Logger) { l.now = now }
}

func New(stdout, stderr io.Writer, opts ...Option) *Logger {
	l := &Logger{stdout: stdout, stderr: stderr, now: time.Now}
	for _, opt := range opts {
		opt(l)
	}
	l.start = l.now()
	return l
}

func Std() *Logger {
	return New(os.Stdout, os.Stderr)
}

// Discard is a Logger that drops everything, for tests that do not inspect output.
func Discard() *Logger {
	return New(io.Discard, io.Discard)
}

// SetFile copies every line, with ANSI colors stripped, into w (the run log). Pass nil to stop.
func (l *Logger) SetFile(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file = w
}

func (l *Logger) StartClock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.start = l.now()
}

func (l *Logger) UseWallClock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.wallClock = true
}

func (l *Logger) Elapsed() time.Duration {
	return l.now().Sub(l.start)
}

func (l *Logger) emit(w io.Writer, terminal, file string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(w, terminal+"\n")
	if l.file != nil {
		_, _ = io.WriteString(l.file, ansi.Strip(file)+"\n")
	}
}

func (l *Logger) Println(text string) { l.emit(l.stdout, text, text) }

func (l *Logger) Eprintln(text string) { l.emit(l.stderr, text, text) }

// WriteFile writes raw text to the run log only.
func (l *Logger) WriteFile(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_, _ = io.WriteString(l.file, ansi.Strip(text))
	}
}

func (l *Logger) clock() string {
	if l.wallClock {
		return FormatWallClock(l.now())
	}
	return FormatClock(l.Elapsed())
}

// bar is the gutter with the clock left out, so detail and continuation lines keep the timeline unbroken.
func (l *Logger) bar() string {
	return " " + strings.Repeat(" ", utf8.RuneCountInString(l.clock())) + " " + ansi.Rule("│")
}

func (l *Logger) prefix(glyph string) string {
	return " " + ansi.Muted(l.clock()) + " " + ansi.Rule("│") + " " + glyph + " "
}

// pad is the bar followed by spaces up to width visible cells.
func (l *Logger) pad(width int) string {
	bar := l.bar()
	return bar + strings.Repeat(" ", max(0, width-visibleLen(bar)))
}

// continueLines puts each line after the first under the bar, indented to width, painting every line on its own.
func (l *Logger) continueLines(text string, width int, paint func(string) string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if paint != nil && line != "" {
			line = paint(line)
		}
		switch {
		case i == 0:
		case line == "":
			line = l.bar()
		default:
			line = l.pad(width) + line
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func (l *Logger) render(glyph, text string, paint func(string) string) string {
	head := l.prefix(glyph)
	return head + l.continueLines(text, visibleLen(head), paint)
}

func (l *Logger) gutterIndent() string {
	return l.pad(visibleLen(l.prefix(" ")))
}

func (l *Logger) Banner(title, detail string) {
	l.emit(l.stdout, FormatBanner(title, detail), formatBanner(title, detail, false))
}

func (l *Logger) Section(title string) { l.Println(FormatSection(title)) }

func (l *Logger) Step(text string) { l.Println(l.render(ansi.Info("•"), text, nil)) }

func (l *Logger) OK(text string) { l.Println(l.render(ansi.OK("✓"), text, nil)) }

func (l *Logger) Warn(text string) { l.Eprintln(l.render(ansi.Warn("!"), text, ansi.Warn)) }

func (l *Logger) Fail(text string) { l.Eprintln(l.render(ansi.Fail("✗"), text, ansi.Fail)) }

func (l *Logger) Field(label, value string) {
	l.Println(l.gutterIndent() + ansi.Muted(PadEnd(label, labelWidth)) + value)
}

func (l *Logger) ClaudeText(text string) {
	l.Println(l.render(ansi.Claude("❯"), strings.TrimSpace(text), nil))
}

// ToolCall prints a tool line. In a terminal the detail is cut to one line; the run log gets it in full.
func (l *Logger) ToolCall(name, detail string) {
	head := l.prefix(ansi.Tool("⚙")) + ansi.Bold(ansi.Tool(PadEnd(name, 6))) + " "
	if detail == "" {
		l.Println(strings.TrimRight(head, " "))
		return
	}
	fileLine := head + l.continueLines(detail, visibleLen(head), ansi.Muted)
	width := TerminalWidth()
	if width == 0 {
		l.Println(fileLine)
		return
	}
	lines := strings.Split(detail, "\n")
	oneLine := lines[0]
	if len(lines) > 1 {
		oneLine += " …"
	}
	l.emit(l.stdout, head+ansi.Muted(Truncate(oneLine, width-visibleLen(head))), fileLine)
}

func (l *Logger) ToolError(message string) {
	first, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	l.Println(l.gutterIndent() + ansi.Fail("✗ "+Truncate(first, 200)))
}

type ExitSummary struct {
	ExitCode int
	Duration *time.Duration
	Turns    *int
	CostUSD  *float64
}

func (l *Logger) AgentExit(s ExitSummary) {
	parts := []string{fmt.Sprintf("claude exited %d", s.ExitCode)}
	if s.Duration != nil {
		parts = append(parts, FormatDuration(*s.Duration))
	}
	if s.Turns != nil {
		parts = append(parts, fmt.Sprintf("%d turns", *s.Turns))
	}
	if s.CostUSD != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *s.CostUSD))
	}
	text := strings.Join(parts, " · ")
	l.Println(l.bar())
	if s.ExitCode == 0 {
		l.OK(text)
	} else {
		l.Fail(text)
	}
}

func (l *Logger) Finished(agent, failure string) {
	elapsed := FormatDuration(l.Elapsed())
	if failure != "" {
		l.Eprintln("\n" + ansi.Bold(ansi.Fail("✗ "+agent+" failed after "+elapsed+":")) + " " + ansi.Fail(failure))
		return
	}
	l.Println("\n" + ansi.Bold(ansi.OK("✓ "+agent+" completed in "+elapsed)))
}

func (l *Logger) Stopped(agent string) {
	l.Eprintln("\n" + ansi.Bold(ansi.Warn("! "+agent+" stopped after "+FormatDuration(l.Elapsed()))))
}

// ErrReported marks an error that the command already printed; main exits non-zero without printing it again.
var ErrReported = errors.New("failure already reported")

// ErrStopped means a stop signal ended the command early; it was reported, and main exits with ExitStopped.
var ErrStopped = fmt.Errorf("stopped: %w", ErrReported)

// ExitStopped is the exit code of a command that ErrStopped ended, 128 + SIGTERM like a process killed by it.
const ExitStopped = 143
