// Package taskstest provides an in-memory tasks.Manager for tests.
package taskstest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/tasks"
)

type Created struct {
	Ref     string
	Body    model.TaskContentBody
	FoundBy string
	Labels  []string
}

type UpdateCall struct {
	Ref    string
	Update tasks.Update
}

type Note struct {
	Ref, Text string
}

// Fake keeps tasks in memory and records every call. Updates change the stored tasks like aven would.
type Fake struct {
	mu      sync.Mutex
	tasks   []model.Task
	created []Created
	updates []UpdateCall
	notes   []Note

	FailNotes  bool
	FailCreate bool
	FailUpdate func(ref string, u tasks.Update) error
	// OnListReady runs before ListReady returns, to widen race windows in claim tests.
	OnListReady func()
}

func New(initial ...model.Task) *Fake {
	f := &Fake{}
	for _, t := range initial {
		f.Add(t)
	}
	return f
}

func (f *Fake) Add(t model.Task) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.Metadata == nil {
		t.Metadata = map[string]string{}
	}
	f.tasks = append(f.tasks, t)
}

func clone(t model.Task) model.Task {
	t.Metadata = maps.Clone(t.Metadata)
	t.Notes = slices.Clone(t.Notes)
	return t
}

func (f *Fake) filter(keep func(model.Task) bool) []model.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.Task{}
	for _, t := range f.tasks {
		if keep(t) {
			out = append(out, clone(t))
		}
	}
	return out
}

func (f *Fake) ListReady(context.Context) ([]model.Task, error) {
	ready := f.filter(func(t model.Task) bool { return t.Status == model.StatusTodo })
	if f.OnListReady != nil {
		f.OnListReady()
	}
	return ready, nil
}

func (f *Fake) ListDone(context.Context) ([]model.Task, error) {
	return f.filter(func(t model.Task) bool { return t.Status == model.StatusDone }), nil
}

func (f *Fake) GetIndex(context.Context) (tasks.Index, error) {
	return tasks.NewIndex(f.filter(func(model.Task) bool { return true })), nil
}

func (f *Fake) Create(_ context.Context, body model.TaskContentBody, foundBy string, labels []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailCreate {
		return "", errors.New("aven add failed")
	}
	ref := fmt.Sprintf("VTM-%d", len(f.created)+1)
	f.created = append(f.created, Created{Ref: ref, Body: body, FoundBy: foundBy, Labels: labels})
	f.tasks = append(f.tasks, model.Task{
		Ref: ref, Title: body.Title, Status: model.StatusInbox, Severity: body.Severity,
		Metadata: map[string]string{model.MetaFoundBy: foundBy}, Description: tasks.FormatDescription(body),
	})
	return ref, nil
}

func (f *Fake) Update(_ context.Context, ref string, u tasks.Update) error {
	if f.FailUpdate != nil {
		if err := f.FailUpdate(ref, u); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, UpdateCall{Ref: ref, Update: u})
	for i := range f.tasks {
		t := &f.tasks[i]
		if t.Ref != ref {
			continue
		}
		if u.Status != "" {
			t.Status = u.Status
		}
		for k, v := range u.Metadata {
			if v != "" {
				t.Metadata[k] = v
			}
		}
		for _, k := range u.RemoveMetadata {
			delete(t.Metadata, k)
		}
	}
	return nil
}

func (f *Fake) AddNote(_ context.Context, ref, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNotes {
		return errors.New("aven note failed")
	}
	f.notes = append(f.notes, Note{Ref: ref, Text: text})
	for i := range f.tasks {
		if f.tasks[i].Ref == ref {
			f.tasks[i].Notes = append(f.tasks[i].Notes, model.TaskNote{Body: text})
		}
	}
	return nil
}

func (f *Fake) Task(ref string) model.Task {
	for _, t := range f.filter(func(t model.Task) bool { return t.Ref == ref }) {
		return t
	}
	return model.Task{}
}

func (f *Fake) Created() []Created {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.created)
}

func (f *Fake) Updates() []UpdateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.updates)
}

func (f *Fake) Notes() []Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.notes)
}

var _ tasks.Manager = (*Fake)(nil)
