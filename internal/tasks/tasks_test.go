package tasks

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/golden"
	"github.com/leodnlv/vteam/internal/model"
)

func filterFromGolden(raw map[string]any) config.TaskFilter {
	f := config.TaskFilter{}
	for k, v := range raw {
		items, isList := v.([]any)
		if !isList {
			items = []any{v}
		}
		for _, item := range items {
			s, _ := item.(string)
			f[k] = append(f[k], s)
		}
	}
	return f
}

func TestFilterAndPickGolden(t *testing.T) {
	var g struct {
		Tasks      []model.Task
		MaxRetries int
		Cases      []struct {
			Filter  map[string]any
			Matches []string
			Next    *string
		}
	}
	golden.Load(t, "task-filter", &g)
	for _, c := range g.Cases {
		filter := filterFromGolden(c.Filter)
		matches := []string{}
		for _, task := range g.Tasks {
			if MatchesFilter(task, filter) {
				matches = append(matches, task.Ref)
			}
		}
		if diff := cmp.Diff(c.Matches, matches); diff != "" {
			t.Errorf("filter %v matches (-want +got):\n%s", c.Filter, diff)
		}
		next, ok := PickNext(g.Tasks, filter, g.MaxRetries)
		if (c.Next == nil) == ok || (ok && next.Ref != *c.Next) {
			t.Errorf("filter %v picked %q (ok=%v), want %v", c.Filter, next.Ref, ok, c.Next)
		}
	}
}

func TestDescriptionGolden(t *testing.T) {
	var cases []struct {
		Body     model.TaskContentBody
		Expected string
	}
	golden.Load(t, "task-description", &cases)
	for _, c := range cases {
		if diff := cmp.Diff(c.Expected, FormatDescription(c.Body)); diff != "" {
			t.Error(diff)
		}
	}
}

func task(meta map[string]string) model.Task {
	return model.Task{Ref: "APP-0001", Title: "T", Status: model.StatusTodo, Severity: model.SeverityLow, Metadata: meta}
}

func TestMatchesFilter(t *testing.T) {
	t.Run("matches an exact value", func(t *testing.T) {
		f := config.TaskFilter{"found-by": {"improver"}}
		if !MatchesFilter(task(map[string]string{"found-by": "improver"}), f) || MatchesFilter(task(map[string]string{"found-by": "human"}), f) {
			t.Error("wrong match")
		}
	})
	t.Run("ORs values within a list", func(t *testing.T) {
		f := config.TaskFilter{"found-by": {"improver", "security-analyst"}}
		if !MatchesFilter(task(map[string]string{"found-by": "security-analyst"}), f) || MatchesFilter(task(nil), f) {
			t.Error("wrong match")
		}
	})
	t.Run("ANDs different keys", func(t *testing.T) {
		f := config.TaskFilter{"found-by": {"improver"}, "actor": {"coder"}}
		if !MatchesFilter(task(map[string]string{"found-by": "improver", "actor": "coder"}), f) || MatchesFilter(task(map[string]string{"found-by": "improver"}), f) {
			t.Error("wrong match")
		}
	})
	t.Run("treats null as a missing or empty key", func(t *testing.T) {
		if !MatchesFilter(task(nil), config.TaskFilter{"actor": {""}}) ||
			!MatchesFilter(task(map[string]string{"actor": ""}), config.TaskFilter{"actor": {""}}) ||
			MatchesFilter(task(map[string]string{"actor": "x"}), config.TaskFilter{"actor": {"", "y"}}) {
			t.Error("wrong match")
		}
	})
	t.Run("matches everything with an empty filter", func(t *testing.T) {
		if !MatchesFilter(task(map[string]string{"any": "thing"}), nil) {
			t.Error("no match")
		}
	})
}

func TestPickNext(t *testing.T) {
	make := func(ref string, sev model.Severity, created string, retries int, meta map[string]string) model.Task {
		return model.Task{Ref: ref, Severity: sev, CreatedAt: created, RetryCount: retries, Metadata: meta}
	}
	t.Run("picks the highest severity, then the oldest", func(t *testing.T) {
		ready := []model.Task{
			make("LOW", "low", "2026-01-01", 0, nil),
			make("NEW-HIGH", "high", "2026-03-01", 0, nil),
			make("OLD-HIGH", "high", "2026-02-01", 0, nil),
		}
		if got, _ := PickNext(ready, nil, 3); got.Ref != "OLD-HIGH" {
			t.Errorf("picked %q", got.Ref)
		}
	})
	t.Run("skips tasks outside the filter or at max retries", func(t *testing.T) {
		ready := []model.Task{
			make("OTHER", "critical", "", 0, map[string]string{"found-by": "human"}),
			make("SPENT", "critical", "", 3, map[string]string{"found-by": "improver"}),
			make("OK", "low", "", 0, map[string]string{"found-by": "improver"}),
		}
		if got, _ := PickNext(ready, config.TaskFilter{"found-by": {"improver"}}, 3); got.Ref != "OK" {
			t.Errorf("picked %q", got.Ref)
		}
	})
	t.Run("returns false when nothing qualifies", func(t *testing.T) {
		if _, ok := PickNext(nil, nil, 3); ok {
			t.Error("picked a task")
		}
	})
}

func TestToAvenLabel(t *testing.T) {
	var cases []struct{ Input, Expected string }
	golden.Load(t, "aven-label", &cases)
	for _, c := range append(cases, []struct{ Input, Expected string }{
		{"vteam-coder", "vteam-coder"}, {"Foo_Bar", "foo-bar"}, {"a:b", "a-b"},
		{"a  /  b", "a-b"}, {"-a-", "a"}, {"ünï", "n"},
	}...) {
		if got := ToAvenLabel(c.Input); got != c.Expected {
			t.Errorf("ToAvenLabel(%q) = %q, want %q", c.Input, got, c.Expected)
		}
	}
}

func TestLabelConventions(t *testing.T) {
	cfg := config.Tasks{MaxRetries: 3, Project: "app", Label: "vteam"}
	if got := AgentLabel(cfg, "coder"); got != "vteam-coder" {
		t.Errorf("AgentLabel = %q", got)
	}
	if got := AgentLabel(config.Tasks{Label: "Bot"}, "Security_Analyst"); got != "bot-security-analyst" {
		t.Errorf("AgentLabel = %q", got)
	}
	if diff := cmp.Diff([]string{"vteam-improver"}, LabelsOnCreate(cfg, "improver")); diff != "" {
		t.Error(diff)
	}
	if diff := cmp.Diff([]string{"vteam-coder"}, LabelsOnDone(cfg, "coder")); diff != "" {
		t.Error(diff)
	}
}

func TestCandidates(t *testing.T) {
	ready := []model.Task{
		{Ref: "LOW", Severity: "low", CreatedAt: "2026-01-01"},
		{Ref: "OTHER", Severity: "critical", Metadata: map[string]string{"found-by": "human"}},
		{Ref: "SPENT", Severity: "high", CreatedAt: "2026-01-01", RetryCount: 3},
		{Ref: "HIGH", Severity: "high", CreatedAt: "2026-02-01"},
	}
	var refs []string
	for _, c := range Candidates(ready, config.TaskFilter{"found-by": {""}}) {
		refs = append(refs, c.Ref)
	}
	if want := []string{"SPENT", "HIGH", "LOW"}; !slices.Equal(refs, want) {
		t.Errorf("candidates = %v, want %v", refs, want)
	}
}
