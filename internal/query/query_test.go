package query

import (
	"strings"
	"testing"
	"time"
)

func TestParseMessageRangeDateExpandsFullDay(t *testing.T) {
	got, err := ParseMessageRange("2026-05-14", "", "", time.Date(2026, 5, 20, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasStart || !got.HasEnd {
		t.Fatal("expected date to set both bounds")
	}
	if got.End-got.Start != 24*60*60-1 {
		t.Fatalf("date should expand to one full local day; start=%d end=%d", got.Start, got.End)
	}
}

func TestParseMessageRangeRejectsDateWithExplicitBounds(t *testing.T) {
	_, err := ParseMessageRange("today", "2026-05-14", "", time.Date(2026, 5, 20, 12, 0, 0, 0, time.Local))
	if err == nil || !strings.Contains(err.Error(), "date cannot be combined") {
		t.Fatalf("expected date/start conflict, got %v", err)
	}
}

func TestParseTimelineRangeRequiresExplicitRange(t *testing.T) {
	_, err := ParseTimelineRange("", "2026-05-14", "", time.Date(2026, 5, 20, 12, 0, 0, 0, time.Local))
	if err == nil || !strings.Contains(err.Error(), "timeline requires date or both start and end") {
		t.Fatalf("expected missing end error, got %v", err)
	}
}

func TestParseTimelineRangeLabelsBounds(t *testing.T) {
	got, err := ParseTimelineRange("", "2026-05-14 00:00:00", "2026-05-14 23:59:59", time.Date(2026, 5, 20, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got.StartLabel != "2026-05-14 00:00:00" || got.EndLabel != "2026-05-14 23:59:59" {
		t.Fatalf("unexpected labels: %+v", got)
	}
}

func TestNormalizeRefreshScope(t *testing.T) {
	got, err := NormalizeRefreshScope("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "all" {
		t.Fatalf("default scope = %q, want all", got)
	}
	if _, err := NormalizeRefreshScope("nope"); err == nil {
		t.Fatal("expected invalid scope error")
	}
}

func TestMessageRangeCalendarDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	for _, tc := range []struct {
		day   string
		hours int64
	}{{"2026-03-08", 23}, {"2026-11-01", 25}} {
		got, err := ParseMessageRange(tc.day, "", "", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if got.End-got.Start+1 != tc.hours*3600 {
			t.Fatalf("%s duration=%d", tc.day, got.End-got.Start+1)
		}
		bounds, err := ParseMessageRange("", tc.day, tc.day, time.Now())
		if err != nil || bounds != got {
			t.Fatalf("date and bounds differ: %v %v", bounds, err)
		}
	}
	a, err := ParseMessageRange("", "2026-10-21T00:00:00+08:00", "2026-10-21T00:00:00+08:00", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseMessageRange("", "2026-10-20T16:00:00Z", "2026-10-20T16:00:00Z", time.Now())
	if err != nil || a != b {
		t.Fatalf("equivalent timezones differ: %v %v", b, err)
	}
}
