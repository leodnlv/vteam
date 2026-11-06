package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestSeverityValid(t *testing.T) {
	tests := []struct {
		severity Severity
		want     bool
	}{
		{SeverityCritical, true},
		{SeverityHigh, true},
		{SeverityMedium, true},
		{SeverityLow, true},
		{Severity(""), false},
		{Severity("urgent"), false},
		{Severity("HIGH"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			if got := tt.severity.Valid(); got != tt.want {
				t.Errorf("Severity(%q).Valid() = %v, want %v", tt.severity, got, tt.want)
			}
		})
	}
}

func TestSeverityPriority(t *testing.T) {
	tests := []struct {
		severity Severity
		want     int
	}{
		{SeverityCritical, 0},
		{SeverityHigh, 1},
		{SeverityMedium, 2},
		{SeverityLow, 3},
		{Severity("unknown"), 3},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			if got := tt.severity.Priority(); got != tt.want {
				t.Errorf("Severity(%q).Priority() = %d, want %d", tt.severity, got, tt.want)
			}
		})
	}
}

func TestISOTimeFormatsUTCWithMilliseconds(t *testing.T) {
	ts := time.Date(2026, 11, 6, 13, 59, 52, 123_000_000, time.UTC)

	if got, want := ISOTime(ts), "2026-11-06T13:59:52.123Z"; got != want {
		t.Errorf("ISOTime() = %q, want %q", got, want)
	}
}

func TestISOTimeConvertsLocalTimeToUTC(t *testing.T) {
	ts := time.Date(2026, 11, 6, 15, 0, 0, 0, time.FixedZone("CET", 3600))

	if got, want := ISOTime(ts), "2026-11-06T14:00:00.000Z"; got != want {
		t.Errorf("ISOTime() = %q, want %q", got, want)
	}
}

func TestISOTimeTruncatesSubMillisecondPrecision(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 987_654_321, time.UTC)

	if got, want := ISOTime(ts), "2026-01-02T03:04:05.987Z"; got != want {
		t.Errorf("ISOTime() = %q, want %q", got, want)
	}
}

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
