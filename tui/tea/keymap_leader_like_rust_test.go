package tea

import (
	"reflect"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust #52273 (keymap_setup.rs:252-266, keymap_setup/capture.rs:97-98,161-162):
// the action menu offers a leader chord that captures the key following the
// configured leader and stores it as a `leader <key>` spec.
func TestModelKeymapLeaderCaptureWritesLeaderSpecLikeRust(t *testing.T) {
	var edits []codextui.KeymapEdit
	model := NewModel(nil, Options{
		OnKeymapEdit: func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
			edits = append(edits, edit)
			return modelKeymapConfigAfterEdit(t, nil, edit), "saved keymap", nil
		},
	})
	model.openKeymapActionMenu("global", "open_transcript")
	if model.modal == nil || model.modal.id != chatwidget.KeymapActionMenuViewID {
		t.Fatalf("action modal = %#v", model.modal)
	}
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "leader_set")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.keymapCapture == nil || model.modal.keymapCapture.Intent != keymapCaptureLeader {
		t.Fatalf("capture modal = %#v", model.modal)
	}
	if !strings.Contains(model.renderModal(), "Press the key that follows the configured leader. Esc cancels.") {
		t.Fatalf("capture prompt = %q", model.renderModal())
	}
	model.Update(key(bubbletea.KeyCtrlK))
	if len(edits) != 1 || edits[0].Context != "global" || edits[0].Action != "open_transcript" {
		t.Fatalf("leader edits = %#v", edits)
	}
	if !reflect.DeepEqual(edits[0].Bindings, []string{"leader ctrl-k"}) {
		t.Fatalf("leader bindings = %#v, want leader ctrl-k", edits[0].Bindings)
	}
	if model.notice != "saved keymap" {
		t.Fatalf("notice = %q", model.notice)
	}
}

// Rust routes an unbound leader to the leader action instead of capturing a
// chord that could never fire (keymap_setup.rs:266-278).
func TestModelKeymapConfigureLeaderRoutesToLeaderActionLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("chat", "interrupt_turn", []string{"ctrl-x"}); err != nil {
		t.Fatal(err)
	}
	// The ctrl-x alias shadows the default leader, so no prefix is active.
	if prefixes, err := codextui.LeaderKeybindings(config); err != nil || len(prefixes) != 0 {
		t.Fatalf("prefixes = %#v err = %v", prefixes, err)
	}
	model := NewModel(nil, Options{KeymapConfig: config})
	model.openKeymapActionMenu("chat", "interrupt_turn")
	if model.modal == nil || model.modal.id != chatwidget.KeymapActionMenuViewID {
		t.Fatalf("action modal = %#v", model.modal)
	}
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "leader_config")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.id != chatwidget.KeymapActionMenuViewID {
		t.Fatalf("leader modal = %#v", model.modal)
	}
	if model.keymapSelectedContext != "global" || model.keymapSelectedAction != "leader" {
		t.Fatalf("leader target = %s.%s", model.keymapSelectedContext, model.keymapSelectedAction)
	}
	modalOptionIndexByID(t, model.modal.options, "set")
}
