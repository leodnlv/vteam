package status

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/ansi"
	"github.com/leodnlv/vteam/internal/ensuremerge"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/leodnlv/vteam/internal/tasks"
)

// pipelineData is everything the pipeline sections read. Only recorded timestamps are used: aven keeps no
// status history and the merge ensurer records no merge time, so triage and merge times are never shown.
type pipelineData struct {
	tasks      []model.Task
	ready      []model.Task
	runs       []model.RunState
	sessions   []session.Session
	maxRetries int
}

type stage struct {
	name  string
	glyph string
	paint func(string) string
}

const (
	stageMerged = iota
	stageDone
	stagePROpen
	stageActive
	stageTodo
	stageBacklog
	stageInbox
	stagePRClosed
	stageRejected
)

var stages = []stage{
	stageMerged:   {"merged", "█", ansi.OK},
	stageDone:     {"done", "▓", ansi.OK},
	stagePROpen:   {"PR open", "█", ansi.Cron},
	stageActive:   {"active", "█", ansi.Info},
	stageTodo:     {"todo", "▒", ansi.Info},
	stageBacklog:  {"backlog", "▒", ansi.Muted},
	stageInbox:    {"inbox", "█", ansi.Accent},
	stagePRClosed: {"PR closed", "█", ansi.Fail},
	stageRejected: {"rejected", "░", ansi.Muted},
}

const (
	createdBarWidth  = 40
	activityBarWidth = 16
)

func stageOf(t model.Task) int {
	hasPR := t.Metadata[model.MetaPRURL] != ""
	switch t.Status {
	case model.StatusDone:
		switch {
		case t.Metadata[model.MetaMerged] == "true":
			return stageMerged
		case hasPR:
			return stagePROpen
		}
		return stageDone
	case model.StatusActive:
		return stageActive
	case model.StatusBacklog:
		return stageBacklog
	case model.StatusInbox:
		return stageInbox
	case model.StatusCanceled:
		if hasPR {
			return stagePRClosed
		}
		return stageRejected
	}
	return stageTodo
}

func renderPipeline(p pipelineData, window time.Duration, now time.Time) []string {
	since := now.Add(-window)
	in := func(t time.Time) bool { return t.After(since) && !t.After(now) }
	label := "last " + windowLabel(window)
	lines := renderCreated(p, in, label)
	lines = append(lines, renderActivity(p, since, window, label)...)
	lines = append(lines, renderLeadTime(p, in, label, now)...)
	return append(lines, renderQueue(p)...)
}

func windowLabel(window time.Duration) string {
	if window >= 48*time.Hour && window%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", window/(24*time.Hour))
	}
	return fmt.Sprintf("%dh", int(window.Hours()))
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func origin(t model.Task) string {
	if by := t.Metadata[model.MetaFoundBy]; by != "" {
		return by
	}
	return "by hand"
}

// firstPR is when a run first noted the task's PR, or the task's completed time when no note mentions one.
func firstPR(t model.Task) (time.Time, bool) {
	var first time.Time
	for _, n := range t.Notes {
		if at, ok := parseTime(n.CreatedAt); ok && tasks.HasPRParagraph(n.Body) && (first.IsZero() || at.Before(first)) {
			first = at
		}
	}
	if !first.IsZero() {
		return first, true
	}
	if t.Metadata[model.MetaPRURL] == "" {
		return time.Time{}, false
	}
	return parseTime(t.Metadata[model.MetaCompleted])
}

func renderCreated(p pipelineData, in func(time.Time) bool, label string) []string {
	byOrigin := map[string][]int{}
	total := make([]int, len(stages))
	n := 0
	for _, t := range p.tasks {
		created, ok := parseTime(t.CreatedAt)
		if !ok || !in(created) {
			continue
		}
		o := origin(t)
		if byOrigin[o] == nil {
			byOrigin[o] = make([]int, len(stages))
		}
		s := stageOf(t)
		byOrigin[o][s]++
		total[s]++
		n++
	}
	lines := []string{log.FormatSection(fmt.Sprintf("Tasks created · %s (%d)", label, n))}
	if n == 0 {
		return append(lines, log.FormatListItem(ansi.Muted("·"), ansi.Dim("no tasks created")))
	}

	origins := make([]string, 0, len(byOrigin))
	width := len("all")
	for o := range byOrigin {
		origins = append(origins, o)
		width = max(width, len(o))
	}
	slices.SortFunc(origins, func(a, b string) int {
		return cmp.Or(cmp.Compare(sum(byOrigin[b]), sum(byOrigin[a])), cmp.Compare(a, b))
	})
	row := func(name string, counts []int) string {
		c := sum(counts)
		bar := stackedBar(counts, max(1, int(math.Round(float64(c)/float64(n)*createdBarWidth))))
		return "  " + ansi.Bold(log.PadEnd(name, width)) + fmt.Sprintf("  %4d  ", c) + bar
	}
	if len(origins) > 1 {
		lines = append(lines, row("all", total))
	}
	for _, o := range origins {
		lines = append(lines, row(o, byOrigin[o]))
	}

	var legend []string
	for i, s := range stages {
		if total[i] > 0 {
			legend = append(legend, s.paint(s.glyph)+" "+ansi.Muted(s.name)+" "+ansi.Bold(fmt.Sprint(total[i])))
		}
	}
	lines = append(lines, "")
	indent := strings.Repeat(" ", 2+width+8)
	for _, l := range wrapJoined(legend, "   ", 60) {
		lines = append(lines, indent+l)
	}
	reached := total[stageMerged] + total[stagePROpen] + total[stagePRClosed]
	return append(lines, "  "+ansi.Muted(fmt.Sprintf("%s reached a PR · %s merged", percent(reached, n), percent(total[stageMerged], n))))
}

// stackedBar draws counts side by side in width cells, giving every non-zero count at least one cell when it can.
func stackedBar(counts []int, width int) string {
	total := sum(counts)
	if total == 0 {
		return ""
	}
	cells := make([]int, len(counts))
	acc, prevEnd := 0, 0
	for i, c := range counts {
		acc += c
		end := int(math.Round(float64(acc) / float64(total) * float64(width)))
		cells[i], prevEnd = end-prevEnd, end
	}
	for i, c := range counts {
		if c > 0 && cells[i] == 0 {
			if j := argmax(cells); cells[j] > 1 {
				cells[j]--
				cells[i] = 1
			}
		}
	}
	var b strings.Builder
	for i, n := range cells {
		if n > 0 {
			b.WriteString(stages[i].paint(strings.Repeat(stages[i].glyph, n)))
		}
	}
	return b.String()
}

type activity struct {
	name  string
	times []time.Time
	paint func(string) string
	note  string
}

func renderActivity(p pipelineData, since time.Time, window time.Duration, label string) []string {
	now := since.Add(window)
	in := func(t time.Time) bool { return t.After(since) && !t.After(now) }
	byRef := map[string]model.Task{}
	var created, opened, sentBack, closed, picked, sessions []time.Time
	for _, t := range p.tasks {
		byRef[t.Ref] = t
		if at, ok := parseTime(t.CreatedAt); ok && in(at) {
			created = append(created, at)
		}
		if at, ok := firstPR(t); ok && in(at) {
			opened = append(opened, at)
		}
		for _, note := range t.Notes {
			at, ok := parseTime(note.CreatedAt)
			if !ok || !in(at) {
				continue
			}
			switch {
			case tasks.HasNoteHeadline(note.Body, ensuremerge.Name, "reopened"):
				sentBack = append(sentBack, at)
			case tasks.HasNoteHeadline(note.Body, ensuremerge.Name, "canceled"):
				closed = append(closed, at)
			}
		}
	}
	followUps := 0
	for _, r := range p.runs {
		at, ok := parseTime(r.StartedAt)
		if r.TaskRef == "" || !ok || !in(at) {
			continue
		}
		picked = append(picked, at)
		if pr, ok := firstPR(byRef[r.TaskRef]); ok && pr.Before(at) {
			followUps++
		}
	}
	for _, s := range p.sessions {
		if at, ok := parseTime(s.StartedAt); ok && in(at) {
			sessions = append(sessions, at)
		}
	}
	pickedNote := ""
	if followUps > 0 {
		pickedNote = fmt.Sprintf("%d PR follow-up(s)", followUps)
	}
	rows := []activity{
		{"created", created, ansi.Accent, ""},
		{"picked", picked, ansi.Info, pickedNote},
		{"PR opened", opened, ansi.Cron, ""},
		{"sent back", sentBack, ansi.Warn, "changes requested or conflicting"},
		{"PR closed", closed, ansi.Fail, "closed without merging"},
		{"sessions", sessions, ansi.Accent, "interactive"},
	}
	most := 0
	for _, r := range rows {
		most = max(most, len(r.times))
	}
	buckets := 12
	if window > 48*time.Hour {
		buckets = 14
	}
	lines := []string{log.FormatSection("Activity · " + label)}
	for _, r := range rows {
		line := "  " + ansi.Bold(log.PadEnd(r.name, 10)) + fmt.Sprintf("%4d  ", len(r.times)) +
			r.paint(log.PadEnd(hbar(len(r.times), most, activityBarWidth), activityBarWidth)) + "  " +
			r.paint(spark(bucketize(r.times, since, window, buckets)))
		if r.note != "" {
			line += "  " + ansi.Muted(r.note)
		}
		lines = append(lines, line)
	}
	return lines
}

func bucketize(times []time.Time, since time.Time, window time.Duration, n int) []int {
	out := make([]int, n)
	for _, t := range times {
		i := int(float64(t.Sub(since)) / float64(window) * float64(n))
		out[min(max(i, 0), n-1)]++
	}
	return out
}

const sparkTicks = "▁▂▃▄▅▆▇█"

func spark(values []int) string {
	ticks := []rune(sparkTicks)
	most := slices.Max(values)
	var b strings.Builder
	for _, v := range values {
		if v == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(ticks[int(math.Ceil(float64(v)/float64(most)*float64(len(ticks))))-1])
	}
	return b.String()
}

const barPartials = " ▏▎▍▌▋▊▉"

func hbar(v, most, width int) string {
	if most == 0 || v == 0 {
		return ""
	}
	eighths := int(math.Round(float64(v) / float64(most) * float64(width) * 8))
	bar := strings.Repeat("█", eighths/8)
	if rem := eighths % 8; rem > 0 {
		bar += string([]rune(barPartials)[rem])
	}
	return bar
}

func renderLeadTime(p pipelineData, in func(time.Time) bool, label string, now time.Time) []string {
	var toPR, work, inbox, review []time.Duration
	for _, t := range p.tasks {
		created, createdOK := parseTime(t.CreatedAt)
		pr, prOK := firstPR(t)
		// A PR receipt task (see recordPRTask) is created with its PR already open; it never waited for anything.
		if createdOK && prOK && in(pr) && pr.Sub(created) >= time.Minute {
			toPR = append(toPR, pr.Sub(created))
		}
		switch stageOf(t) {
		case stageInbox:
			if createdOK {
				inbox = append(inbox, now.Sub(created))
			}
		case stagePROpen:
			if prOK {
				review = append(review, now.Sub(pr))
			}
		}
	}
	for _, r := range p.runs {
		start, startOK := parseTime(r.StartedAt)
		end, endOK := parseTime(r.CompletedAt)
		if r.TaskRef != "" && r.CommitSHA != "" && r.Status == model.RunCompleted && startOK && endOK && in(start) {
			work = append(work, end.Sub(start))
		}
	}
	lines := []string{
		log.FormatSection("Lead time"),
		"  " + strings.Repeat(" ", 26) + ansi.Muted(log.PadEnd("median", 10)+log.PadEnd("p90", 10)),
	}
	row := func(name string, ds []time.Duration, unit string) string {
		head := "  " + log.PadEnd(name, 26)
		if len(ds) == 0 {
			return head + ansi.Dim(log.PadEnd("—", 10)+log.PadEnd("—", 10)) + ansi.Muted("0 "+unit)
		}
		return head + ansi.Bold(log.PadEnd(log.FormatAge(quantile(ds, 0.5)), 10)) +
			log.PadEnd(log.FormatAge(quantile(ds, 0.9)), 10) + ansi.Muted(fmt.Sprintf("%d %s", len(ds), unit))
	}
	return append(lines,
		row("created → first PR", toPR, "PRs · "+label),
		row("picked → pushed", work, "runs · "+label),
		row("waiting in inbox", inbox, "tasks · now"),
		row("open PRs waiting", review, "PRs · now"),
	)
}

func quantile(ds []time.Duration, q float64) time.Duration {
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	return sorted[min(len(sorted)-1, int(q*float64(len(sorted))))]
}

func renderQueue(p pipelineData) []string {
	count := make([]int, len(stages))
	exhausted := 0
	retries := make([]int, max(1, p.maxRetries))
	for _, t := range p.tasks {
		s := stageOf(t)
		count[s]++
		if s != stageTodo {
			continue
		}
		if t.RetryCount >= p.maxRetries {
			exhausted++
		} else {
			retries[max(0, t.RetryCount)]++
		}
	}
	ready := 0
	for _, t := range p.ready {
		if t.RetryCount < p.maxRetries {
			ready++
		}
	}
	parts := []string{
		fmt.Sprintf("inbox %s", ansi.Bold(fmt.Sprint(count[stageInbox]))),
		fmt.Sprintf("todo %s %s", ansi.Bold(fmt.Sprint(count[stageTodo])), ansi.Muted(fmt.Sprintf("(%d ready)", ready))),
		fmt.Sprintf("active %s", ansi.Bold(fmt.Sprint(count[stageActive]))),
		fmt.Sprintf("PR open %s", ansi.Bold(fmt.Sprint(count[stagePROpen]))),
	}
	if exhausted > 0 {
		parts = append(parts, ansi.Warn(fmt.Sprintf("at max retries %d", exhausted)))
	}
	lines := []string{log.FormatSection("Queue"), log.FormatListItem(ansi.Info("•"), strings.Join(parts, ansi.Muted(" · ")))}
	if count[stageTodo] > exhausted {
		var hist []string
		for k, c := range retries {
			hist = append(hist, fmt.Sprintf("%d×%d", k, c))
		}
		lines = append(lines, log.FormatListDetail("retries", strings.Join(hist, ansi.Muted(" · "))+ansi.Muted("  (tasks in todo by retry count)")))
	}
	return lines
}

// wrapJoined joins items with sep into lines no wider than width visible columns.
func wrapJoined(items []string, sep string, width int) []string {
	var lines []string
	current := ""
	for _, it := range items {
		switch {
		case current == "":
			current = it
		case visibleWidth(current)+len(sep)+visibleWidth(it) > width:
			lines = append(lines, current)
			current = it
		default:
			current += sep + it
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func visibleWidth(s string) int {
	return len([]rune(ansi.Strip(s)))
}

func percent(part, whole int) string {
	if whole == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", int(math.Round(100*float64(part)/float64(whole))))
}

func sum(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

func argmax(xs []int) int {
	best := 0
	for i, x := range xs {
		if x > xs[best] {
			best = i
		}
	}
	return best
}
