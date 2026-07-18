package chatcomposer

import (
	"strings"
	"time"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer/footer_state.rs.

type ComposerFooterMode string

const (
	ComposerFooterModeComposerEmpty        ComposerFooterMode = "composer_empty"
	ComposerFooterModeComposerHasDraft     ComposerFooterMode = "composer_has_draft"
	ComposerFooterModeShortcutOverlay      ComposerFooterMode = "shortcut_overlay"
	ComposerFooterModeQuitShortcutReminder ComposerFooterMode = "quit_shortcut_reminder"
	ComposerFooterModeEscHint              ComposerFooterMode = "esc_hint"
	ComposerFooterModeHistorySearch        ComposerFooterMode = "history_search"
)

type FooterState struct {
	Hint                         string
	QuitShortcutExpiresAt        *time.Time
	QuitShortcutKey              string
	EscBacktrackHint             bool
	UseShiftEnterHint            bool
	Mode                         ComposerFooterMode
	HintOverride                 []FooterHintOverride
	PlanModeNudgeVisible         bool
	Flash                        *FooterFlash
	ContextWindowPercent         *int64
	ContextWindowUsedTokens      *int64
	CollaborationModeIndicator   string
	GoalStatusIndicator          string
	IDEContextActive             bool
	StatusLineValue              string
	StatusLineHyperlinkURL       string
	StatusLineEnabled            bool
	SideConversationContextLabel string
	ActiveAgentLabel             string
	ExternalEditorKey            string
	ShowTranscriptKey            string
	InsertNewlineKey             string
	QueueKey                     string
	ToggleShortcutsKey           string
	HistorySearchKey             string
	ReasoningDownKey             string
	ReasoningUpKey               string
}

type FooterHintOverride struct {
	Key  string
	Hint string
}

type FooterFlash struct {
	Line      string
	ExpiresAt time.Time
}

func NewFooterState() FooterState {
	return FooterState{
		Mode:               ComposerFooterModeComposerEmpty,
		QuitShortcutKey:    "Ctrl+C",
		ExternalEditorKey:  "Ctrl+G",
		ShowTranscriptKey:  "Ctrl+T",
		InsertNewlineKey:   "Ctrl+J",
		QueueKey:           "Tab",
		ToggleShortcutsKey: "?",
		HistorySearchKey:   "Ctrl+R",
		ReasoningDownKey:   "Alt+,",
		ReasoningUpKey:     "Alt+.",
	}
}

func (f FooterState) FlashVisibleAt(now time.Time) bool {
	return f.Flash != nil && now.Before(f.Flash.ExpiresAt)
}

func (f FooterState) FlashVisible() bool {
	return f.FlashVisibleAt(time.Now())
}

func (f *FooterState) ShowFlash(line string, duration time.Duration, now time.Time) {
	if f == nil {
		return
	}
	f.Flash = &FooterFlash{Line: line, ExpiresAt: now.Add(duration)}
}

func (f FooterState) StatusLineText() (string, bool) {
	if strings.TrimSpace(f.StatusLineValue) == "" {
		return "", false
	}
	return f.StatusLineValue, true
}

func (f FooterState) ContextIndicators() []string {
	var out []string
	if f.StatusLineEnabled && f.StatusLineValue != "" {
		out = append(out, f.StatusLineValue)
	}
	if f.ActiveAgentLabel != "" {
		out = append(out, f.ActiveAgentLabel)
	}
	if f.SideConversationContextLabel != "" {
		out = append(out, f.SideConversationContextLabel)
	}
	if f.GoalStatusIndicator != "" {
		out = append(out, f.GoalStatusIndicator)
	}
	if f.IDEContextActive {
		out = append(out, "IDE context")
	}
	return out
}

func (f FooterState) ContextLine() string {
	return strings.Join(f.ContextIndicators(), " \u00b7 ")
}

func (f FooterState) QuitShortcutActiveAt(now time.Time) bool {
	return f.QuitShortcutExpiresAt != nil && now.Before(*f.QuitShortcutExpiresAt)
}

func (f *FooterState) SetQuitShortcutReminder(key string, duration time.Duration, now time.Time) {
	if f == nil {
		return
	}
	if key == "" {
		key = "Ctrl+C"
	}
	expires := now.Add(duration)
	f.QuitShortcutKey = key
	f.QuitShortcutExpiresAt = &expires
	f.Mode = ComposerFooterModeQuitShortcutReminder
}

func (f *FooterState) ClearExpiredTransientState(now time.Time) bool {
	if f == nil {
		return false
	}
	changed := false
	if f.Flash != nil && !now.Before(f.Flash.ExpiresAt) {
		f.Flash = nil
		changed = true
	}
	if f.QuitShortcutExpiresAt != nil && !now.Before(*f.QuitShortcutExpiresAt) {
		f.QuitShortcutExpiresAt = nil
		if f.Mode == ComposerFooterModeQuitShortcutReminder {
			f.Mode = ComposerFooterModeComposerEmpty
		}
		changed = true
	}
	return changed
}
