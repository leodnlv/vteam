// Package model holds the types shared across vteam packages.
package model

import (
	"encoding/json"
	"fmt"
	"time"
)

type TaskStatus string

const (
	StatusInbox    TaskStatus = "inbox"
	StatusBacklog  TaskStatus = "backlog"
	StatusTodo     TaskStatus = "todo"
	StatusActive   TaskStatus = "active"
	StatusDone     TaskStatus = "done"
	StatusCanceled TaskStatus = "canceled"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	}
	return false
}

func (s Severity) Priority() int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	default:
		return 3
	}
}

type Platform string

const (
	GitHub Platform = "github"
	GitLab Platform = "gitlab"
)

const (
	MetaFoundBy     = "found-by"
	MetaRetryCount  = "retry-count"
	MetaCompleted   = "completed"
	MetaBranch      = "branch"
	MetaPRURL       = "pr-url"
	MetaMerged      = "merged"
	MetaClaimedBy   = "claimed-by"
	MetaClaimedPID  = "claimed-pid"
	MetaClaimedHost = "claimed-host"
	// MetaClaimedSession marks a claim held by an interactive session rather than a live process.
	MetaClaimedSession = "claimed-session"
)

type TaskNote struct {
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

type Task struct {
	Ref         string            `json:"ref"`
	Title       string            `json:"title"`
	Status      TaskStatus        `json:"status"`
	Severity    Severity          `json:"severity"`
	RetryCount  int               `json:"retryCount"`
	Metadata    map[string]string `json:"metadata"`
	Description string            `json:"description"`
	Notes       []TaskNote        `json:"notes"`
	CreatedAt   string            `json:"createdAt"`
}

type ReviewComment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type ReviewablePR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
	URL    string `json:"url"`
}

type PRReviewContext struct {
	PR       ReviewablePR    `json:"pr"`
	Comments []ReviewComment `json:"comments"`
	RepoSlug string          `json:"repoSlug"`
}

type PRState string

const (
	PROpen   PRState = "open"
	PRClosed PRState = "closed"
	PRMerged PRState = "merged"
)

type PRMergeability string

const (
	Mergeable           PRMergeability = "mergeable"
	Conflicting         PRMergeability = "conflicting"
	MergeabilityUnknown PRMergeability = "unknown"
)

type PRReviewStatus string

const (
	ReviewApproved         PRReviewStatus = "approved"
	ReviewChangesRequested PRReviewStatus = "changes_requested"
	ReviewRequired         PRReviewStatus = "review_required"
	ReviewNone             PRReviewStatus = "none"
)

type PullRequestInfo struct {
	ReviewablePR
	State      PRState        `json:"state"`
	BaseBranch string         `json:"baseBranch"`
	Labels     []string       `json:"labels"`
	Mergeable  PRMergeability `json:"mergeable"`
	Review     PRReviewStatus `json:"review"`
}

type ReviewThread struct {
	ID       string          `json:"id"`
	Path     string          `json:"path,omitempty"`
	Line     int             `json:"line,omitempty"`
	Comments []ReviewComment `json:"comments"`
}

type BaseBranchMerge struct {
	BaseBranch      string   `json:"baseBranch"`
	ConflictedFiles []string `json:"conflictedFiles"`
}

type PRFollowUpContext struct {
	PR        PullRequestInfo  `json:"pr"`
	Threads   []ReviewThread   `json:"threads"`
	BaseMerge *BaseBranchMerge `json:"baseMerge,omitempty"`
}

type CommitMessage struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type TaskContentBody struct {
	Title        string   `json:"title"`
	Severity     Severity `json:"severity"`
	Description  string   `json:"description"`
	SuggestedFix string   `json:"suggestedFix,omitempty"`
	Files        []string `json:"files"`
}

type ContentType string

const (
	ContentTask    ContentType = "task"
	ContentGeneric ContentType = "generic"
	// ContentTasks is a list of findings. Only interactive runs are offered it.
	ContentTasks ContentType = "tasks"
)

// Content is the agent's primary deliverable: a task finding, a list of them, or a generic string.
type Content struct {
	Type    ContentType
	Task    *TaskContentBody
	Tasks   []TaskContentBody
	Generic string
}

// Findings returns the task findings the content carries, none for generic content.
func (c Content) Findings() []TaskContentBody {
	switch c.Type {
	case ContentTask:
		return []TaskContentBody{*c.Task}
	case ContentTasks:
		return c.Tasks
	}
	return nil
}

func (c Content) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case ContentTask:
		return json.Marshal(struct {
			Type ContentType      `json:"type"`
			Body *TaskContentBody `json:"body"`
		}{c.Type, c.Task})
	case ContentTasks:
		return json.Marshal(struct {
			Type ContentType       `json:"type"`
			Body []TaskContentBody `json:"body"`
		}{c.Type, c.Tasks})
	}
	return json.Marshal(struct {
		Type ContentType `json:"type"`
		Body string      `json:"body"`
	}{c.Type, c.Generic})
}

func (c *Content) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type ContentType     `json:"type"`
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch raw.Type {
	case ContentTask:
		var body TaskContentBody
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return fmt.Errorf("content.body: %w", err)
		}
		*c = Content{Type: raw.Type, Task: &body}
	case ContentTasks:
		var body []TaskContentBody
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return fmt.Errorf("content.body: %w", err)
		}
		*c = Content{Type: raw.Type, Tasks: body}
	case ContentGeneric:
		var body string
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return fmt.Errorf("content.body: %w", err)
		}
		*c = Content{Type: raw.Type, Generic: body}
	default:
		return fmt.Errorf("content.type: expected \"task\", \"tasks\" or \"generic\", got %q", raw.Type)
	}
	return nil
}

type OutputStatus string

const (
	OutputCompleted OutputStatus = "completed"
	OutputPartial   OutputStatus = "partial"
	OutputBlocked   OutputStatus = "blocked"
	OutputFailed    OutputStatus = "failed"
)

type ThreadReply struct {
	Thread string `json:"thread"`
	Body   string `json:"body"`
}

type AgentOutput struct {
	Status        OutputStatus   `json:"status"`
	Summary       string         `json:"summary"`
	Content       *Content       `json:"content,omitempty"`
	FilesChanged  []string       `json:"filesChanged,omitempty"`
	CommitMessage *CommitMessage `json:"commitMessage,omitempty"`
	BlockerReason string         `json:"blockerReason,omitempty"`
	MemoryUpdate  string         `json:"memoryUpdate,omitempty"`
	ThreadReplies []ThreadReply  `json:"threadReplies,omitempty"`
}

type TaskRef struct {
	Title    string   `json:"title"`
	Severity Severity `json:"severity"`
}

type RunOutcome struct {
	Agent         string         `json:"agent"`
	Status        string         `json:"status"`
	StartedAt     string         `json:"startedAt"`
	CompletedAt   string         `json:"completedAt"`
	Error         string         `json:"error,omitempty"`
	Focus         string         `json:"focus,omitempty"`
	Task          *TaskRef       `json:"task,omitempty"`
	Branch        string         `json:"branch,omitempty"`
	PRURL         string         `json:"prUrl,omitempty"`
	ReviewedPR    *ReviewablePR  `json:"reviewedPR,omitempty"`
	TasksCreated  []string       `json:"tasksCreated,omitempty"`
	CommitMessage *CommitMessage `json:"commitMessage,omitempty"`
	Content       *Content       `json:"content,omitempty"`
}

type RunStatus string

const (
	RunStarted       RunStatus = "started"
	RunClaudeRunning RunStatus = "claude_running"
	RunProcessing    RunStatus = "processing"
	RunCompleted     RunStatus = "completed"
	RunFailed        RunStatus = "failed"
	RunStopped       RunStatus = "stopped"
	// RunInteractive is a run handed off to an interactive session, settled later by `vteam submit`.
	RunInteractive RunStatus = "interactive"
)

type RunState struct {
	RunID         string         `json:"runId"`
	Agent         string         `json:"agent"`
	Status        RunStatus      `json:"status"`
	StartedAt     string         `json:"startedAt"`
	CompletedAt   string         `json:"completedAt,omitempty"`
	WorktreePath  string         `json:"worktreePath,omitempty"`
	BranchName    string         `json:"branchName,omitempty"`
	TaskRef       string         `json:"taskRef,omitempty"`
	Error         string         `json:"error,omitempty"`
	ClaudeOutput  *AgentOutput   `json:"claudeOutput,omitempty"`
	TasksCreated  []string       `json:"tasksCreated,omitempty"`
	CommitSHA     string         `json:"commitSha,omitempty"`
	CommitMessage *CommitMessage `json:"commitMessage,omitempty"`
}

// ISOTime formats t like JavaScript's Date.toISOString, the format stored in run state and task metadata.
func ISOTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
