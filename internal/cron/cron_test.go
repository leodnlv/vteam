package cron

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/golden"
)

// Syntax croner accepted but vteam dropped in the Go port.
var dropped = map[string]bool{"0 0 L * *": true, "0 0 * * 5#2": true}

func TestGolden(t *testing.T) {
	var cases []struct {
		Expr  string
		Valid bool
		From  time.Time
		Next  []time.Time
	}
	golden.Load(t, "cron", &cases)
	for _, c := range cases {
		t.Run(c.Expr, func(t *testing.T) {
			s, err := Parse(c.Expr)
			if dropped[c.Expr] || !c.Valid {
				if err == nil {
					t.Fatal("Parse succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var got []time.Time
			from := c.From.UTC()
			for range c.Next {
				from = s.Next(from)
				got = append(got, from)
			}
			if diff := cmp.Diff(c.Next, got); diff != "" {
				t.Errorf("next runs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValid(t *testing.T) {
	for _, expr := range []string{"* * * * *", "0 */6 * * *", "0 9 * * 1-5", "*/5 * * * *", "0 0 1 jan *", "0 0 * * 5-7"} {
		if !Valid(expr) {
			t.Errorf("Valid(%q) = false", expr)
		}
	}
	for _, expr := range []string{"", "* * *", "* * * * * *", "invalid", "a b c d e", "*/0 * * * *", "5-1 * * * *", "0 0 32 * *"} {
		if Valid(expr) {
			t.Errorf("Valid(%q) = true", expr)
		}
	}
}

func TestDayOfMonthAndWeekMatchEitherWhenBothAreSet(t *testing.T) {
	s, _ := Parse("0 0 13 * 5")
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	got := []time.Time{}
	for range 3 {
		from = s.Next(from)
		got = append(got, from)
	}
	want := []time.Time{
		time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error(diff)
	}
}

func TestSevenIsSunday(t *testing.T) {
	s, _ := Parse("0 0 * * 5-7")
	next := s.Next(time.Date(2026, 3, 7, 1, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextIsStrictlyAfter(t *testing.T) {
	s, _ := Parse("30 10 * * *")
	at := time.Date(2026, 3, 7, 10, 30, 0, 0, time.UTC)
	if next := s.Next(at); !next.Equal(at.AddDate(0, 0, 1)) {
		t.Errorf("next = %v", next)
	}
}
