package tea

import (
	"strings"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

// Rust #59739 workspace_hud: an open presets menu owns the floating panel and
// is painted over the frame, so the composer keeps its rows.
func TestPresetsHUDOverlaysFrameLikeRust(t *testing.T) {
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "hello", Label: quickDialPtr("Greeting")},
		},
	})
	before := model.viewString()
	model.quickDialMenu = true
	after := model.viewString()

	if got, want := strings.Count(after, "\n"), strings.Count(before, "\n"); got != want {
		t.Fatalf("presets panel changed the frame height: %d rows, want %d", got+1, want+1)
	}
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")
	if beforeLines[len(beforeLines)-1] != afterLines[len(afterLines)-1] {
		t.Fatalf("presets panel moved the last row:\nbefore %q\nafter  %q", beforeLines[len(beforeLines)-1], afterLines[len(afterLines)-1])
	}
	row := -1
	for index, line := range afterLines {
		if strings.Contains(line, "Presets") {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatalf("presets panel missing from the frame:\n%s", after)
	}
	if row == len(afterLines)-1 {
		t.Fatalf("presets panel painted on the reserved bottom row: row %d of %d", row, len(afterLines))
	}
	column := strings.Index(afterLines[row], "Presets")
	if codextui.DisplayWidth(afterLines[row][:column]) < 48 {
		t.Fatalf("presets title starts at column %d, want the right half", codextui.DisplayWidth(afterLines[row][:column]))
	}
	if !strings.Contains(strings.Join(afterLines, "\n"), "1 · Greeting") {
		t.Fatalf("presets panel dropped the stored preset:\n%s", after)
	}
}
