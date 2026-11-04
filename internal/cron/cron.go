// Package cron parses 5-field cron expressions (minute hour day-of-month month day-of-week)
// and computes their next fire times in local time.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type field struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	minuteField = field{name: "minute", min: 0, max: 59}
	hourField   = field{name: "hour", min: 0, max: 23}
	domField    = field{name: "day of month", min: 1, max: 31}
	monthField  = field{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	dowField = field{name: "day of week", min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

type Schedule struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

func Parse(expr string) (*Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("expected 5 fields (minute hour day month weekday), got %d", len(parts))
	}
	s := &Schedule{domStar: strings.HasPrefix(parts[2], "*"), dowStar: strings.HasPrefix(parts[4], "*")}
	var err error
	for i, target := range []struct {
		bits *uint64
		f    field
	}{{&s.minute, minuteField}, {&s.hour, hourField}, {&s.dom, domField}, {&s.month, monthField}, {&s.dow, dowField}} {
		if *target.bits, err = parseField(parts[i], target.f); err != nil {
			return nil, err
		}
	}
	if s.dow&(1<<7) != 0 {
		s.dow = s.dow&^(1<<7) | 1
	}
	return s, nil
}

func Valid(expr string) bool {
	_, err := Parse(expr)
	return err == nil
}

func parseField(spec string, f field) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(spec, ",") {
		rangeSpec, stepSpec, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepSpec)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("invalid step %q in %s field", stepSpec, f.name)
			}
			step = n
		}
		lo, hi := f.min, f.max
		if rangeSpec != "*" {
			loSpec, hiSpec, isRange := strings.Cut(rangeSpec, "-")
			var err error
			if lo, err = f.value(loSpec); err != nil {
				return 0, err
			}
			hi = lo
			if isRange {
				if hi, err = f.value(hiSpec); err != nil {
					return 0, err
				}
			} else if hasStep {
				hi = f.max
			}
			if lo > hi {
				return 0, fmt.Errorf("invalid range %q in %s field", rangeSpec, f.name)
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

func (f field) value(spec string) (int, error) {
	if n, ok := f.names[strings.ToLower(spec)]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(spec)
	if err != nil || n < f.min || n > f.max {
		return 0, fmt.Errorf("invalid value %q in %s field (%d-%d)", spec, f.name, f.min, f.max)
	}
	return n, nil
}

func has(bits uint64, v int) bool { return bits&(1<<v) != 0 }

func (s *Schedule) dayMatches(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// Next returns the first fire time strictly after t, or the zero time if none exists within five years.
func (s *Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case !has(s.month, int(t.Month())):
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case !has(s.hour, t.Hour()):
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case !has(s.minute, t.Minute()):
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
