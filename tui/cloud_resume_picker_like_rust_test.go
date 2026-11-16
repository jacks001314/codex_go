package tui

import (
	"strings"
	"testing"
	"time"
)

func cloudSessionPickerState(backendID BackendID) *SessionPickerState {
	state := NewSessionPickerStateForBackend(backendID, SessionPickerResume, nil, "")
	state.UseThemeColors = false
	state.CloudBackends[backendID] = true
	return state
}

// Rust #61742: dense and comfortable resume/fork rows prefix Cloud sessions.
func TestSessionPickerMarksCloudRowsLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	backendID := NewBackendID()
	state := cloudSessionPickerState(backendID)
	item := SessionSummary{ThreadID: "thread-cloud", Preview: "Fix resume picker", UpdatedAt: now.Add(-42 * time.Second)}

	state.Density = SessionDensityDense
	dense := state.renderDenseSessionRow(item, false, 30, now)
	if !strings.Contains(dense, "☁︎ Fix resume") || !strings.Contains(dense, "…") {
		t.Fatalf("dense cloud row = %q", dense)
	}
	if width := DisplayWidth(stripSGRForTest(dense)); width > 30 {
		t.Fatalf("dense cloud row width = %d, want <= 30: %q", width, dense)
	}

	comfortable := state.renderComfortableSessionRow(item, false, 30, now)
	if len(comfortable) == 0 || !strings.Contains(comfortable[0], "☁︎ Fix resume picker") {
		t.Fatalf("comfortable cloud rows = %#v", comfortable)
	}
}

func TestSessionPickerExpandedCloudSessionLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	backendID := NewBackendID()
	state := cloudSessionPickerState(backendID)
	item := SessionSummary{
		ThreadID:  "thread-cloud",
		Title:     "feat(tui): add raw scrollback mode to the transcript",
		UpdatedAt: now,
	}
	rows := renderExpandedSessionRows(item, state.itemCloud(item), 53, now)
	if len(rows) == 0 || !strings.HasPrefix(strings.TrimSpace(rows[0]), "│ Session:    ☁︎ feat(tui): add raw scrollback mode…") {
		t.Fatalf("expanded cloud session row = %q", rows)
	}

	local := NewSessionPickerStateForBackend(backendID, SessionPickerResume, nil, "")
	rows = renderExpandedSessionRows(item, local.itemCloud(item), 53, now)
	if strings.Contains(rows[0], "☁︎") {
		t.Fatalf("local expanded session row gained a marker: %q", rows[0])
	}
}
