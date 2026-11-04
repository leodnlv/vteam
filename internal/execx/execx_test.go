package execx

import (
	"context"
	"errors"
	"testing"
)

func TestOSRunReturnsStdout(t *testing.T) {
	out, err := OS{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "cat; echo done"}, Stdin: "in\n"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "in\ndone\n" {
		t.Errorf("stdout = %q", out)
	}
}

func TestOSRunReportsExitErrors(t *testing.T) {
	_, err := OS{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo out; echo bad >&2; exit 3"}})
	var execErr *Error
	if !errors.As(err, &execErr) {
		t.Fatalf("err = %v", err)
	}
	if execErr.ExitCode != 3 || execErr.Output() != "bad" || execErr.Stdout != "out\n" {
		t.Errorf("got %+v", execErr)
	}
	if err.Error() != "sh -c echo out; echo bad >&2; exit 3 failed: bad" {
		t.Errorf("message = %q", err.Error())
	}
}

func TestOutputFallsBackToStdout(t *testing.T) {
	err := &Error{Stdout: " only stdout \n"}
	if err.Output() != "only stdout" {
		t.Errorf("Output() = %q", err.Output())
	}
}

func TestOSRunReportsMissingBinaries(t *testing.T) {
	_, err := OS{}.Run(context.Background(), Cmd{Name: "vteam-definitely-not-installed"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestOSRunUsesDir(t *testing.T) {
	dir := t.TempDir()
	out, err := OS{}.Run(context.Background(), Cmd{Name: "pwd", Dir: dir})
	if err != nil || out == "" {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{Handler: func(c Cmd) (string, error) {
		if c.Args[0] == "fail" {
			return "", Failure(c, "nope")
		}
		return "ok", nil
	}}
	if out, _ := f.Run(context.Background(), Cmd{Name: "x", Args: []string{"a"}}); out != "ok" {
		t.Errorf("out = %q", out)
	}
	if _, err := f.Run(context.Background(), Cmd{Name: "x", Args: []string{"fail"}}); err == nil {
		t.Error("expected failure")
	}
	if got := f.Args(); len(got) != 2 || got[1][1] != "fail" {
		t.Errorf("args = %v", got)
	}
}
