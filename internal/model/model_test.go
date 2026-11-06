package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestContentMarshalGeneric(t *testing.T) {
	got, err := json.Marshal(Content{Type: ContentGeneric, Generic: "LGTM"})

	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"generic","body":"LGTM"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestContentMarshalTask(t *testing.T) {
	content := Content{Type: ContentTask, Task: &TaskContentBody{
		Title:       "Race in lock release",
		Severity:    SeverityHigh,
		Description: "Release can remove another process's lock",
		Files:       []string{"internal/lock/lock.go"},
	}}

	got, err := json.Marshal(content)

	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"task","body":{"title":"Race in lock release","severity":"high","description":"Release can remove another process's lock","files":["internal/lock/lock.go"]}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestContentUnmarshalGeneric(t *testing.T) {
	var got Content

	err := json.Unmarshal([]byte(`{"type":"generic","body":"Looks good"}`), &got)

	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(Content{Type: ContentGeneric, Generic: "Looks good"}, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestContentUnmarshalTask(t *testing.T) {
	var got Content
	raw := `{"type":"task","body":{"title":"Leak","severity":"low","description":"d","suggestedFix":"close it","files":["a.go","b.go"]}}`

	err := json.Unmarshal([]byte(raw), &got)

	if err != nil {
		t.Fatal(err)
	}
	want := Content{Type: ContentTask, Task: &TaskContentBody{
		Title:        "Leak",
		Severity:     SeverityLow,
		Description:  "d",
		SuggestedFix: "close it",
		Files:        []string{"a.go", "b.go"},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestContentRoundTrip(t *testing.T) {
	cases := map[string]Content{
		"generic": {Type: ContentGeneric, Generic: "report"},
		"task": {Type: ContentTask, Task: &TaskContentBody{
			Title: "t", Severity: SeverityMedium, Description: "d", Files: []string{"x.go"},
		}},
	}

	for name, original := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}

			var decoded Content
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(original, decoded); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestContentUnmarshalErrors(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"unknown type", `{"type":"review","body":"x"}`, `content.type: expected "task" or "generic", got "review"`},
		{"missing type", `{"body":"x"}`, `content.type: expected "task" or "generic", got ""`},
		{"generic body is not a string", `{"type":"generic","body":{"a":1}}`, "content.body:"},
		{"task body is not an object", `{"type":"task","body":"just text"}`, "content.body:"},
		{"task body missing", `{"type":"task"}`, "content.body:"},
		{"not an object", `"nope"`, "cannot unmarshal"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got Content

			err := json.Unmarshal([]byte(c.raw), &got)

			if err == nil {
				t.Fatalf("expected an error, got %+v", got)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not contain %q", err, c.wantErr)
			}
		})
	}
}
