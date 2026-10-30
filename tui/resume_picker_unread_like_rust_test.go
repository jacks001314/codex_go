package tui

import (
	"strings"
	"testing"
	"time"
)

const unreadTaskMarkerSGR = "\x1b[38;2;59;130;246m\u25cf\x1b[39m"

func TestSessionPickerDenseRowShowsUnreadMarkerLikeRust(t *testing.T) {
	now := time.Date(2026, 4, 28, 16, 30, 0, 0, time.UTC)
	state := NewSessionPickerState(SessionPickerResume, nil, "")
	state.Density = SessionDensityDense
	state.UseThemeColors = false

	unread := SessionSummary{ThreadID: "thread-1", Preview: "Unread chat", UpdatedAt: now, UnreadRevision: "revision-1"}
	row := state.renderDenseSessionRow(unread, false, 80, now)
	if !strings.Contains(row, unreadTaskMarkerSGR+" Unread chat") {
		t.Fatalf("dense unread row = %q", row)
	}

	read := SessionSummary{ThreadID: "thread-1", Preview: "Read chat", UpdatedAt: now}
	row = state.renderDenseSessionRow(read, false, 80, now)
	if strings.Contains(row, unreadTaskMarkerSGR) || !strings.Contains(row, "Read chat") {
		t.Fatalf("dense read row = %q", row)
	}
}

func TestSessionPickerComfortableRowShowsUnreadMarkerLikeRust(t *testing.T) {
	now := time.Date(2026, 4, 28, 16, 30, 0, 0, time.UTC)
	state := NewSessionPickerState(SessionPickerResume, nil, "")
	state.Density = SessionDensityComfortable
	state.UseThemeColors = false

	item := SessionSummary{ThreadID: "thread-1", Preview: "Unread chat", UpdatedAt: now, UnreadRevision: "revision-1"}
	lines := state.renderComfortableSessionRow(item, false, 80, now)
	if len(lines) == 0 || !strings.Contains(lines[0], unreadTaskMarkerSGR+" Unread chat") {
		t.Fatalf("comfortable unread rows = %#v", lines)
	}

	selected := state.renderComfortableSessionRow(item, true, 80, now)
	if len(selected) == 0 || !strings.Contains(selected[0], unreadTaskMarkerSGR) {
		t.Fatalf("selected comfortable row lost the unread marker: %#v", selected)
	}
}

func TestSessionPickerUnreadMarkerKeepsTitleWithinWidthLikeRust(t *testing.T) {
	now := time.Date(2026, 4, 28, 16, 30, 0, 0, time.UTC)
	state := NewSessionPickerState(SessionPickerResume, nil, "")
	state.Density = SessionDensityDense
	state.UseThemeColors = false

	item := SessionSummary{ThreadID: "thread-1", Preview: strings.Repeat("x", 120), UpdatedAt: now, UnreadRevision: "revision-1"}
	row := state.renderDenseSessionRow(item, false, 40, now)
	if width := DisplayWidth(stripSGRForTest(row)); width > 40 {
		t.Fatalf("unread dense row width = %d, want <= 40: %q", width, row)
	}
}

// stripSGRForTest removes ANSI SGR sequences so row widths compare with the
// terminal cell count.
func stripSGRForTest(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\x1b' && i+1 < len(value) && value[i+1] == '[' {
			i += 2
			for i < len(value) && (value[i] < '@' || value[i] > '~') {
				i++
			}
			continue
		}
		out.WriteByte(value[i])
	}
	return out.String()
}
