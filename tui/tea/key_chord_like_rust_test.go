package tea

import (
	"reflect"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func keyChordConfig(t *testing.T, context string, action string, chord string) *codextui.KeymapConfig {
	t.Helper()
	config := codextui.NewKeymapConfig()
	if err := config.Set(context, action, []string{chord}); err != nil {
		t.Fatal(err)
	}
	return config
}

// TestConfiguredKeyChordSubmitsLikeRust covers the runtime half of key chords:
// the first stroke is consumed, the completed two-stroke spec reaches the
// existing composer submit handler, and a timeout or Esc cancels the prefix.
func TestConfiguredKeyChordSubmitsLikeRust(t *testing.T) {
	var requests []SubmitRequest
	model := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: keyChordConfig(t, "composer", "submit", "ctrl-x ctrl-s"),
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})
	typeText(t, model, "send with chord")
	model.Update(key(bubbletea.KeyCtrlX))
	if len(requests) != 0 || model.pendingKeyChordPrefix != "ctrl-x" {
		t.Fatalf("first stroke submitted=%#v pending=%q", requests, model.pendingKeyChordPrefix)
	}
	if !strings.Contains(model.notice, "second key") {
		t.Fatalf("pending chord notice = %q", model.notice)
	}
	model.Update(key(bubbletea.KeyCtrlS))
	if len(requests) != 1 || requests[0].Prompt != "send with chord" {
		t.Fatalf("completed chord requests = %#v", requests)
	}
	if model.pendingKeyChordPrefix != "" {
		t.Fatalf("pending chord leaked: %q", model.pendingKeyChordPrefix)
	}

	timeout := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: keyChordConfig(t, "composer", "submit", "ctrl-x ctrl-s"),
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})
	now := time.Unix(100, 0)
	timeout.now = func() time.Time { return now }
	typeText(t, timeout, "too late")
	timeout.Update(key(bubbletea.KeyCtrlX))
	now = now.Add(codextui.LeaderChordTimeout + time.Second)
	timeout.Update(key(bubbletea.KeyCtrlS))
	if len(requests) != 1 || timeout.pendingKeyChordPrefix != "" {
		t.Fatalf("expired chord requests=%#v pending=%q", requests, timeout.pendingKeyChordPrefix)
	}

	cancelled := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: keyChordConfig(t, "composer", "submit", "ctrl-x ctrl-s"),
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})
	typeText(t, cancelled, "cancel")
	cancelled.Update(key(bubbletea.KeyCtrlX))
	cancelled.Update(key(bubbletea.KeyEsc))
	if len(requests) != 1 || cancelled.pendingKeyChordPrefix != "" {
		t.Fatalf("cancelled chord requests=%#v pending=%q", requests, cancelled.pendingKeyChordPrefix)
	}
}

func TestConfiguredGlobalChordSubmitFallbackLikeRust(t *testing.T) {
	var requests []SubmitRequest
	model := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: keyChordConfig(t, "global", "submit", "ctrl-x ctrl-s"),
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})
	typeText(t, model, "global fallback")
	model.Update(key(bubbletea.KeyCtrlX))
	model.Update(key(bubbletea.KeyCtrlS))
	if len(requests) != 1 || requests[0].Prompt != "global fallback" {
		t.Fatalf("global chord requests = %#v", requests)
	}
}

// TestModelKeymapChordCaptureWritesTwoStrokeSpecLikeRust covers the action-menu
// and modal capture half: two consecutive keys become one normalized binding.
func TestModelKeymapChordCaptureWritesTwoStrokeSpecLikeRust(t *testing.T) {
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
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "set_chord")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.keymapCapture == nil ||
		model.modal.keymapCapture.Intent != keymapCaptureChordReplaceAll {
		t.Fatalf("chord capture modal = %#v", model.modal)
	}
	if !strings.Contains(model.renderModal(), "Press the first key of the chord") {
		t.Fatalf("first-key prompt = %q", model.renderModal())
	}
	model.Update(key(bubbletea.KeyCtrlX))
	if model.modal == nil || model.modal.keymapCapture == nil ||
		model.modal.keymapCapture.ChordFirst != "ctrl-x" ||
		!strings.Contains(model.renderModal(), "First key: ctrl-x") {
		t.Fatalf("second-key state = %#v prompt=%q", model.modal, model.renderModal())
	}
	model.Update(key(bubbletea.KeyCtrlS))
	if len(edits) != 1 || edits[0].Context != "global" || edits[0].Action != "open_transcript" ||
		!reflect.DeepEqual(edits[0].Bindings, []string{"ctrl-x ctrl-s"}) {
		t.Fatalf("chord edits = %#v", edits)
	}
}
