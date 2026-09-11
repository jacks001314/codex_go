package tui

import (
	"strings"
	"testing"
	"time"
)

func threadColorPickerItems() []SessionSummary {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	return []SessionSummary{
		{
			ThreadID:  "00000000-0000-0000-0000-000000000001",
			Title:     "Investigate auth flow",
			CWD:       `D:\repo\a`,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-15 * time.Minute),
		},
		{
			ThreadID:  "3f2a1c9e-1111-2222-3333-444455556666",
			Title:     "Resume picker redesign",
			CWD:       `D:\repo\a`,
			CreatedAt: now.Add(-90 * time.Minute),
			UpdatedAt: now.Add(-30 * time.Minute),
		},
	}
}

func TestSessionPickerColorsThreadTitles(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	ResetThreadColorPaletteCache()
	picker := NewSessionPickerState(SessionPickerResume, threadColorPickerItems(), "")
	picker.ThemeID = "catppuccin-mocha"
	if !picker.UseThemeColors {
		t.Fatal("theme colors should default on (Rust PickerState::new)")
	}
	rendered := strings.Join(picker.RenderRows(80, now), "\n")
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("expected truecolor thread titles:\n%q", rendered)
	}

	dense := NewSessionPickerState(SessionPickerResume, threadColorPickerItems(), "")
	dense.ThemeID = "catppuccin-mocha"
	dense.Density = SessionDensityDense
	denseRendered := strings.Join(dense.RenderRows(80, now), "\n")
	if !strings.Contains(denseRendered, "\x1b[38;2;") {
		t.Fatalf("expected truecolor dense thread titles:\n%q", denseRendered)
	}
}

func TestSessionPickerColorSuppression(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	picker := NewSessionPickerState(SessionPickerResume, threadColorPickerItems(), "")
	picker.ThemeID = "catppuccin-mocha"
	picker.UseThemeColors = false
	rendered := strings.Join(picker.RenderRows(80, now), "\n")
	if strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("identity colors should be suppressed when theme colors are off:\n%q", rendered)
	}
}

func TestSessionPickerSelectedRowKeepsThreadColor(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	picker := NewSessionPickerState(SessionPickerResume, threadColorPickerItems(), "")
	picker.ThemeID = "catppuccin-mocha"
	// The first visible row is selected; it must keep the identity color while
	// still carrying the selection style.
	rows := picker.RenderRows(80, now)
	if len(rows) == 0 {
		t.Fatal("expected rows")
	}
	if !strings.Contains(rows[0], "\x1b[38;2;") {
		t.Fatalf("selected row lost the thread color: %q", rows[0])
	}
	if !strings.Contains(rows[0], SelectedRowMarker) || !strings.HasSuffix(rows[0], "\x1b[0m") {
		t.Fatalf("selected row missing selection style: %q", rows[0])
	}
}
