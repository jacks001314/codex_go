package historycell

import (
	"strings"
	"testing"
	"time"
)

// Rust #48807, `separators_tests.rs::completion_label_shows_known_durations_including_short_turns`:
// every known duration is rendered, a sub-second turn shows `<1s`, and the
// completion metadata is separated with a bullet instead of a middle dot.
//
// Go still prefixes the timestamp with the historical `done ` label (Rust
// removed it in #46067), which is why the expected strings below keep it.
func TestCompletionLabelShowsShortDurationsLikeRust(t *testing.T) {
	completedAt := time.Date(2000, 9, 6, 14, 32, 0, 0, time.Local)
	seconds := func(value int64) *int64 { return &value }

	cases := []struct {
		elapsed *int64
		want    string
	}{
		{nil, "done 2:32 PM"},
		{seconds(0), "Worked for <1s \u2022 done 2:32 PM"},
		{seconds(12), "Worked for 12s \u2022 done 2:32 PM"},
		{seconds(60), "Worked for 1m 0s \u2022 done 2:32 PM"},
		{seconds(61), "Worked for 1m 1s \u2022 done 2:32 PM"},
		{seconds(125), "Worked for 2m 5s \u2022 done 2:32 PM"},
		{seconds(3605), "Worked for 1h 0m 5s \u2022 done 2:32 PM"},
	}
	for _, tc := range cases {
		cell := NewFinalMessageSeparator(tc.elapsed, nil).WithCompletedAt(completedAt)
		cell.DisplayDate = separatorDate(completedAt)
		if got := cell.Label(); got != tc.want {
			t.Errorf("elapsed=%v label = %q, want %q", tc.elapsed, got, tc.want)
		}
	}
}

// Rust #48807, `separators_tests.rs::completion_without_timestamp_retains_known_elapsed_duration`:
// a completed turn without a saved timestamp still shows the duration it knows.
func TestCompletionWithoutTimestampRetainsElapsedLikeRust(t *testing.T) {
	elapsed := int64(125)
	cell := NewFinalMessageSeparator(&elapsed, nil)
	raw := cell.RawLines()
	if len(raw) != 1 || raw[0] != "Worked for 2m 5s" {
		t.Fatalf("raw = %#v, want [Worked for 2m 5s]", raw)
	}
	if strings.Contains(raw[0], "\u00b7") {
		t.Fatalf("raw still uses the middle-dot separator: %q", raw[0])
	}

	// A separator without any known metadata still occupies no transcript rows.
	empty := NewFinalMessageSeparator(nil, nil)
	if got := empty.DisplayLines(80); len(got) != 0 {
		t.Fatalf("empty separator displayed %#v, want no rows", got)
	}
	if got := empty.RawLines(); len(got) != 0 {
		t.Fatalf("empty separator raw = %#v", got)
	}
}
