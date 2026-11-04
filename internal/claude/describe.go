package claude

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type ContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   map[string]any  `json:"input,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

func str(input map[string]any, key string) string {
	s, _ := input[key].(string)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func displayPath(path, cwd string) string {
	rel, err := filepath.Rel(cwd, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return path
	}
	return rel
}

// DescribeTool renders the one-line detail shown after a tool name in run output.
func DescribeTool(name string, input map[string]any, cwd string) string {
	path := firstNonEmpty(str(input, "file_path"), str(input, "notebook_path"), str(input, "path"))
	shownPath := ""
	if path != "" {
		shownPath = displayPath(path, cwd)
	}
	switch name {
	case "Bash":
		return str(input, "command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		return shownPath
	case "Grep", "Glob":
		if shownPath != "" {
			return str(input, "pattern") + "  in " + shownPath
		}
		return str(input, "pattern")
	case "WebFetch":
		return str(input, "url")
	case "WebSearch":
		return str(input, "query")
	}
	return firstNonEmpty(str(input, "description"), str(input, "command"), str(input, "pattern"), shownPath, str(input, "url"), str(input, "query"))
}

// ToolErrorText returns the message of a failed tool result, or "" when the block is not a failure.
func ToolErrorText(b ContentBlock) string {
	if b.Type != "tool_result" || !b.IsError {
		return ""
	}
	var text string
	if err := json.Unmarshal(b.Content, &text); err == nil {
		return firstNonEmpty(text, "tool failed")
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(b.Content, &parts)
	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = p.Text
	}
	return firstNonEmpty(strings.TrimSpace(strings.Join(texts, "\n")), "tool failed")
}
