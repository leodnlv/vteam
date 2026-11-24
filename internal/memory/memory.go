// Package memory stores an agent's memory under vteam/.memory/<agent>/: durable facts that only the orchestrator
// appends to (facts.md) and a rolling log that the curation agent rewrites after each run (store.md).
package memory

import (
	"os"
	"path/filepath"
	"strings"
)

// FactsWarnLines is the facts.md size above which vteam status asks a human to prune it.
const FactsWarnLines = 50

func Dir(cwd, agent string) string {
	return filepath.Join(cwd, "vteam", ".memory", agent)
}

func LogPath(cwd, agent string) string {
	return filepath.Join(Dir(cwd, agent), "store.md")
}

func FactsPath(cwd, agent string) string {
	return filepath.Join(Dir(cwd, agent), "facts.md")
}

func read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func ReadLog(cwd, agent string) string { return read(LogPath(cwd, agent)) }

func ReadFacts(cwd, agent string) string { return read(FactsPath(cwd, agent)) }

func WriteLog(cwd, agent, content string) error {
	return write(LogPath(cwd, agent), strings.TrimSpace(content)+"\n")
}

func write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// CountFacts is the number of non-blank lines in facts.md.
func CountFacts(cwd, agent string) int {
	n := 0
	for line := range strings.Lines(ReadFacts(cwd, agent)) {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func normalizeFact(fact string) string {
	fact = strings.Join(strings.Fields(fact), " ")
	return strings.TrimSpace(strings.TrimPrefix(fact, "- "))
}

// AppendFacts adds each fact to facts.md as a "- " line, skipping blank ones and ones already in the file, and
// returns how many it added. Facts are never removed by vteam; humans prune the file.
func AppendFacts(cwd, agent string, facts []string) (int, error) {
	current := ReadFacts(cwd, agent)
	known := map[string]bool{}
	for line := range strings.Lines(current) {
		known[normalizeFact(line)] = true
	}
	var added []string
	for _, f := range facts {
		f = normalizeFact(f)
		if f == "" || known[f] {
			continue
		}
		known[f] = true
		added = append(added, "- "+f)
	}
	if len(added) == 0 {
		return 0, nil
	}
	content := strings.Join(added, "\n") + "\n"
	if current != "" {
		content = current + "\n" + content
	}
	return len(added), write(FactsPath(cwd, agent), content)
}
