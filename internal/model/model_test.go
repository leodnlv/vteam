package model

import (
	"testing"
	"time"
)

func TestSeverityValid(t *testing.T) {
	tests := []struct {
		severity Severity
		want     bool
	}{
		{SeverityCritical, true},
		{SeverityHigh, true},
		{SeverityMedium, true},
		{SeverityLow, true},
		{Severity(""), false},
		{Severity("urgent"), false},
		{Severity("HIGH"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			if got := tt.severity.Valid(); got != tt.want {
				t.Errorf("Severity(%q).Valid() = %v, want %v", tt.severity, got, tt.want)
			}
		})
	}
}

func TestSeverityPriority(t *testing.T) {
	tests := []struct {
		severity Severity
		want     int
	}{
		{SeverityCritical, 0},
		{SeverityHigh, 1},
		{SeverityMedium, 2},
		{SeverityLow, 3},
		{Severity("unknown"), 3},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			if got := tt.severity.Priority(); got != tt.want {
				t.Errorf("Severity(%q).Priority() = %d, want %d", tt.severity, got, tt.want)
			}
		})
	}
}

func TestISOTimeFormatsUTCWithMilliseconds(t *testing.T) {
	ts := time.Date(2026, 11, 6, 13, 59, 52, 123_000_000, time.UTC)

	if got, want := ISOTime(ts), "2026-11-06T13:59:52.123Z"; got != want {
		t.Errorf("ISOTime() = %q, want %q", got, want)
	}
}

func TestISOTimeConvertsLocalTimeToUTC(t *testing.T) {
	ts := time.Date(2026, 11, 6, 15, 0, 0, 0, time.FixedZone("CET", 3600))

	if got, want := ISOTime(ts), "2026-11-06T14:00:00.000Z"; got != want {
		t.Errorf("ISOTime() = %q, want %q", got, want)
	}
}

func TestISOTimeTruncatesSubMillisecondPrecision(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 987_654_321, time.UTC)

	if got, want := ISOTime(ts), "2026-01-02T03:04:05.987Z"; got != want {
		t.Errorf("ISOTime() = %q, want %q", got, want)
	}
}
