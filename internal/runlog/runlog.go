// Package runlog writes each run's output, colors stripped, to vteam/.logs/<agent>/<timestamp>.log.
package runlog

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/log"
)

// Env carries the log path the scheduler picked for a child run.
const Env = "VTEAM_RUN_LOG"

func FormatFilename(t time.Time) string {
	iso := t.UTC().Format("2006-01-02T15:04:05.000Z")
	return strings.NewReplacer(":", "-", ".", "-").Replace(iso) + ".log"
}

func AgentDir(cwd, agent string) string {
	return filepath.Join(cwd, "vteam", ".logs", agent)
}

func Path(cwd, agent string, now time.Time) string {
	return filepath.Join(AgentDir(cwd, agent), FormatFilename(now))
}

type RunLog struct {
	Path   string
	file   *os.File
	logger *log.Logger
}

// Start opens the run log and attaches it to logger, so every line the logger prints is copied into it.
func Start(cwd, agent string, now time.Time, logger *log.Logger) (*RunLog, error) {
	path := os.Getenv(Env)
	if path == "" {
		path = Path(cwd, agent, now)
	}
	return Open(path, logger)
}

// Open appends to the log at path and attaches it to logger.
func Open(path string, logger *log.Logger) (*RunLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	logger.SetFile(file)
	return &RunLog{Path: path, file: file, logger: logger}, nil
}

func (r *RunLog) Close() error {
	r.logger.SetFile(nil)
	return r.file.Close()
}
