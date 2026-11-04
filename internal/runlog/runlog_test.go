package runlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leodnlv/vteam/internal/log"
)

func TestFormatFilename(t *testing.T) {
	got := FormatFilename(time.Date(2026, 4, 21, 8, 40, 0, 0, time.UTC))
	if got != "2026-04-21T08-40-00-000Z.log" {
		t.Errorf("got %q", got)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestStartWritesAPerRunFile(t *testing.T) {
	t.Setenv(Env, "")
	dir := t.TempDir()
	r, err := Start(dir, "coder", time.Date(2026, 4, 21, 8, 40, 0, 0, time.UTC), log.Discard())
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if want := filepath.Join(dir, "vteam", ".logs", "coder", "2026-04-21T08-40-00-000Z.log"); r.Path != want {
		t.Errorf("path = %q, want %q", r.Path, want)
	}
}

func TestStartUsesTheSchedulerPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vteam", ".logs", "coder", "from-scheduler.log")
	t.Setenv(Env, path)
	r, err := Start(dir, "coder", time.Now(), log.Discard())
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if r.Path != path {
		t.Errorf("path = %q", r.Path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
}

func TestLoggerLinesGoToTheFileWithoutColorsUntilClosed(t *testing.T) {
	t.Setenv(Env, "")
	t.Setenv("FORCE_COLOR", "1")
	var terminal bytes.Buffer
	logger := log.New(&terminal, &terminal)
	r, err := Start(t.TempDir(), "coder", time.Now(), logger)
	if err != nil {
		t.Fatal(err)
	}
	logger.Println("out line")
	logger.Eprintln("\x1b[32m✓ err line\x1b[39m")
	logger.WriteFile("direct line\n")
	_ = r.Close()
	logger.Println("after close")

	if got := read(t, r.Path); got != "out line\n✓ err line\ndirect line\n" {
		t.Errorf("file = %q", got)
	}
	if !bytes.Contains(terminal.Bytes(), []byte("after close")) {
		t.Error("terminal output stopped after close")
	}
}

func TestStartAppendsToAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	_ = os.WriteFile(path, []byte("earlier\n"), 0o644)
	t.Setenv(Env, path)
	logger := log.Discard()
	r, _ := Start(dir, "coder", time.Now(), logger)
	logger.Println("later")
	_ = r.Close()
	if got := read(t, path); got != "earlier\nlater\n" {
		t.Errorf("file = %q", got)
	}
}
