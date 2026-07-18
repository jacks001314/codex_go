package bottompane

import (
	"strings"
)

// Rust parity: codex-rs/tui/src/bottom_pane/experimental_features_view.rs.

type ExperimentalFeatureRow struct {
	Key     string
	Enabled bool
}

type ExperimentalFeatureItem struct {
	Feature     string
	Name        string
	Description string
	Enabled     bool
}

type ExperimentalFeaturesEvent struct {
	Updates map[string]bool
}

type ExperimentalFeaturesView struct {
	Features []ExperimentalFeatureItem
	State    ScrollState
	Complete bool
	Events   []ExperimentalFeaturesEvent
}

const selectedRowMarker = "\u203a"

func NewExperimentalFeaturesView(features []ExperimentalFeatureItem) *ExperimentalFeaturesView {
	view := &ExperimentalFeaturesView{Features: append([]ExperimentalFeatureItem(nil), features...), State: NewScrollState()}
	view.State.ClampSelection(len(view.Features))
	return view
}

func (v *ExperimentalFeaturesView) VisibleLen() int {
	if v == nil {
		return 0
	}
	return len(v.Features)
}

func (v *ExperimentalFeaturesView) MoveUp() {
	if v == nil || v.VisibleLen() == 0 {
		return
	}
	v.State.MoveUpWrap(v.VisibleLen())
	v.State.EnsureVisible(v.VisibleLen(), min(MaxPopupRows, v.VisibleLen()))
}

func (v *ExperimentalFeaturesView) MoveDown() {
	if v == nil || v.VisibleLen() == 0 {
		return
	}
	v.State.MoveDownWrap(v.VisibleLen())
	v.State.EnsureVisible(v.VisibleLen(), min(MaxPopupRows, v.VisibleLen()))
}

func (v *ExperimentalFeaturesView) PageUp() {
	if v == nil {
		return
	}
	v.State.PageUpClamped(v.VisibleLen(), min(MaxPopupRows, v.VisibleLen()))
}

func (v *ExperimentalFeaturesView) PageDown() {
	if v == nil {
		return
	}
	v.State.PageDownClamped(v.VisibleLen(), min(MaxPopupRows, v.VisibleLen()))
}

func (v *ExperimentalFeaturesView) JumpTop() {
	if v == nil {
		return
	}
	v.State.JumpTop(v.VisibleLen(), min(MaxPopupRows, v.VisibleLen()))
}

func (v *ExperimentalFeaturesView) JumpBottom() {
	if v == nil {
		return
	}
	v.State.JumpBottom(v.VisibleLen(), min(MaxPopupRows, v.VisibleLen()))
}

func (v *ExperimentalFeaturesView) ToggleSelected() {
	if v == nil || !v.State.HasSelection || v.State.SelectedIdx < 0 || v.State.SelectedIdx >= len(v.Features) {
		return
	}
	v.Features[v.State.SelectedIdx].Enabled = !v.Features[v.State.SelectedIdx].Enabled
}

func (v *ExperimentalFeaturesView) SaveAndClose() {
	if v == nil || v.Complete {
		return
	}
	if len(v.Features) > 0 {
		updates := map[string]bool{}
		for _, feature := range v.Features {
			updates[feature.Feature] = feature.Enabled
		}
		v.Events = append(v.Events, ExperimentalFeaturesEvent{Updates: updates})
	}
	v.Complete = true
}

func (v *ExperimentalFeaturesView) HandleKey(key string) {
	if v == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "up", "ctrl+p":
		v.MoveUp()
	case "down", "ctrl+n":
		v.MoveDown()
	case "pageup", "ctrl+u":
		v.PageUp()
	case "pagedown", "ctrl+d":
		v.PageDown()
	case "home", "ctrl+a":
		v.JumpTop()
	case "end", "ctrl+e":
		v.JumpBottom()
	case "space":
		v.ToggleSelected()
	case "enter", "esc", "ctrl+c":
		v.SaveAndClose()
	}
}

func (v *ExperimentalFeaturesView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	rows := []string{
		"Experimental features",
		"Toggle experimental features. Changes are saved to config.toml.",
	}
	if len(v.Features) == 0 {
		rows = append(rows, "  No experimental features available for now")
	} else {
		v.State.ClampSelection(len(v.Features))
		v.State.EnsureVisible(len(v.Features), min(MaxPopupRows, len(v.Features)))
		rows = append(rows, RenderGenericRows(v.displayRows(), v.State, MaxPopupRows, "  No experimental features available for now", max(width-2, 1), ColumnWidthConfig{})...)
	}
	rows = append(rows, ExperimentalPopupHintLine())
	return rows
}

func (v *ExperimentalFeaturesView) DesiredHeight(width int) int {
	return len(v.Rows(width))
}

func ExperimentalPopupHintLine() string {
	return "Press space to select or enter to save for next conversation"
}

func (v *ExperimentalFeaturesView) displayRows() []GenericDisplayRow {
	if v == nil {
		return nil
	}
	rows := make([]GenericDisplayRow, 0, len(v.Features))
	for idx, feature := range v.Features {
		prefix := " "
		if v.State.HasSelection && v.State.SelectedIdx == idx {
			prefix = selectedRowMarker
		}
		marker := " "
		if feature.Enabled {
			marker = "x"
		}
		rows = append(rows, GenericDisplayRow{
			Name:        prefix + " [" + marker + "] " + feature.Name,
			Description: feature.Description,
		})
	}
	return rows
}
