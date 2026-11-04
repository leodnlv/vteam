package slugify

import (
	"testing"

	"github.com/leodnlv/vteam/internal/golden"
)

func TestSlugifyGolden(t *testing.T) {
	var cases []struct{ Input, Expected string }
	golden.Load(t, "slugify", &cases)
	for _, c := range cases {
		if got := Slugify(c.Input); got != c.Expected {
			t.Errorf("Slugify(%q) = %q, want %q", c.Input, got, c.Expected)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"lowercases input", "Hello World", "hello-world"},
		{"replaces spaces with hyphens", "fix auth bug", "fix-auth-bug"},
		{"strips non-alphanumeric characters", "fix: the bug!", "fix-the-bug"},
		{"collapses consecutive separators into one hyphen", "too   many---spaces", "too-many-spaces"},
		{"strips leading and trailing hyphens", "--trimmed--", "trimmed"},
		{"handles already-slugified input", "already-a-slug", "already-a-slug"},
		{"returns empty string for empty input", "", ""},
		{"returns empty string for all-special-character input", "!@#$%", ""},
		{"preserves numbers", "step 1 of 3", "step-1-of-3"},
		{"handles unicode accented characters", "café résumé", "cafe-resume"},
		{"handles mixed case with punctuation", "Missing Null-Check in Auth", "missing-null-check-in-auth"},
		{"handles input used for task filenames", "Fix Auth Bug", "fix-auth-bug"},
		{"handles input used for branch names", "2026-04-19-fix-null-check", "2026-04-19-fix-null-check"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Slugify(c.in); got != c.want {
				t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
