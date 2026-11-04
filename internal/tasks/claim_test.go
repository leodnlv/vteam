package tasks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

func TestClaimMetadata(t *testing.T) {
	want := map[string]string{"claimed-by": "run-1", "claimed-pid": "42", "claimed-host": "box"}
	if diff := cmp.Diff(want, tasks.ClaimMetadata("run-1", 42, "box")); diff != "" {
		t.Error(diff)
	}
}

func active(ref string, meta map[string]string) model.Task {
	return model.Task{Ref: ref, Title: "Task " + ref, Status: model.StatusActive, Severity: "high", Metadata: meta}
}

func claim(pid, host string) map[string]string {
	return map[string]string{"claimed-by": "run", "claimed-pid": pid, "claimed-host": host}
}

func alive(pid int) bool { return pid == 100 }

func refs(ts []model.Task) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Ref)
	}
	return out
}

func TestReleaseOrphaned(t *testing.T) {
	ctx := context.Background()

	t.Run("returns tasks claimed by a dead process on this host to todo", func(t *testing.T) {
		fake := taskstest.New(active("A", claim("200", "box")))
		sweep, err := tasks.ReleaseOrphaned(ctx, fake, alive, "box")
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]string{"A"}, refs(sweep.Released)); diff != "" {
			t.Error(diff)
		}
		want := []taskstest.UpdateCall{{Ref: "A", Update: tasks.Update{Status: model.StatusTodo, RemoveMetadata: tasks.ClaimKeys}}}
		if diff := cmp.Diff(want, fake.Updates()); diff != "" {
			t.Error(diff)
		}
		if fake.Task("A").Status != model.StatusTodo || fake.Task("A").Metadata["claimed-pid"] != "" {
			t.Errorf("task = %+v", fake.Task("A"))
		}
	})

	t.Run("leaves tasks claimed by a live process", func(t *testing.T) {
		fake := taskstest.New(active("A", claim("100", "box")))
		sweep, _ := tasks.ReleaseOrphaned(ctx, fake, alive, "box")
		if len(sweep.Released)+len(sweep.ForeignHost)+len(sweep.Unclaimed) != 0 || len(fake.Updates()) != 0 {
			t.Errorf("sweep = %+v", sweep)
		}
	})

	t.Run("leaves tasks claimed on another host", func(t *testing.T) {
		fake := taskstest.New(active("A", claim("200", "other")))
		sweep, _ := tasks.ReleaseOrphaned(ctx, fake, alive, "box")
		if diff := cmp.Diff([]string{"A"}, refs(sweep.ForeignHost)); diff != "" || len(fake.Updates()) != 0 {
			t.Errorf("sweep = %+v", sweep)
		}
	})

	t.Run("leaves active tasks without a vteam claim", func(t *testing.T) {
		fake := taskstest.New(active("A", nil), active("B", map[string]string{"claimed-pid": "garbage"}))
		sweep, _ := tasks.ReleaseOrphaned(ctx, fake, alive, "box")
		if diff := cmp.Diff([]string{"A", "B"}, refs(sweep.Unclaimed)); diff != "" || len(fake.Updates()) != 0 {
			t.Errorf("sweep = %+v", sweep)
		}
	})

	t.Run("ignores tasks that are not active", func(t *testing.T) {
		todo := active("A", claim("200", "box"))
		todo.Status = model.StatusTodo
		fake := taskstest.New(todo)
		if sweep, _ := tasks.ReleaseOrphaned(ctx, fake, alive, "box"); len(sweep.Released) != 0 {
			t.Errorf("sweep = %+v", sweep)
		}
	})
}

func TestReleaseOrphanedPropagatesErrors(t *testing.T) {
	fake := taskstest.New(active("A", claim("200", "box")))
	fake.FailUpdate = func(string, tasks.Update) error { return errors.New("db locked") }
	if _, err := tasks.ReleaseOrphaned(context.Background(), fake, alive, "box"); err == nil {
		t.Error("update error ignored")
	}
	if tasks.Hostname() == "" {
		t.Error("no hostname")
	}
}
