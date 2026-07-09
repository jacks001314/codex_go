package chatcomposer

import "strings"

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer/draft_state.rs.

type DraftState struct {
	Text                            string
	Cursor                          int
	IsBashMode                      bool
	PendingPastes                   []PendingPaste
	InputEnabled                    bool
	InputDisabledPlaceholder        string
	DisablePasteBurst               bool
	MentionBindings                 map[uint64]ComposerMentionBinding
	RecentSubmissionMentionBindings []MentionBinding
}

type PendingPaste struct {
	Placeholder string
	Content     string
}

type ComposerMentionBinding struct {
	Sigil   rune
	Mention string
	Path    string
}

type MentionBinding struct {
	Sigil   rune
	Mention string
	Path    string
}

func NewDraftState() *DraftState {
	return &DraftState{
		InputEnabled:    true,
		MentionBindings: map[uint64]ComposerMentionBinding{},
	}
}

func (d *DraftState) SetText(text string) {
	if d == nil {
		return
	}
	d.Text = text
	d.Cursor = len(text)
}

func (d *DraftState) Insert(text string) {
	if d == nil || text == "" || !d.InputEnabled {
		return
	}
	d.clampCursor()
	d.Text = d.Text[:d.Cursor] + text + d.Text[d.Cursor:]
	d.Cursor += len(text)
}

func (d *DraftState) InsertElement(placeholder string) {
	if placeholder == "" {
		return
	}
	d.Insert(placeholder)
}

func (d *DraftState) ReplaceElementPayload(oldPayload string, newPayload string) bool {
	if d == nil || oldPayload == "" || oldPayload == newPayload {
		return false
	}
	idx := strings.Index(d.Text, oldPayload)
	if idx < 0 {
		return false
	}
	d.Text = d.Text[:idx] + newPayload + d.Text[idx+len(oldPayload):]
	switch {
	case d.Cursor <= idx:
	case d.Cursor <= idx+len(oldPayload):
		d.Cursor = idx + len(newPayload)
	default:
		d.Cursor += len(newPayload) - len(oldPayload)
	}
	d.clampCursor()
	return true
}

func (d *DraftState) SetInputEnabled(enabled bool, placeholder string) {
	if d == nil {
		return
	}
	d.InputEnabled = enabled
	if enabled {
		d.InputDisabledPlaceholder = ""
	} else {
		d.InputDisabledPlaceholder = placeholder
	}
}

func (d *DraftState) AddPendingPaste(placeholder string, content string) {
	if d != nil && placeholder != "" {
		d.PendingPastes = append(d.PendingPastes, PendingPaste{Placeholder: placeholder, Content: content})
	}
}

func (d *DraftState) TakePendingPastes() []PendingPaste {
	if d == nil {
		return nil
	}
	out := append([]PendingPaste(nil), d.PendingPastes...)
	d.PendingPastes = nil
	return out
}

func (d *DraftState) AddMentionBinding(id uint64, binding ComposerMentionBinding) {
	if d == nil {
		return
	}
	if d.MentionBindings == nil {
		d.MentionBindings = map[uint64]ComposerMentionBinding{}
	}
	d.MentionBindings[id] = binding
}

func (d *DraftState) TakeRecentSubmissionMentionBindings() []MentionBinding {
	if d == nil {
		return nil
	}
	out := append([]MentionBinding(nil), d.RecentSubmissionMentionBindings...)
	d.RecentSubmissionMentionBindings = nil
	return out
}

func (d *DraftState) IsEmpty() bool {
	return d == nil || (strings.TrimSpace(d.Text) == "" && len(d.PendingPastes) == 0)
}

func (d *DraftState) Clear() {
	if d == nil {
		return
	}
	d.Text = ""
	d.Cursor = 0
	d.PendingPastes = nil
	d.RecentSubmissionMentionBindings = nil
}

func (d *DraftState) clampCursor() {
	if d.Cursor < 0 {
		d.Cursor = 0
	}
	if d.Cursor > len(d.Text) {
		d.Cursor = len(d.Text)
	}
	for d.Cursor > 0 && d.Cursor < len(d.Text) && (d.Text[d.Cursor]&0xC0) == 0x80 {
		d.Cursor--
	}
}
