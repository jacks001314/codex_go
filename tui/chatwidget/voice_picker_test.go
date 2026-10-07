package chatwidget

import "testing"

func TestVoicePickerViewMarksCurrentVoice(t *testing.T) {
	view := NewVoicePickerView("cove", []string{"alloy", "cove", "echo"})
	if view.ViewID != VoicePickerViewID {
		t.Fatalf("view id = %q", view.ViewID)
	}
	// Rust #49437 ChatWidget::open_realtime_voices pushes a trailing Back row so
	// the catalog returns to the "Voice settings" root.
	if len(view.Items) != 4 {
		t.Fatalf("items = %#v", view.Items)
	}
	back := view.Items[len(view.Items)-1]
	if back.ID != VoiceSettingsBackOptionID || back.Name != "Back" || !back.DismissOnSelect {
		t.Fatalf("back row = %#v", back)
	}
	current := 0
	for _, item := range view.Items[:len(view.Items)-1] {
		if item.IsCurrent {
			current++
			if item.Name != "cove" {
				t.Fatalf("current item = %#v", item)
			}
		}
		if item.ID != VoicePickerOptionPrefix+item.Name {
			t.Fatalf("item id = %q for %q", item.ID, item.Name)
		}
	}
	if current != 1 {
		t.Fatalf("current markers = %d", current)
	}
	if !view.Searchable || !view.AllowCancel {
		t.Fatalf("view = %#v", view)
	}
	if view.Title != "Select voice" || view.Subtitle != "Applies to your next voice conversation." {
		t.Fatalf("copy drifted from Rust: title=%q subtitle=%q", view.Title, view.Subtitle)
	}
}

func TestVoicePickerViewHandlesAnEmptyCatalog(t *testing.T) {
	view := NewVoicePickerView("cove", nil)
	if len(view.Items) != 2 || !view.Items[0].Disabled {
		t.Fatalf("items = %#v", view.Items)
	}
	if view.Items[1].ID != VoiceSettingsBackOptionID {
		t.Fatalf("empty catalog lost its Back row: %#v", view.Items)
	}
	view = NewVoicePickerView("cove", []string{"  ", ""})
	if len(view.Items) != 2 || !view.Items[0].Disabled {
		t.Fatalf("blank voices produced %#v", view.Items)
	}
}

func TestVoiceFromPickerOption(t *testing.T) {
	if voice, ok := VoiceFromPickerOption("voice:cove"); !ok || voice != "cove" {
		t.Fatalf("option = %q / %v", voice, ok)
	}
	for _, option := range []string{"", "cove", "voice:", "voice:  ", "other:cove"} {
		if voice, ok := VoiceFromPickerOption(option); ok {
			t.Fatalf("option %q produced %q", option, voice)
		}
	}
}
