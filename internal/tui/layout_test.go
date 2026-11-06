package tui

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWindowPadsShortInputWithBlankLines(t *testing.T) {
	got := window([]string{"abc"}, 3, 5)

	want := []string{"abc  ", "     ", "     "}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("window mismatch (-want +got):\n%s", diff)
	}
}

func TestWindowDropsLinesBeyondHeight(t *testing.T) {
	got := window([]string{"one", "two", "three"}, 2, 5)

	want := []string{"one  ", "two  "}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("window mismatch (-want +got):\n%s", diff)
	}
}

func TestWindowTruncatesLongLinesWithEllipsis(t *testing.T) {
	got := window([]string{"abcdefghij"}, 1, 5)

	want := []string{"abcd…"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("window mismatch (-want +got):\n%s", diff)
	}
}

func TestWindowWithZeroHeightReturnsNoLines(t *testing.T) {
	if got := window([]string{"abc"}, 0, 5); len(got) != 0 {
		t.Errorf("window = %q, want no lines", got)
	}
}

func TestWindowWithNonPositiveWidthReturnsEmptyLines(t *testing.T) {
	got := window([]string{"abc", "def"}, 2, 0)

	want := []string{"", ""}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("window mismatch (-want +got):\n%s", diff)
	}
}
