package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/utils"
)

func quickDialPtr(value string) *string { return &value }

func quickDialTestState() *codextui.State {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	return state
}

func TestQuickDialsSlashCommandOpensHUDLikeRust(t *testing.T) {
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "hello", Label: quickDialPtr("Greeting")},
			"2": {Input: "  first line\nsecond line"},
			"0": {Input: "/status"},
		},
	})
	invocation, ok := codextui.ParseCommand("/presets")
	if !ok || invocation.Command != codextui.CommandPresets {
		t.Fatalf("ParseCommand(/presets) = %#v ok=%v", invocation, ok)
	}
	runTeaCmd(t, model, model.applyCommand(invocation))
	if !model.quickDialMenu {
		t.Fatal("/presets did not open the quick-dial HUD")
	}
	view := utils.StripANSI(model.View())
	for _, want := range []string{
		"Presets",
		"1 · Greeting",
		"2 · first line",
		"0 · /status",
		"n new   s from settings   e edit   d delete   esc close",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("preset HUD missing %q:\n%s", want, view)
		}
	}
}

func TestPresetsUnavailableBeforeSessionLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	invocation, ok := codextui.ParseCommand("/presets")
	if !ok {
		t.Fatal("/presets did not parse")
	}
	runTeaCmd(t, model, model.applyCommand(invocation))
	if model.quickDialMenu {
		t.Fatal("HUD opened before the session was configured")
	}
	if want := "Presets are available after startup completes."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
}

func TestQuickDialRecallKeepsComposerDraftLikeRust(t *testing.T) {
	var submitted []SubmitRequest
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "hello from preset"},
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	typeText(t, model, "keep this draft")
	if cmd := model.openQuickDialMenu(); cmd != nil {
		t.Fatal("opening the preset HUD unexpectedly returned a command")
	}
	updated, cmd := model.Update(runes("1"))
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}
	runTeaCmd(t, model, cmd)

	if len(submitted) != 1 || submitted[0].Prompt != "hello from preset" {
		t.Fatalf("submitted requests = %#v, want one preset prompt", submitted)
	}
	if got := model.composerText(); got != "keep this draft" {
		t.Fatalf("composer draft = %q, want it preserved", got)
	}
	if model.quickDialMenu {
		t.Fatal("recalling a preset left the HUD open")
	}
}

// TestQuickDialMenuEnterKeepsHudOpenLikeRust covers Rust #54381: Enter is
// consumed by the passive HUD and does not dismiss it or submit the draft.
func TestQuickDialMenuEnterKeepsHudOpenLikeRust(t *testing.T) {
	var submitted []SubmitRequest
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "hello"},
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	typeText(t, model, "keep this draft")
	model.openQuickDialMenu()
	model.Update(key(bubbletea.KeyEnter))
	if !model.quickDialMenu {
		t.Fatal("Enter dismissed the preset HUD")
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted requests = %#v, want none", submitted)
	}
	if got := model.composerText(); got != "keep this draft" {
		t.Fatalf("composer draft = %q", got)
	}
}

// TestQuickDialHeldActionConsumesRepeatsLikeRust covers Rust #54381's
// ActivationKeyGuard: an action key held across the menu transition must not
// type into the composer or re-trigger the menu.
func TestQuickDialHeldActionConsumesRepeatsLikeRust(t *testing.T) {
	var submitted []SubmitRequest
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "hello"},
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	now := time.Unix(100, 0)
	model.now = func() time.Time { return now }
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("1"))
	runTeaCmd(t, model, cmd)
	if len(submitted) != 1 {
		t.Fatalf("submitted requests = %#v, want one recall", submitted)
	}

	// The held repeat is consumed instead of reaching the composer.
	model.Update(runes("1"))
	if got := model.composerText(); got != "" {
		t.Fatalf("held repeat reached the composer: %q", got)
	}
	if model.quickDialMenu {
		t.Fatal("held repeat reopened the preset HUD")
	}

	// After the repeat window a fresh press is normal input.
	now = now.Add(2 * quickDialKeyGuardWindow)
	model.Update(runes("1"))
	if got := model.composerText(); got != "1" {
		t.Fatalf("fresh press after the guard window = %q, want 1", got)
	}
}

// TestQuickDialPasteDismissesHudLikeRust covers Rust #54381: a paste, including
// a single character, cancels the HUD and reaches the composer.
func TestQuickDialPasteDismissesHudLikeRust(t *testing.T) {
	var submitted []SubmitRequest
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "hello"},
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	model.openQuickDialMenu()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'1'}, Paste: true})
	if model.quickDialMenu {
		t.Fatal("paste left the preset HUD open")
	}
	if len(submitted) != 0 {
		t.Fatalf("paste recalled a preset: %#v", submitted)
	}
	if got := model.composerText(); got != "1" {
		t.Fatalf("composer text = %q, want the pasted character", got)
	}
}

// TestQuickDialMenuYieldsToActivePopupLikeRust covers Rust #54381 popup
// ownership: an active slash popup keeps the preset HUD closed.
func TestQuickDialMenuYieldsToActivePopupLikeRust(t *testing.T) {
	model := NewModel(quickDialTestState(), Options{Width: 100, Height: 30})
	model.slashPopup = slashCommandPopup{Active: true}
	model.openQuickDialMenu()
	if model.quickDialMenu {
		t.Fatal("preset HUD opened over an active slash popup")
	}
	if !model.slashPopup.Active {
		t.Fatal("opening presets dismissed the popup it must yield to")
	}
}

func TestQuickDialRecallParsesSlashCommandsLikeRust(t *testing.T) {
	state := quickDialTestState()
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "/status"},
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			t.Fatalf("slash-command preset was submitted as a prompt: %#v", request)
			return nil
		},
	})
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("1"))
	runTeaCmd(t, model, cmd)
	if len(state.Messages) == 0 {
		t.Fatal("slash-command preset did not dispatch /status locally")
	}
	if !strings.Contains(state.Messages[len(state.Messages)-1].Text, "/status") {
		t.Fatalf("status output not recorded: %#v", state.Messages)
	}
}

func TestQuickDialPrefillsUseCurrentSettingsLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-5",
		ReasoningEffort: "medium",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
		ServiceTier:     "standard",
	})
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{
		Width:  120,
		Height: 36,
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5",
			ShowInPicker:           true,
			DefaultReasoningEffort: "medium",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{
				Effort: "medium",
				Label:  "Medium",
			}},
			ServiceTiers: []string{"priority"},
		}},
	})
	model.statusControls.StatusLineIDs = []string{"model"}
	model.statusControls.StatusLineConfigured = true
	model.statusControls.StatusLineUseThemeColors = true
	model.statusControls.TerminalTitleIDs = []string{"project-name"}
	model.statusControls.TerminalTitleConfigured = true
	model.statusLineUseColors = true

	model.openQuickDialMenu()
	_, cmd := model.Update(runes("s"))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialPrefillPickerID {
		t.Fatalf("prefill picker modal = %#v", model.modal)
	}
	want := map[string]string{
		"model":          "/model gpt-5 --reasoning=medium --speed=standard",
		"permissions":    "/permissions workspace-write --approval-policy=on-request --reviewer=user",
		"terminal-title": "/title project-name",
		"status-line":    "/statusline model --colors",
	}
	found := map[string]bool{}
	for _, option := range model.modal.options {
		if option.Disabled {
			t.Fatalf("prefill %q unexpectedly disabled: %q", option.ID, option.DisabledReason)
		}
		if option.Description != want[option.ID] {
			t.Fatalf("prefill %q = %q, want %q", option.ID, option.Description, want[option.ID])
		}
		found[option.ID] = true
	}
	for id := range want {
		if !found[id] {
			t.Fatalf("missing prefill %q: %#v", id, model.modal.options)
		}
	}

	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.customPrompt == nil {
		t.Fatalf("prefill selection did not open the editor: %#v", model.modal)
	}
	if got := model.modal.customPrompt.Text; got != want["model"] {
		t.Fatalf("prefilled editor text = %q, want %q", got, want["model"])
	}
}

func TestQuickDialPrefillsDisableUnavailableSettingsLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{
		Width:  120,
		Height: 36,
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5",
			ShowInPicker:           true,
			DefaultReasoningEffort: "medium",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{
				Effort: "medium",
				Label:  "Medium",
			}},
		}},
	})
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("s"))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialPrefillPickerID {
		t.Fatalf("prefill picker modal = %#v", model.modal)
	}
	for _, option := range model.modal.options {
		if option.ID != "permissions" {
			continue
		}
		if !option.Disabled || !strings.Contains(option.DisabledReason, "Select a named permission profile") {
			t.Fatalf("permissions prefill = %#v", option)
		}
		return
	}
	t.Fatal("permissions prefill option missing")
}

func TestQuickDialStatusSurfaceLayoutsNormalizeItemsLikeRust(t *testing.T) {
	model := NewModel(quickDialTestState(), Options{Width: 120, Height: 36})
	model.statusControls.TerminalTitleIDs = []string{"model-name", "project", "model-name"}
	model.statusControls.TerminalTitleConfigured = true
	model.statusControls.StatusLineIDs = []string{"model-name", "project-root", "model-name"}
	model.statusControls.StatusLineConfigured = true
	model.statusLineUseColors = false

	title, reason := model.currentTerminalTitleQuickDial()
	if reason != "" || title.Input != "/title model project-name model" {
		t.Fatalf("terminal-title preset = %#v reason=%q", title, reason)
	}
	status, reason := model.currentStatusLineQuickDial()
	if reason != "" || status.Input != "/statusline model project-name model --no-colors" {
		t.Fatalf("status-line preset = %#v reason=%q", status, reason)
	}

	model.statusControls.TerminalTitleIDs = []string{}
	model.statusControls.StatusLineIDs = []string{}
	model.statusLineUseColors = true
	if title, reason := model.currentTerminalTitleQuickDial(); reason != "" || title.Input != "/title --clear" {
		t.Fatalf("empty terminal-title preset = %#v reason=%q", title, reason)
	}
	if status, reason := model.currentStatusLineQuickDial(); reason != "" || status.Input != "/statusline --clear --colors" {
		t.Fatalf("empty status-line preset = %#v reason=%q", status, reason)
	}

	model.statusControls.TerminalTitleIDs = []string{"invalid"}
	model.statusControls.StatusLineIDs = []string{"invalid"}
	if _, reason := model.currentTerminalTitleQuickDial(); !strings.Contains(reason, "Choose available terminal-title items") {
		t.Fatalf("invalid terminal-title reason = %q", reason)
	}
	if _, reason := model.currentStatusLineQuickDial(); !strings.Contains(reason, "Choose available status-line items") {
		t.Fatalf("invalid status-line reason = %q", reason)
	}
}

func TestQuickDialNewSlotWritesConfigAndUpdatesLivePresetsLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := NewModel(quickDialTestState(), Options{
		Width:      100,
		Height:     30,
		QuickDials: map[string]config.TUIPreset{},
		OnWriteSettings: func(writes []SettingsEdit) (SettingsWriteResult, error) {
			edits = append(edits, writes...)
			return SettingsWriteResult{
				QuickDials: map[string]config.TUIPreset{
					"1": {Input: "  new prompt  ", Label: quickDialPtr("")},
				},
				FilePath: "/tmp/config.toml",
			}, nil
		},
	})
	typeText(t, model, "  new prompt  ")
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("n"))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.customPrompt == nil {
		t.Fatalf("new preset did not open the editor: %#v", model.modal)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialSavePickerID {
		t.Fatalf("save picker modal = %#v", model.modal)
	}
	if model.modal.selected != 0 {
		t.Fatalf("save picker selected slot = %d, want first empty slot", model.modal.selected)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	if len(edits) != 1 {
		t.Fatalf("settings edits = %#v, want one preset write", edits)
	}
	if edits[0].KeyPath != "tui.presets.1" {
		t.Fatalf("preset key path = %q", edits[0].KeyPath)
	}
	value, ok := edits[0].Value.(map[string]any)
	if !ok || value["input"] != "  new prompt  " || value["label"] != "" {
		t.Fatalf("preset value = %#v", edits[0].Value)
	}
	if preset, ok := model.quickDials["1"]; !ok || preset.Input != "  new prompt  " {
		t.Fatalf("live presets = %#v, want slot 1 saved", model.quickDials)
	}
	if !strings.Contains(model.notice, "Saved preset 1") {
		t.Fatalf("notice = %q, want saved-preset confirmation", model.notice)
	}
}

func TestQuickDialEditorOmitsAttachmentsLikeRust(t *testing.T) {
	state := quickDialTestState()
	model := NewModel(state, Options{
		Width:      100,
		Height:     30,
		QuickDials: map[string]config.TUIPreset{},
	})
	model.attachments = []bottompane.ComposerAttachment{{
		Kind: bottompane.AttachmentImage,
		Path: "image.png",
	}}
	typeText(t, model, "draft with image")
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("n"))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.customPrompt == nil {
		t.Fatalf("new preset editor = %#v", model.modal)
	}
	if got := model.modal.customPrompt.Text; got != "" {
		t.Fatalf("preset editor copied attachment-bearing draft: %q", got)
	}
	if !strings.Contains(state.Messages[len(state.Messages)-1].Text, "Images cannot be copied into a preset") {
		t.Fatalf("missing attachment warning: %#v", state.Messages)
	}
}

func TestQuickDialDeletePickerConfirmsAndClearsSlotLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "delete me"},
		},
		OnWriteSettings: func(writes []SettingsEdit) (SettingsWriteResult, error) {
			edits = append(edits, writes...)
			return SettingsWriteResult{QuickDials: map[string]config.TUIPreset{}}, nil
		},
	})
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("d"))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialDeletePickerID {
		t.Fatalf("delete picker modal = %#v", model.modal)
	}
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != quickDialDeleteConfirmID {
		t.Fatalf("delete confirmation modal = %#v", model.modal)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialDeleteConfirmID {
		t.Fatal("first confirmation press should only arm the delete")
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	if len(edits) != 1 || edits[0].KeyPath != "tui.presets.1" || edits[0].Value != nil {
		t.Fatalf("delete edits = %#v", edits)
	}
	if _, ok := model.quickDials["1"]; ok {
		t.Fatalf("deleted preset still live: %#v", model.quickDials)
	}
	if !strings.Contains(model.notice, "Deleted preset 1") {
		t.Fatalf("notice = %q, want deleted-preset confirmation", model.notice)
	}
}

func TestQuickDialEditPickerReopensEditorAndPreservesLabelLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "old prompt", Label: quickDialPtr("Old label")},
		},
		OnWriteSettings: func(writes []SettingsEdit) (SettingsWriteResult, error) {
			edits = append(edits, writes...)
			return SettingsWriteResult{
				QuickDials: map[string]config.TUIPreset{
					"1": {Input: "old prompt", Label: quickDialPtr("Old label")},
				},
			}, nil
		},
	})
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("e"))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialEditPickerID {
		t.Fatalf("edit picker modal = %#v", model.modal)
	}
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.customPrompt == nil {
		t.Fatalf("edit editor modal = %#v", model.modal)
	}
	if model.modal.customPrompt.Text != "old prompt" || model.modal.title != "Edit preset 1" {
		t.Fatalf("editor text/title = %q/%q", model.modal.customPrompt.Text, model.modal.title)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialReplaceConfirmID {
		t.Fatalf("replace confirmation modal = %#v", model.modal)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialReplaceConfirmID {
		t.Fatal("first replacement confirmation press should only arm the write")
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(edits) != 1 || edits[0].KeyPath != "tui.presets.1" {
		t.Fatalf("edit writes = %#v", edits)
	}
	value, ok := edits[0].Value.(map[string]any)
	if !ok || value["input"] != "old prompt" || value["label"] != "Old label" {
		t.Fatalf("edited preset value = %#v", edits[0].Value)
	}
}

func TestQuickDialSavePickerReplacesOccupiedSlotAfterConfirmationLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := NewModel(quickDialTestState(), Options{
		Width:  100,
		Height: 30,
		QuickDials: map[string]config.TUIPreset{
			"1": {Input: "old prompt"},
		},
		OnWriteSettings: func(writes []SettingsEdit) (SettingsWriteResult, error) {
			edits = append(edits, writes...)
			return SettingsWriteResult{
				QuickDials: map[string]config.TUIPreset{
					"1": {Input: "new prompt"},
				},
			}, nil
		},
	})
	typeText(t, model, "new prompt")
	model.openQuickDialMenu()
	_, cmd := model.Update(runes("n"))
	runTeaCmd(t, model, cmd)
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialSavePickerID {
		t.Fatalf("save picker modal = %#v", model.modal)
	}
	model.modal.selected = 0
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != quickDialReplaceConfirmID {
		t.Fatalf("occupied slot did not ask for replacement confirmation: %#v", model.modal)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(edits) != 1 || edits[0].KeyPath != "tui.presets.1" {
		t.Fatalf("replace writes = %#v", edits)
	}
	value, ok := edits[0].Value.(map[string]any)
	if !ok || value["input"] != "new prompt" {
		t.Fatalf("replacement value = %#v", edits[0].Value)
	}
}
