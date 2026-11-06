package tui

import "testing"

func TestSpread(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		width int
		want  string
	}{
		{"pads the gap between left and right", "agents", "main", 14, "agents    main"},
		{"keeps a two cell gap when it just fits", "abc", "def", 8, "abc  def"},
		{"drops right when the gap would be one cell", "abc", "def", 7, "abc    "},
		{"drops right when both do not fit", "abcdef", "ghij", 8, "abcdef  "},
		{"truncates left with an ellipsis when it alone is too wide", "abcdefghij", "kl", 6, "abcde…"},
		{"returns empty when width is zero", "abc", "def", 0, ""},
		{"counts wide characters by display cells", "日本", "ab", 8, "日本  ab"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := spread(c.left, c.right, c.width); got != c.want {
				t.Errorf("spread(%q, %q, %d) = %q, want %q", c.left, c.right, c.width, got, c.want)
			}
		})
	}
}
