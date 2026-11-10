package tasks

import "testing"

func TestHasPRParagraph(t *testing.T) {
	for body, want := range map[string]bool{
		"coder: completed\n\nsummary\n\n" + PRParagraph("https://x/pull/1"): true,
		PRParagraph("https://x/pull/1"):                                     true,
		"merge-ensurer: canceled\n\nPR https://x/pull/1 was closed":         false,
		"coder: completed\n\nsee the PR: it is fine":                        false,
	} {
		if got := HasPRParagraph(body); got != want {
			t.Errorf("HasPRParagraph(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestHasNoteHeadline(t *testing.T) {
	body := NoteHeadline("merge-ensurer", "reopened") + "\n\nPR has the label"
	if !HasNoteHeadline(body, "merge-ensurer", "reopened") {
		t.Error("want the headline matched")
	}
	if HasNoteHeadline(body, "merge-ensurer", "canceled") || HasNoteHeadline("merge-ensurer: reopened later", "merge-ensurer", "reopened") {
		t.Error("only the exact first line matches")
	}
}
