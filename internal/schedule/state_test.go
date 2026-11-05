package schedule

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestJobCommandRunsAgentsAndTheMergeEnsurer(t *testing.T) {
	if diff := cmp.Diff([]string{"run", "coder"}, JobCommand("coder")); diff != "" {
		t.Errorf("agent command mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ensure-merge"}, JobCommand("merge-ensurer")); diff != "" {
		t.Errorf("merge ensurer command mismatch (-want +got):\n%s", diff)
	}
}

func TestDefaultCronFileNamesWhereTheDefaultIsSaved(t *testing.T) {
	if got := DefaultCronFile("coder"); got != "agent.json" {
		t.Errorf("DefaultCronFile(coder) = %q, want agent.json", got)
	}
	if got := DefaultCronFile("merge-ensurer"); got != "vteam.config.json" {
		t.Errorf("DefaultCronFile(merge-ensurer) = %q, want vteam.config.json", got)
	}
}

func TestEnabledReportsACorruptStateFile(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(StateDir(cwd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StatePath(cwd), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Enabled(cwd)

	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("Enabled error = %v, want an invalid state file error", err)
	}
}

func TestEnableKeepsOtherJobsEnabled(t *testing.T) {
	cwd := t.TempDir()
	writeAgent(t, cwd, "coder", ``)
	enable(t, cwd, "coder", "*/5 * * * *")
	enable(t, cwd, "merge-ensurer", "0 * * * *")

	got, err := Enabled(cwd)

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"coder": "*/5 * * * *", "merge-ensurer": "0 * * * *"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("enabled mismatch (-want +got):\n%s", diff)
	}
}
