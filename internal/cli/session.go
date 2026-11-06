package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/output"
	"github.com/leodnlv/vteam/internal/run"
	"github.com/leodnlv/vteam/internal/runlog"
	"github.com/leodnlv/vteam/internal/schedule"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/spf13/cobra"
)

func submitCmd() *cobra.Command {
	var project, id, file string
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit the output of an interactive session (JSON on stdin) and close the session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input := cmd.InOrStdin()
			if file != "" {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				input = f
			}
			return submit(cmd.Context(), project, id, input)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "the vteam project (default: found from the current directory)")
	cmd.Flags().StringVar(&id, "session", "", "the session's run id (default: the session whose worktree holds the current directory)")
	cmd.Flags().StringVar(&file, "file", "", "read the output JSON from this file instead of stdin")
	return cmd
}

func findSession(ctx context.Context, project, id string) (string, session.Session, error) {
	here, err := cwd()
	if err != nil {
		return "", session.Session{}, err
	}
	if project == "" {
		if project, err = session.ProjectDir(ctx, here); err != nil {
			return "", session.Session{}, err
		}
	}
	var s session.Session
	if id != "" {
		s, err = session.Load(project, id)
	} else {
		s, err = session.FindByDir(project, here)
	}
	return project, s, err
}

func submit(ctx context.Context, project, id string, input io.Reader) error {
	project, s, err := findSession(ctx, project, id)
	if err != nil {
		return err
	}
	switch s.Status {
	case session.Open:
	case session.Abandoned:
		return errors.New("this session was abandoned (its window was closed) and its task was released; it can no longer be submitted")
	default:
		return errors.New("this session was already submitted")
	}
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	out, err := output.ParseInteractive(string(data))
	if err != nil {
		return fmt.Errorf("%w (fix the JSON and submit again)", err)
	}

	l, err := lock.Acquire(ctx, filepath.Join(session.Dir(project, s.RunID), "submit"), "submit", 30*time.Second)
	if err != nil {
		return err
	}
	defer l.Release()
	if s, err = session.Load(project, s.RunID); err != nil {
		return err
	}
	if s.Status != session.Open {
		return errors.New("this session was already submitted")
	}

	logger := log.Std()
	logPath := s.LogPath
	if logPath == "" {
		logPath = runlog.Path(project, s.Agent, time.Now())
	}
	runLog, err := runlog.Open(logPath, logger)
	if err != nil {
		return err
	}
	defer func() { _ = runLog.Close() }()
	logger.StartClock()
	logger.Banner(s.Agent+" submit", "log: "+relative(project, runLog.Path))

	agent, err := config.ResolveAgent(s.Agent, project)
	if err != nil {
		return err
	}
	cfg, err := config.Load(project)
	if err != nil {
		return err
	}
	s, err = run.Submit(ctx, run.NewDeps(project, cfg, logger), agent, s, out)
	if err != nil {
		return fmt.Errorf("%w (the session is still open: fix the problem and submit again)", err)
	}
	if err := schedule.SpawnFinalize(project, s.RunID, runLog.Path); err != nil {
		return fmt.Errorf("submitted, but failed to close the session: %w", err)
	}
	logger.OK("submitted; the worktree is removed once the on-finish hook and memory curation are done")
	return nil
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Interactive session internals", Hidden: true}
	cmd.AddCommand(&cobra.Command{
		Use:   "finalize <run-id>",
		Short: "Run the on-finish hook and memory curation of a submitted session, then close it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			return finalize(cmd.Context(), dir, args[0])
		},
	})
	return cmd
}

func finalize(ctx context.Context, dir, runID string) error {
	s, err := session.Load(dir, runID)
	if err != nil {
		return err
	}
	if s.Status != session.Submitting {
		return fmt.Errorf("session %s is %s, not submitting", runID, s.Status)
	}
	logger := log.Std()
	runLog, err := runlog.Start(dir, s.Agent, time.Now(), logger)
	if err != nil {
		return err
	}
	defer func() { _ = runLog.Close() }()
	logger.StartClock()
	agent, err := config.ResolveAgent(s.Agent, dir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	return run.Finalize(ctx, run.NewDeps(dir, cfg, logger), agent, s)
}
