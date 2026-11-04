// Package golden loads the language-neutral test cases captured from the TypeScript implementation.
package golden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func Path(t testing.TB, elem ...string) string {
	return filepath.Join(append([]string{RepoRoot(t), "testdata"}, elem...)...)
}

func Load(t testing.TB, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(Path(t, "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode golden %s: %v", name, err)
	}
}
