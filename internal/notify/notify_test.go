package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/leodnlv/vteam/internal/execx"
)

func TestMacOSPassesTextAsArguments(t *testing.T) {
	fake := &execx.Fake{Handler: func(execx.Cmd) (string, error) { return "", nil }}
	err := MacOS{Runner: fake}.Notify(context.Background(), `vteam "coder"`, `done" & do shell script "x`)
	if err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Name != "osascript" {
		t.Fatalf("calls = %v", calls)
	}
	args := calls[0].Args
	if got := args[len(args)-2:]; got[0] != `done" & do shell script "x` || got[1] != `vteam "coder"` {
		t.Fatalf("text args = %q", got)
	}
	for _, a := range args[:len(args)-2] {
		if a != "-e" && a == `done" & do shell script "x` {
			t.Fatalf("message leaked into script: %q", args)
		}
	}
}

func TestMacOSReturnsRunnerError(t *testing.T) {
	want := errors.New("boom")
	fake := &execx.Fake{Handler: func(execx.Cmd) (string, error) { return "", want }}
	if err := (MacOS{Runner: fake}).Notify(context.Background(), "t", "m"); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestForOS(t *testing.T) {
	fake := &execx.Fake{}
	if _, ok := forOS("darwin", fake).(MacOS); !ok {
		t.Fatal("darwin should notify")
	}
	if _, ok := forOS("linux", fake).(Noop); !ok {
		t.Fatal("linux should be a no-op")
	}
}
