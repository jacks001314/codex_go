package tea

import (
	"context"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

func statuslineProviderDiscovered(t *testing.T, candidates ...StatuslineProviderCandidate) StatuslineProviderDiscoveryFunc {
	t.Helper()
	return func(context.Context, StatuslineProviderDiscoveryRequest) <-chan StatuslineProviderDiscoveryUpdate {
		updates := make(chan StatuslineProviderDiscoveryUpdate, 1)
		updates <- StatuslineProviderDiscoveryUpdate{Server: "fixture", Providers: candidates}
		close(updates)
		return updates
	}
}

func statuslineProviderPickerModel(
	t *testing.T,
	selected *config.StatusLineProviderSelection,
	discover StatuslineProviderDiscoveryFunc,
	writes *[]SettingsEdit,
) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-provider")
	state.SetStatus("idle")
	state.CWD = t.TempDir()
	model := NewModel(state, Options{
		Width:              120,
		Height:             30,
		FeatureSettings:    map[string]bool{"tui_extensions_statusline": true},
		StatusLineProvider: selected,
		OnResolveStatuslineProvider: func(context.Context, StatuslineProviderReadRequest) (StatuslineProvider, error) {
			return StatuslineProvider{Found: true, Title: "Fixture Line", URI: "ui://fixture/line"}, nil
		},
		OnReadStatuslineProvider: func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return statuslineProviderContents(`"ready"`), nil
		},
		OnDiscoverStatuslineProviders: discover,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			*writes = append(*writes, edits...)
			result := SettingsWriteResult{}
			for _, edit := range edits {
				if edit.KeyPath == statusLineProviderConfigKey {
					result.StatusLineProvider = &config.StatusLineProviderSelection{
						Server:  editValueString(edit.Value, "server"),
						Tool:    editValueString(edit.Value, "tool"),
						Default: editValueString(edit.Value, "default") != "",
					}
				}
			}
			return result, nil
		},
	})
	model.width, model.height = 120, 30
	return model
}

func editValueString(value any, key string) string {
	fields, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	text, _ := fields[key].(string)
	return text
}

// Rust #61988: the item editor offers the provider picker only when status line
// extensions are enabled.
func TestStatusLineSetupOffersProviderPickerLikeRust(t *testing.T) {
	model := statuslineProviderPickerModel(t, nil, nil, &[]SettingsEdit{})
	model.openStatusLineSetup()
	if model.modal == nil || model.modal.kind != ModalKindStatusLine {
		t.Fatalf("status line setup modal = %#v", model.modal)
	}
	if model.modal.options[0].ID != statuslineProviderPickerActionID {
		t.Fatalf("first option = %q", model.modal.options[0].ID)
	}
	// Without a discovery hook the picker still opens; only the subtitle and
	// provider rows depend on discovery.
	model.toggleStatusSetupSelection()
	if model.modal == nil || model.modal.kind != ModalKindStatuslineProvider {
		t.Fatalf("provider picker modal = %#v", model.modal)
	}
	if model.statuslinePicker == nil || model.statuslinePicker.editor == nil {
		t.Fatal("picker did not stage the item editor underneath")
	}

	plain := statuslineProviderPickerModel(t, nil, nil, &[]SettingsEdit{})
	plain.featureSettings = map[string]bool{}
	plain.openStatusLineSetup()
	for _, option := range plain.modal.options {
		if option.ID == statuslineProviderPickerActionID {
			t.Fatal("disabled extension gate still offered the provider picker")
		}
	}
}

// Rust #61988 statusline_picker_tests: discovery keeps the highlighted row and
// ignores results from a retired scan.
func TestStatuslineProviderPickerDiscoveryKeepsHighlightLikeRust(t *testing.T) {
	writes := []SettingsEdit{}
	discover := statuslineProviderDiscovered(t,
		StatuslineProviderCandidate{Server: "fixture", Tool: "zeta", Title: "Zeta"},
		StatuslineProviderCandidate{Server: "fixture", Tool: "line", Title: "Fixture Line"},
		StatuslineProviderCandidate{Server: "fixture", Tool: "alpha", Title: "Alpha"},
	)
	selected := &config.StatusLineProviderSelection{Server: "fixture", Tool: "line"}
	model := statuslineProviderPickerModel(t, selected, discover, &writes)
	model.openStatusLineSetup()
	cmd := model.toggleStatusSetupSelection()
	if model.modal.body != "Discovering MCP status line providers…" {
		t.Fatalf("discovering subtitle = %q", model.modal.body)
	}
	// The user highlights the default row while discovery is in flight.
	model.modal.selected = 1
	model.statuslinePicker.cursorID = model.modal.options[1].ID

	model.Update(cmd())
	if model.statuslinePicker.discovering || model.modal.body != "" {
		t.Fatalf("discovery did not settle: %#v", model.modal.body)
	}
	if model.modal.options[model.modal.selected].ID != statuslineProviderDefaultOption {
		t.Fatalf("discovery lost the highlight: %q", model.modal.options[model.modal.selected].ID)
	}
	labels := []string{}
	for _, option := range model.modal.options {
		labels = append(labels, option.Label)
	}
	want := []string{"Fixture Line (current)", "Default status line", "Alpha", "Zeta"}
	if len(labels) != len(want) {
		t.Fatalf("picker rows = %#v", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("picker rows = %#v, want %#v", labels, want)
		}
	}

	stale := statuslinePickerDiscoveryMsg{
		scan:    model.statuslinePicker.scan + 100,
		updates: []StatuslineProviderDiscoveryUpdate{{Providers: []StatuslineProviderCandidate{{Server: "fixture", Tool: "stale", Title: "Stale"}}}},
	}
	model.Update(stale)
	for _, option := range model.modal.options {
		if option.Label == "Stale" {
			t.Fatal("retired discovery scan still merged rows")
		}
	}
}

// Rust #61988: choosing a provider saves it and returns to the item editor, and
// the default row works while disconnected.
func TestStatuslineProviderPickerSelectsAndReturnsToEditorLikeRust(t *testing.T) {
	writes := []SettingsEdit{}
	discover := statuslineProviderDiscovered(t,
		StatuslineProviderCandidate{Server: "fixture", Tool: "zeta", Title: "Zeta"},
	)
	model := statuslineProviderPickerModel(t, nil, discover, &writes)
	model.openStatusLineSetup()
	discovery := model.toggleStatusSetupSelection()
	model.Update(discovery())

	// Default row is index 0 when nothing is saved.
	if model.modal.options[0].ID != statuslineProviderDefaultOption {
		t.Fatalf("first row = %q", model.modal.options[0].ID)
	}
	model.modal.selected = 1
	save := model.activateModalOption(1)
	if model.modal == nil || model.modal.kind != ModalKindStatusLine {
		t.Fatalf("picker did not return to the item editor: %#v", model.modal)
	}
	if save == nil {
		t.Fatal("provider choice did not save the selection")
	}
	model.Update(save())
	if len(writes) != 1 || writes[0].KeyPath != statusLineProviderConfigKey {
		t.Fatalf("settings writes = %#v", writes)
	}
	if model.StatusLineProvider() == nil || model.StatusLineProvider().Tool != "zeta" {
		t.Fatalf("saved provider = %#v", model.StatusLineProvider())
	}

	// Disconnected: provider rows are unavailable, the default row still works.
	offline := statuslineProviderPickerModel(t, &config.StatusLineProviderSelection{Server: "fixture", Tool: "zeta"}, discover, &writes)
	offline.statuslineDisconnected = true
	offline.openStatusLineSetup()
	offline.toggleStatusSetupSelection()
	if offline.modal.body != "Plugin providers are unavailable while disconnected." {
		t.Fatalf("offline subtitle = %q", offline.modal.body)
	}
	for _, option := range offline.modal.options {
		switch option.ID {
		case statuslineProviderDefaultOption:
			if option.Disabled {
				t.Fatal("offline picker disabled the default row")
			}
		default:
			if !option.Disabled {
				t.Fatalf("offline picker enabled %q", option.ID)
			}
		}
	}
}

// Rust #61988: numeric shortcuts cover only the first nine selectable rows.
func TestStatuslineProviderPickerNumericShortcutsLikeRust(t *testing.T) {
	candidates := []StatuslineProviderCandidate{}
	for i := 0; i < 12; i++ {
		candidates = append(candidates, StatuslineProviderCandidate{
			Server: "fixture",
			Tool:   "tool-" + string(rune('a'+i)),
			Title:  "Tool " + string(rune('A'+i)),
		})
	}
	model := statuslineProviderPickerModel(t, nil, statuslineProviderDiscovered(t, candidates...), &[]SettingsEdit{})
	model.openStatusLineSetup()
	model.Update(model.toggleStatusSetupSelection()())
	// The default row plus twelve providers exceed the nine-row cap.
	shortcut := 0
	for _, option := range model.modal.options {
		if option.Disabled {
			continue
		}
		shortcut++
		want := ""
		if shortcut <= 9 {
			want = string(rune('0' + shortcut))
		}
		if option.Shortcut != want {
			t.Fatalf("row %q shortcut = %q, want %q", option.Label, option.Shortcut, want)
		}
	}
	if shortcut != 13 {
		t.Fatalf("selectable rows = %d", shortcut)
	}
}
