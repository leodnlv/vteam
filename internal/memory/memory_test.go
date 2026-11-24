package memory

import (
	"os"
	"testing"
)

func TestAppendFacts(t *testing.T) {
	cwd := t.TempDir()
	added, err := AppendFacts(cwd, "coder", []string{"  uses   just  ", "", "- uses just", "multi\nline"})
	if err != nil || added != 2 {
		t.Fatalf("added %d, err %v", added, err)
	}
	if got := ReadFacts(cwd, "coder"); got != "- uses just\n- multi line" {
		t.Errorf("facts = %q", got)
	}

	if err := os.WriteFile(FactsPath(cwd, "coder"), []byte("# edited by hand\n- uses just\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err = AppendFacts(cwd, "coder", []string{"uses just", "new"})
	if err != nil || added != 1 {
		t.Fatalf("added %d, err %v", added, err)
	}
	data, _ := os.ReadFile(FactsPath(cwd, "coder"))
	if string(data) != "# edited by hand\n- uses just\n- new\n" {
		t.Errorf("facts = %q", data)
	}
	if n := CountFacts(cwd, "coder"); n != 3 {
		t.Errorf("count = %d", n)
	}
}

func TestAppendNothingLeavesNoFile(t *testing.T) {
	cwd := t.TempDir()
	if added, err := AppendFacts(cwd, "coder", []string{" "}); err != nil || added != 0 {
		t.Fatalf("added %d, err %v", added, err)
	}
	if _, err := os.Stat(FactsPath(cwd, "coder")); !os.IsNotExist(err) {
		t.Errorf("facts.md created: %v", err)
	}
}

func TestLog(t *testing.T) {
	cwd := t.TempDir()
	if ReadLog(cwd, "coder") != "" || CountFacts(cwd, "coder") != 0 {
		t.Error("missing files must read as empty")
	}
	if err := WriteLog(cwd, "coder", "\n  entry\n\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(LogPath(cwd, "coder"))
	if string(data) != "entry\n" {
		t.Errorf("log = %q", data)
	}
}
