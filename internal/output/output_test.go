package output

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/golden"
	"github.com/leodnlv/vteam/internal/model"
)

func TestParseGolden(t *testing.T) {
	var cases []struct {
		Name, Input string
		OK          bool
		Expected    any
	}
	golden.Load(t, "agent-output", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			out, err := Parse(c.Input)
			if !c.OK {
				if err == nil {
					t.Fatalf("Parse succeeded, want error; got %+v", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			data, _ := json.Marshal(out)
			var got any
			_ = json.Unmarshal(data, &got)
			if diff := cmp.Diff(c.Expected, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func mustParse(t *testing.T, text string) model.AgentOutput {
	t.Helper()
	out, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return out
}

type obj = map[string]any

func taskContent(body obj) obj {
	return obj{"status": "completed", "summary": "Found issue.", "content": obj{"type": "task", "body": body}}
}

func TestParseValidShapes(t *testing.T) {
	t.Run("parses minimal output (status + summary only)", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "completed", "summary": "No issues found."}))
		want := model.AgentOutput{Status: model.OutputCompleted, Summary: "No issues found."}
		if diff := cmp.Diff(want, out); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("parses generic content", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "completed", "summary": "Review complete.",
			"content": obj{"type": "generic", "body": "Looks good overall."}}))
		if diff := cmp.Diff(&model.Content{Type: model.ContentGeneric, Generic: "Looks good overall."}, out.Content); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("parses task content with all fields", func(t *testing.T) {
		out := mustParse(t, mustJSON(taskContent(obj{
			"title": "Missing null check", "severity": "high", "description": "Potential NPE in auth module",
			"suggestedFix": "Add null check before accessing user.email", "files": []string{"src/auth.ts:45"},
		})))
		want := &model.TaskContentBody{Title: "Missing null check", Severity: model.SeverityHigh,
			Description: "Potential NPE in auth module", SuggestedFix: "Add null check before accessing user.email",
			Files: []string{"src/auth.ts:45"}}
		if out.Content == nil || out.Content.Type != model.ContentTask {
			t.Fatalf("content = %+v", out.Content)
		}
		if diff := cmp.Diff(want, out.Content.Task); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("parses task content without optional suggestedFix", func(t *testing.T) {
		out := mustParse(t, mustJSON(taskContent(obj{
			"title": "Unused import", "severity": "low", "description": "Dead import in utils", "files": []string{"src/utils.ts:1"},
		})))
		if out.Content.Task.SuggestedFix != "" {
			t.Errorf("suggestedFix = %q", out.Content.Task.SuggestedFix)
		}
	})

	t.Run("parses output with filesChanged and commitMessage", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "completed", "summary": "Applied fix.",
			"filesChanged":  []string{"src/auth.ts", "src/utils.ts"},
			"commitMessage": obj{"subject": "vteam: fix null check in auth", "body": "Adds null check to prevent NPE."}}))
		if diff := cmp.Diff([]string{"src/auth.ts", "src/utils.ts"}, out.FilesChanged); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff(&model.CommitMessage{Subject: "vteam: fix null check in auth", Body: "Adds null check to prevent NPE."}, out.CommitMessage); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("parses output with memoryUpdate", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "completed", "summary": "Scanned auth module.",
			"memoryUpdate": "Scanned src/auth.ts — found 2 issues. Config uses singleton."}))
		if out.MemoryUpdate != "Scanned src/auth.ts — found 2 issues. Config uses singleton." {
			t.Errorf("memoryUpdate = %q", out.MemoryUpdate)
		}
	})

	t.Run("parses blocked status with blockerReason", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "blocked", "summary": "Cannot proceed.",
			"blockerReason": "Tests are failing before any changes."}))
		if out.Status != model.OutputBlocked || out.BlockerReason != "Tests are failing before any changes." {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("parses failed status", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "failed", "summary": "Could not complete the task.", "blockerReason": "File not found."}))
		if out.Status != model.OutputFailed || out.BlockerReason != "File not found." {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("parses partial status with all optional fields", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "partial", "summary": "Fixed 2 of 3 issues.",
			"content":       obj{"type": "generic", "body": "Partial results."},
			"filesChanged":  []string{"src/auth.ts"},
			"commitMessage": obj{"subject": "vteam: partial fix", "body": "Fixed 2 of 3."},
			"memoryUpdate":  "Partially fixed auth module.",
			"blockerReason": "Third issue needs manual review.",
			"threadReplies": []obj{{"thread": "T1", "body": "Renamed."}}}))
		if out.Status != model.OutputPartial || out.Content == nil || out.FilesChanged == nil ||
			out.CommitMessage == nil || out.MemoryUpdate == "" || out.BlockerReason == "" || len(out.ThreadReplies) != 1 {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("accepts all four severity levels in task content", func(t *testing.T) {
		for _, severity := range []model.Severity{"critical", "high", "medium", "low"} {
			out := mustParse(t, mustJSON(taskContent(obj{"title": "Issue", "severity": severity, "description": "desc", "files": []string{"a.ts"}})))
			if out.Content.Task.Severity != severity {
				t.Errorf("severity = %q, want %q", out.Content.Task.Severity, severity)
			}
		}
	})

	t.Run("ignores unknown keys", func(t *testing.T) {
		out := mustParse(t, mustJSON(obj{"status": "completed", "summary": "s", "extra": obj{"x": 1}}))
		if out.Summary != "s" {
			t.Errorf("got %+v", out)
		}
	})
}

func TestParseMarkdownFences(t *testing.T) {
	done := mustJSON(obj{"status": "completed", "summary": "Done."})

	t.Run("strips ```json fences", func(t *testing.T) {
		if out := mustParse(t, "```json\n"+done+"\n```"); out.Status != model.OutputCompleted {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("strips plain ``` fences", func(t *testing.T) {
		if out := mustParse(t, "```\n"+done+"\n```"); out.Status != model.OutputCompleted {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("handles triple backticks inside JSON string values", func(t *testing.T) {
		text := mustJSON(taskContent(obj{"title": "SQL bug", "severity": "high", "description": "Broken query",
			"suggestedFix": "Use LEFT JOIN:\n\n```sql\nSELECT * FROM foo\n```\n\nApply to all files.", "files": []string{"src/repo.ts:42"}}))
		out := mustParse(t, "```json\n"+text+"\n```")
		if !strings.Contains(out.Content.Task.SuggestedFix, "```sql") || out.Content.Task.Title != "SQL bug" {
			t.Errorf("got %+v", out.Content.Task)
		}
	})

	t.Run("handles triple backticks inside JSON string values without outer fence", func(t *testing.T) {
		text := mustJSON(taskContent(obj{"title": "URL encoding", "severity": "medium", "description": "Broken query",
			"suggestedFix": "Use encodeURIComponent:\n\n```js\n// line 534\nconst url = encodeURIComponent(domain);\n```\n\nThis prevents path traversal.",
			"files":        []string{"index.html:534"}}))
		out := mustParse(t, text)
		if !strings.Contains(out.Content.Task.SuggestedFix, "```js") || out.Content.Task.Title != "URL encoding" {
			t.Errorf("got %+v", out.Content.Task)
		}
	})

	t.Run("extracts JSON from text with leading prose", func(t *testing.T) {
		if out := mustParse(t, "Here is my output:\n\n"+done); out.Status != model.OutputCompleted {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("skips curly braces in prose before the actual JSON", func(t *testing.T) {
		text := mustJSON(obj{"status": "completed", "summary": "Reviewed MR #9161.", "content": obj{"type": "generic", "body": "One finding posted."}})
		prose := "Review posted. The URL `/platform/{handle}/workspaces/{id}` is broken.\n\n"
		if out := mustParse(t, prose+text); out.Summary != "Reviewed MR #9161." {
			t.Errorf("got %+v", out)
		}
	})
}

func TestParseRejectsInvalidOutput(t *testing.T) {
	cases := map[string]string{
		"throws on non-JSON text":                       "not json at all",
		"throws on missing summary":                     mustJSON(obj{"status": "completed"}),
		"throws on missing status":                      mustJSON(obj{"summary": "Done."}),
		"throws on invalid status value":                mustJSON(obj{"status": "unknown", "summary": "X"}),
		"throws on invalid severity in task content":    mustJSON(taskContent(obj{"title": "Bug", "severity": "urgent", "description": "Bad", "files": []string{"a.ts"}})),
		"throws on task content with empty files array": mustJSON(taskContent(obj{"title": "Bug", "severity": "high", "description": "Bad", "files": []string{}})),
		"throws on unknown content type":                mustJSON(obj{"status": "completed", "summary": "Done", "content": obj{"type": "unknown", "body": "x"}}),
		"throws on task content with missing title":     mustJSON(taskContent(obj{"severity": "high", "description": "Bad", "files": []string{"a.ts"}})),
		"throws on commitMessage with empty subject":    mustJSON(obj{"status": "completed", "summary": "Fixed", "commitMessage": obj{"subject": "", "body": "desc"}}),
		"throws on null for an optional field":          mustJSON(obj{"status": "completed", "summary": "s", "content": nil}),
		"throws on generic content without a body":      mustJSON(obj{"status": "completed", "summary": "s", "content": obj{"type": "generic"}}),
		"throws on commitMessage without a body":        mustJSON(obj{"status": "completed", "summary": "s", "commitMessage": obj{"subject": "x"}}),
		"throws on a non-string file":                   mustJSON(taskContent(obj{"title": "Bug", "severity": "high", "description": "Bad", "files": []any{1}})),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if out, err := Parse(text); err == nil {
				t.Errorf("Parse succeeded: %+v", out)
			}
		})
	}
}

func TestContentRoundTrip(t *testing.T) {
	for _, c := range []model.Content{
		{Type: model.ContentGeneric, Generic: "text"},
		{Type: model.ContentTask, Task: &model.TaskContentBody{Title: "t", Severity: "low", Description: "d", Files: []string{"a"}}},
	} {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var back model.Content
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(c, back); diff != "" {
			t.Error(diff)
		}
	}
}

func TestParseRejectsWrongShapes(t *testing.T) {
	base := func(k string, v any) string { return mustJSON(obj{"status": "completed", "summary": "s", k: v}) }
	for name, text := range map[string]string{
		"content not an object":        base("content", "x"),
		"task body not an object":      base("content", obj{"type": "task", "body": "x"}),
		"task body missing":            base("content", obj{"type": "task"}),
		"commitMessage not an object":  base("commitMessage", "x"),
		"threadReplies not a list":     base("threadReplies", "x"),
		"thread reply not an object":   base("threadReplies", []any{"x"}),
		"filesChanged not a list":      base("filesChanged", "a.ts"),
		"filesChanged with a number":   base("filesChanged", []any{1}),
		"summary not a string":         mustJSON(obj{"status": "completed", "summary": 1}),
		"files missing from task body": base("content", obj{"type": "task", "body": obj{"title": "t", "severity": "low", "description": "d"}}),
		"suggestedFix not a string":    base("content", obj{"type": "task", "body": obj{"title": "t", "severity": "low", "description": "d", "files": []string{"a"}, "suggestedFix": 1}}),
		"braces with no closing":       "{ not closed",
		"fence around invalid json":    "```json\n{nope}\n```",
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := Parse(text); err == nil {
				t.Errorf("Parse succeeded: %+v", out)
			}
		})
	}
}

func TestParseFindsJSONAfterAFenceWithoutJSON(t *testing.T) {
	text := "```\nno json here\n```\n" + mustJSON(obj{"status": "completed", "summary": "after"})
	if out := mustParse(t, text); out.Summary != "after" {
		t.Errorf("got %+v", out)
	}
}

func TestParseInteractiveTaskList(t *testing.T) {
	const doc = `{"status":"completed","summary":"two findings","content":{"type":"tasks","body":[
		{"title":"A","severity":"high","description":"a","files":["a.go"]},
		{"title":"B","severity":"low","description":"b","suggestedFix":"fix b","files":["b.go"]}]}}`
	out, err := ParseInteractive(doc)
	if err != nil {
		t.Fatalf("ParseInteractive: %v", err)
	}
	want := []model.TaskContentBody{
		{Title: "A", Severity: model.SeverityHigh, Description: "a", Files: []string{"a.go"}},
		{Title: "B", Severity: model.SeverityLow, Description: "b", SuggestedFix: "fix b", Files: []string{"b.go"}},
	}
	if diff := cmp.Diff(want, out.Content.Findings()); diff != "" {
		t.Errorf("findings (-want +got):\n%s", diff)
	}
	data, _ := json.Marshal(out.Content)
	var back model.Content
	if err := json.Unmarshal(data, &back); err != nil || len(back.Tasks) != 2 {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

func TestParseRejectsTaskListOutsideInteractive(t *testing.T) {
	const doc = `{"status":"completed","summary":"s","content":{"type":"tasks","body":[{"title":"A","severity":"high","description":"a","files":["a.go"]}]}}`
	if _, err := Parse(doc); err == nil || !strings.Contains(err.Error(), `expected "task" or "generic"`) {
		t.Errorf("Parse error = %v", err)
	}
}

func TestParseInteractiveRejectsBadTaskLists(t *testing.T) {
	cases := map[string]string{
		`"body":[]`: "content.body: expected at least 1 item",
		`"body":{}`: "content.body: expected array",
		`"body":[{"title":"","severity":"high","description":"a","files":["a"]}]`: "content.body[0].title",
	}
	for body, want := range cases {
		doc := `{"status":"completed","summary":"s","content":{"type":"tasks",` + body + `}}`
		if _, err := ParseInteractive(doc); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", body, err, want)
		}
	}
}
