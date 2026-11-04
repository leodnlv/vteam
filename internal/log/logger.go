// Package log renders vteam's terminal output: banner, sections, an elapsed-time gutter, and glyphs per line kind.
package log

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

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

func (l *Logger) prefix(glyph string) string {
	var clock string
	if l.wallClock {
		clock = FormatWallClock(l.now())
	} else {
		clock = FormatClock(l.Elapsed())
	}
	return " " + ansi.Gray(clock) + "  " + glyph + " "
}

func (l *Logger) render(glyph, text string, paint func(string) string) string {
	head := l.prefix(glyph)
	body := indentContinuation(text, visibleLen(head))
	if paint != nil {
		body = paint(body)
	}
	return head + body
}

func (l *Logger) gutterIndent() string {
	return strings.Repeat(" ", visibleLen(l.prefix(" ")))
}

func (l *Logger) Banner(title, detail string) { l.Println(FormatBanner(title, detail)) }

func (l *Logger) Section(title string) { l.Println(FormatSection(title)) }

func (l *Logger) Step(text string) { l.Println(l.render(ansi.Blue("•"), text, nil)) }

func (l *Logger) OK(text string) { l.Println(l.render(ansi.Green("✓"), text, nil)) }

func (l *Logger) Warn(text string) { l.Eprintln(l.render(ansi.Yellow("!"), text, ansi.Yellow)) }

func (l *Logger) Fail(text string) { l.Eprintln(l.render(ansi.Red("✗"), text, ansi.Red)) }

func (l *Logger) Field(label, value string) {
	l.Println(l.gutterIndent() + ansi.Gray(PadEnd(label, labelWidth)) + value)
}

func (l *Logger) ClaudeText(text string) {
	l.Println(l.render(ansi.Magenta("❯"), strings.TrimSpace(text), nil))
}

// ToolCall prints a tool line. In a terminal the detail is cut to one line; the run log gets it in full.
func (l *Logger) ToolCall(name, detail string) {
	head := l.prefix(ansi.Yellow("⚙")) + ansi.Bold(ansi.Yellow(PadEnd(name, 6))) + " "
	if detail == "" {
		l.Println(strings.TrimRight(head, " "))
		return
	}
	fileLine := head + indentContinuation(detail, visibleLen(head))
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
	l.emit(l.stdout, head+ansi.Dim(Truncate(oneLine, width-visibleLen(head))), fileLine)
}

func (l *Logger) ToolError(message string) {
	first, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	l.Println(l.gutterIndent() + ansi.Red("✗ "+Truncate(first, 200)))
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
	l.Println("")
	if s.ExitCode == 0 {
		l.OK(text)
	} else {
		l.Fail(text)
	}
}

func (l *Logger) Finished(agent, failure string) {
	elapsed := FormatDuration(l.Elapsed())
	if failure != "" {
		l.Eprintln("\n" + ansi.Bold(ansi.Red("✗ "+agent+" failed after "+elapsed+":")) + " " + ansi.Red(failure))
		return
	}
	l.Println("\n" + ansi.Bold(ansi.Green("✓ "+agent+" completed in "+elapsed)))
}
