package bottompane

import (
	"strings"
)

// Rust parity: codex-rs/tui/src/bottom_pane/memories_settings_view.rs.

const MemoriesDocURL = "https://developers.openai.com/codex/memories"

type MemoriesSetting string

const (
	MemoriesSettingUse      MemoriesSetting = "use"
	MemoriesSettingGenerate MemoriesSetting = "generate"
)

type MemoriesEventKind string

const (
	MemoriesEventUpdateSettings MemoriesEventKind = "update_memory_settings"
	MemoriesEventReset          MemoriesEventKind = "reset_memories"
)

type MemoriesEvent struct {
	Kind             MemoriesEventKind
	UseMemories      bool
	GenerateMemories bool
}

type MemoriesSettingsView struct {
	Enabled bool

	UseMemories      bool
	GenerateMemories bool
	State            ScrollState
	ResetState       *ScrollState
	Complete         bool
	Events           []MemoriesEvent
}

func NewMemoriesSettingsView(useMemories bool, generateMemories bool) *MemoriesSettingsView {
	view := &MemoriesSettingsView{
		Enabled:          useMemories,
		UseMemories:      useMemories,
		GenerateMemories: generateMemories,
		State:            NewScrollState(),
	}
	view.State.ClampSelection(view.visibleLen())
	return view
}

func (v *MemoriesSettingsView) visibleLen() int {
	if v == nil {
		return 0
	}
	if v.ResetState != nil {
		return 2
	}
	return 3
}

func (v *MemoriesSettingsView) activeState() *ScrollState {
	if v == nil {
		return nil
	}
	if v.ResetState != nil {
		return v.ResetState
	}
	return &v.State
}

func (v *MemoriesSettingsView) MoveUp() {
	if state := v.activeState(); state != nil {
		length := v.visibleLen()
		state.MoveUpWrap(length)
		state.EnsureVisible(length, min(MaxPopupRows, length))
	}
}

func (v *MemoriesSettingsView) MoveDown() {
	if state := v.activeState(); state != nil {
		length := v.visibleLen()
		state.MoveDownWrap(length)
		state.EnsureVisible(length, min(MaxPopupRows, length))
	}
}

func (v *MemoriesSettingsView) PageUp() {
	if state := v.activeState(); state != nil {
		length := v.visibleLen()
		state.PageUpClamped(length, min(MaxPopupRows, length))
	}
}

func (v *MemoriesSettingsView) PageDown() {
	if state := v.activeState(); state != nil {
		length := v.visibleLen()
		state.PageDownClamped(length, min(MaxPopupRows, length))
	}
}

func (v *MemoriesSettingsView) JumpTop() {
	if state := v.activeState(); state != nil {
		length := v.visibleLen()
		state.JumpTop(length, min(MaxPopupRows, length))
	}
}

func (v *MemoriesSettingsView) JumpBottom() {
	if state := v.activeState(); state != nil {
		length := v.visibleLen()
		state.JumpBottom(length, min(MaxPopupRows, length))
	}
}

func (v *MemoriesSettingsView) ToggleSelected() {
	if v == nil || v.ResetState != nil || !v.State.HasSelection {
		return
	}
	switch v.State.SelectedIdx {
	case 0:
		v.UseMemories = !v.UseMemories
		v.Enabled = v.UseMemories
	case 1:
		v.GenerateMemories = !v.GenerateMemories
	}
}

func (v *MemoriesSettingsView) Save() {
	if v == nil || v.Complete {
		return
	}
	if v.ResetState != nil {
		switch v.ResetState.SelectedIdx {
		case 0:
			v.Events = append(v.Events, MemoriesEvent{Kind: MemoriesEventReset})
			v.Complete = true
		case 1:
			v.CloseResetConfirmation()
		}
		return
	}
	switch v.State.SelectedIdx {
	case 2:
		v.OpenResetConfirmation()
	default:
		v.Events = append(v.Events, MemoriesEvent{
			Kind:             MemoriesEventUpdateSettings,
			UseMemories:      v.UseMemories,
			GenerateMemories: v.GenerateMemories,
		})
		v.Complete = true
	}
}

func (v *MemoriesSettingsView) Cancel() {
	if v == nil || v.Complete {
		return
	}
	if v.ResetState != nil {
		v.CloseResetConfirmation()
		return
	}
	v.Complete = true
}

func (v *MemoriesSettingsView) OpenResetConfirmation() {
	if v == nil {
		return
	}
	state := NewScrollState()
	state.ClampSelection(2)
	v.ResetState = &state
}

func (v *MemoriesSettingsView) CloseResetConfirmation() {
	if v == nil {
		return
	}
	v.ResetState = nil
	v.State.SelectedIdx = 2
	v.State.HasSelection = true
	v.State.EnsureVisible(v.visibleLen(), min(MaxPopupRows, v.visibleLen()))
}

func (v *MemoriesSettingsView) HandleKey(key string) {
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
	case "enter":
		v.Save()
	case "esc", "ctrl+c":
		v.Cancel()
	}
}

func (v *MemoriesSettingsView) CurrentSetting(setting MemoriesSetting) bool {
	if v == nil {
		return false
	}
	switch setting {
	case MemoriesSettingUse:
		return v.UseMemories
	case MemoriesSettingGenerate:
		return v.GenerateMemories
	default:
		return false
	}
}

func (v *MemoriesSettingsView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	if v.ResetState != nil {
		return v.resetRows(width)
	}
	rows := []string{
		"Memories",
		"Choose how Codex uses and creates memories. Changes are saved to config.toml",
	}
	options := []struct {
		name string
	}{
		{"Use memories"},
		{"Generate memories"},
		{"Reset all memories"},
	}
	v.State.ClampSelection(len(options))
	v.State.EnsureVisible(len(options), min(MaxPopupRows, len(options)))
	rows = append(rows, RenderGenericRows(v.displayRows(), v.State, MaxPopupRows, "  No memory settings available", max(width-2, 1), ColumnWidthConfig{})...)
	rows = append(rows, "Learn more: "+MemoriesDocURL)
	rows = append(rows, "Press space to toggle; enter to save or select")
	return rows
}

func (v *MemoriesSettingsView) resetRows(width int) []string {
	rows := []string{
		"Reset all memories?",
		"This clears local memory files and rollout summaries for the current Codex home.",
	}
	options := []struct {
		name        string
		description string
	}{
		{"Reset all memories", "Delete local memory files and rollout summaries."},
		{"Go back", "Return to memory settings."},
	}
	v.ResetState.ClampSelection(len(options))
	v.ResetState.EnsureVisible(len(options), min(MaxPopupRows, len(options)))
	rows = append(rows, RenderGenericRows(v.displayRows(), *v.ResetState, MaxPopupRows, "  No memory settings available", max(width-2, 1), ColumnWidthConfig{})...)
	rows = append(rows, StandardPopupHintLine)
	return rows
}

func (v *MemoriesSettingsView) DesiredHeight(width int) int {
	return len(v.Rows(width))
}

func (v *MemoriesSettingsView) displayRows() []GenericDisplayRow {
	if v == nil {
		return nil
	}
	if v.ResetState != nil {
		state := v.ResetState
		return []GenericDisplayRow{
			{
				Name:        memoryResetName(state, 0, "Reset all memories"),
				Description: "Delete local memory files and rollout summaries.",
			},
			{
				Name:        memoryResetName(state, 1, "Go back"),
				Description: "Return to memory settings.",
			},
		}
	}
	return []GenericDisplayRow{
		{
			Name:        memorySettingName(&v.State, 0, "Use memories", v.UseMemories, true),
			Description: "Use memories in the following threads. Applied at next thread.",
		},
		{
			Name:        memorySettingName(&v.State, 1, "Generate memories", v.GenerateMemories, true),
			Description: "Generate memories from the following threads. Current thread included.",
		},
		{
			Name:        memorySettingName(&v.State, 2, "Reset all memories", false, false),
			Description: "Clear local memory files and summaries. Existing threads stay intact.",
		},
	}
}

func memorySettingName(state *ScrollState, idx int, name string, enabled bool, toggle bool) string {
	prefix := " "
	if state != nil && state.HasSelection && state.SelectedIdx == idx {
		prefix = selectedRowMarker
	}
	if !toggle {
		return prefix + " " + name
	}
	marker := " "
	if enabled {
		marker = "x"
	}
	return prefix + " [" + marker + "] " + name
}

func memoryResetName(state *ScrollState, idx int, name string) string {
	if state != nil && state.HasSelection && state.SelectedIdx == idx {
		return selectedRowMarker + name
	}
	return "  " + name
}
