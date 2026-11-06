// Package notify shows desktop notifications for runs that produced something worth looking at.
package notify

import (
	"context"
	"runtime"

	"github.com/leodnlv/vteam/internal/execx"
)

type Notifier interface {
	Notify(ctx context.Context, title, message string) error
}

// MacOS shows notifications through osascript. The text goes in as script arguments, never into the script source.
type MacOS struct {
	Runner execx.Runner
}

func (m MacOS) Notify(ctx context.Context, title, message string) error {
	_, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "osascript",
		Args: []string{
			"-e", "on run argv",
			"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
			"-e", "end run",
			message, title,
		},
	})
	return err
}

type Noop struct{}

func (Noop) Notify(context.Context, string, string) error { return nil }

func New(runner execx.Runner) Notifier {
	return forOS(runtime.GOOS, runner)
}

func forOS(goos string, runner execx.Runner) Notifier {
	if goos == "darwin" {
		return MacOS{Runner: runner}
	}
	return Noop{}
}
