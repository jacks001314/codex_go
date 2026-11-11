package tea

import (
	"fmt"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	bottompane "codex_go/tui/bottom_pane"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/keymsg"
)

type feedbackCategory string

const (
	feedbackCategoryBug         feedbackCategory = "bug"
	feedbackCategoryBadResult   feedbackCategory = "bad_result"
	feedbackCategoryGoodResult  feedbackCategory = "good_result"
	feedbackCategorySafetyCheck feedbackCategory = "safety_check"
	feedbackCategoryOther       feedbackCategory = "other"
)

type feedbackStage string

const (
	feedbackStageDisabled feedbackStage = "disabled"
	feedbackStageCategory feedbackStage = "category"
	feedbackStageConsent  feedbackStage = "consent"
	feedbackStageNote     feedbackStage = "note"
)

// feedbackDestination selects where a submitted report goes (Rust #60542
// FeedbackDestination): the OpenAI upload, or a local diagnostic ZIP.
type feedbackDestination string

const (
	feedbackDestinationOpenAI   feedbackDestination = "openai"
	feedbackDestinationLocalZip feedbackDestination = "local_zip"
)

type feedbackModalState struct {
	stage       feedbackStage
	category    feedbackCategory
	includeLogs bool
	note        string
	// pasteBurst mirrors Rust #45116: a rapid unbracketed paste of a multiline
	// note inserts newlines instead of submitting at the first newline, and the
	// burst window keeps Enter from submitting until the paste has settled.
	pasteBurst bottompane.PasteBurst
}

func (m *Model) openFeedbackFlow() {
	if m == nil {
		return
	}
	if !m.feedbackEnabled && !m.feedbackLocalSaveAvailable {
		m.openModal(ModalRequestMsg{
			ID:      "feedback-disabled",
			Kind:    ModalKindFeedback,
			Title:   "Sending feedback is disabled",
			Body:    "This action is disabled by configuration.",
			Options: []ModalOption{{ID: "close", Label: "Close"}},
		})
		m.modal.feedback = &feedbackModalState{stage: feedbackStageDisabled}
		return
	}
	m.openModal(ModalRequestMsg{
		ID:      "feedback-category",
		Kind:    ModalKindFeedback,
		Title:   "How was this?",
		Options: feedbackCategoryOptions(),
	})
	m.modal.feedback = &feedbackModalState{stage: feedbackStageCategory}
}

// openFeedbackConsentForCategory opens the consent step directly for a
// category chosen by another surface (Rust AppEvent::OpenFeedbackConsent).
func (m *Model) openFeedbackConsentForCategory(category feedbackCategory) {
	if m == nil {
		return
	}
	m.modal = nil
	m.openModal(ModalRequestMsg{ID: "feedback-consent", Kind: ModalKindFeedback})
	m.modal.feedback = &feedbackModalState{category: category}
	m.openFeedbackConsent(m.modal.feedback)
}

func feedbackCategoryOptions() []ModalOption {
	return []ModalOption{
		{ID: string(feedbackCategoryBug), Label: "bug", Description: "Crash, error message, hang, or broken UI/behavior."},
		{ID: string(feedbackCategoryBadResult), Label: "bad result", Description: "Output was off-target, incorrect, incomplete, or unhelpful."},
		{ID: string(feedbackCategoryGoodResult), Label: "good result", Description: "Helpful, correct, high-quality, or delightful result worth celebrating."},
		{ID: string(feedbackCategorySafetyCheck), Label: "safety check", Description: "Benign usage blocked due to safety checks or refusals."},
		{ID: string(feedbackCategoryOther), Label: "other", Description: "Slowness, feature suggestion, UX feedback, or anything else."},
	}
}

func (m *Model) updateFeedbackModal(message keymsg.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.feedback == nil {
		return nil
	}
	state := m.modal.feedback
	if state.stage == feedbackStageNote {
		return m.updateFeedbackNote(message)
	}
	switch message.Type {
	case keymsg.KeyEsc, keymsg.KeyCtrlC:
		m.modal = nil
		return nil
	case keymsg.KeyUp:
		m.moveModalSelection(-1)
		return nil
	case keymsg.KeyDown, keymsg.KeyTab:
		m.moveModalSelection(1)
		return nil
	case keymsg.KeyEnter:
		switch state.stage {
		case feedbackStageDisabled:
			m.modal = nil
		case feedbackStageCategory:
			state.category = feedbackCategory(m.modal.options[m.modal.selected].ID)
			m.openFeedbackConsent(state)
		case feedbackStageConsent:
			state.includeLogs = m.modal.selected == 0
			m.openFeedbackNote(state)
		}
	}
	return nil
}

func (m *Model) openFeedbackConsent(state *feedbackModalState) {
	m.modal.id = "feedback-consent"
	// Rust #60542: the same consent choice now covers both destinations.
	m.modal.title = "Include logs and session history?"
	disclosure := "Files may contain prompts, commands, and tool output:"
	if m.feedbackLocalSaveAvailable {
		disclosure = "Saving a ZIP does not submit feedback to OpenAI.\n" + disclosure
	}
	m.modal.body = strings.Join([]string{
		disclosure,
		"  - codex-logs.log",
		// Rust #47889: the TUI's own client log is staged for remote uploads.
		"  - client-logs.txt (if available)",
		"  - codex-doctor-report.json",
		"  - codex-apps-tools-cache.json (if available)",
		"  - codex-app-directory-cache.json (if available)",
	}, "\n")
	m.modal.options = []ModalOption{
		{ID: "yes", Label: "Yes", Description: "Share the current Codex session logs and diagnostics with the team for troubleshooting."},
		{ID: "no", Label: "No"},
	}
	m.modal.selected = 0
	state.stage = feedbackStageConsent
}

func (m *Model) openFeedbackNote(state *feedbackModalState) {
	m.modal.id = "feedback-note"
	m.modal.title = feedbackNoteTitle(state.category)
	m.modal.body = ""
	m.modal.options = []ModalOption{{ID: "submit", Label: "Submit"}}
	m.modal.selected = 0
	state.stage = feedbackStageNote
}

func feedbackNoteTitle(category feedbackCategory) string {
	label := strings.ReplaceAll(string(category), "_", " ")
	return "Tell us more (" + label + ")"
}

func (m *Model) updateFeedbackNote(message keymsg.KeyMsg) bubbletea.Cmd {
	state := m.modal.feedback
	now := m.currentTime()
	switch message.Type {
	case keymsg.KeyEsc, keymsg.KeyCtrlC:
		m.modal = nil
	case keymsg.KeyCtrlS:
		// Rust #60542: Ctrl+S saves a diagnostic ZIP locally, without uploading.
		if m.feedbackLocalSaveAvailable {
			m.modal = nil
			return m.submitFeedback(state.category, state.note, state.includeLogs, feedbackDestinationLocalZip)
		}
	case keymsg.KeyEnter:
		if !m.disablePasteBurst && state.pasteBurst.DirectInsertNewlineShouldInsert(now) {
			// A newline inside a rapid paste burst is note content, not submit.
			state.pasteBurst.ExtendWindow(now)
			state.note += "\n"
			return nil
		}
		if message.Alt {
			state.note += "\n"
			return nil
		}
		if !m.feedbackEnabled {
			// Rust #60542: Enter only sends while OpenAI uploads are enabled.
			return nil
		}
		m.modal = nil
		return m.submitFeedback(state.category, state.note, state.includeLogs, feedbackDestinationOpenAI)
	case keymsg.KeyCtrlJ:
		state.note += "\n"
		state.pasteBurst.ClearAfterExplicitPaste()
	case keymsg.KeyTab:
		inBurst := !m.disablePasteBurst && state.pasteBurst.DirectInsertNewlineShouldInsert(now)
		state.note += "\t"
		if inBurst {
			state.pasteBurst.ExtendWindow(now)
		}
	case keymsg.KeyBackspace:
		runes := []rune(state.note)
		if len(runes) > 0 {
			state.note = string(runes[:len(runes)-1])
		}
		state.pasteBurst.ClearAfterExplicitPaste()
	case keymsg.KeyRunes:
		if message.Paste {
			// A bracketed paste is explicit input: keep it whole and end any
			// burst so a following Enter submits immediately.
			state.note += string(message.Runes)
			state.pasteBurst.ClearAfterExplicitPaste()
			return nil
		}
		if message.Alt {
			state.note += string(message.Runes)
			state.pasteBurst.ClearAfterExplicitPaste()
			return nil
		}
		pasteLike := false
		if !m.disablePasteBurst {
			_, pasteLike = state.pasteBurst.OnPlainCharNoHold(now)
		}
		state.note += string(message.Runes)
		if pasteLike {
			state.pasteBurst.ExtendWindow(now)
		}
	default:
		state.pasteBurst.ClearAfterExplicitPaste()
	}
	return nil
}

func (m *Model) renderFeedbackNoteModal() string {
	state := m.modal.feedback
	placeholder := "(optional) Write a short description to help us further"
	if state.category == feedbackCategorySafetyCheck {
		placeholder = "(optional) Share what was refused and why it should have been allowed"
	}
	text := state.note
	if text == "" {
		text = placeholder
	}
	lines := []string{feedbackNoteTitle(state.category)}
	if !m.feedbackEnabled {
		lines = append(lines, "  Feedback uploads are disabled.")
	}
	if m.feedbackLocalSaveAvailable {
		lines = append(lines, "  You can save feedback to a ZIP file, which is not sent to OpenAI.")
	}
	lines = append(lines, "  "+text, "", m.feedbackNoteHintLine())
	return strings.Join(lines, "\n")
}

func (m *Model) submitFeedback(category feedbackCategory, note string, includeLogs bool, destination feedbackDestination) bubbletea.Cmd {
	params := appserver.FeedbackUploadParams{
		Classification: string(category),
		IncludeLogs:    includeLogs,
	}
	if reason := strings.TrimSpace(note); reason != "" {
		params.Reason = &reason
	}
	if m.State != nil {
		if threadID := strings.TrimSpace(m.State.ThreadID); threadID != "" {
			params.ThreadID = &threadID
		}
	}
	if destination == feedbackDestinationLocalZip {
		save := m.onSaveFeedback
		if save == nil {
			return func() bubbletea.Msg {
				return FeedbackSubmitResultMsg{Category: category, Destination: destination, IncludeLogs: includeLogs, Err: fmt.Errorf("Update the Codex server to save a feedback ZIP")}
			}
		}
		return func() bubbletea.Msg {
			// The save path is owned by the host handler: it resolves the app
			// server's Codex home and names the archive (Rust #60542).
			response, err := save(appserver.FeedbackSaveParams{FeedbackUploadParams: params})
			return FeedbackSubmitResultMsg{
				Category:    category,
				IncludeLogs: includeLogs,
				Destination: destination,
				SavedPath:   response.SavedPath,
				Err:         err,
			}
		}
	}
	if m.onSubmitFeedback == nil {
		return func() bubbletea.Msg {
			return FeedbackSubmitResultMsg{Category: category, IncludeLogs: includeLogs, Err: fmt.Errorf("feedback/upload is unavailable in this runtime")}
		}
	}
	submit := m.onSubmitFeedback
	return func() bubbletea.Msg {
		response, err := submit(params)
		return FeedbackSubmitResultMsg{Category: category, IncludeLogs: includeLogs, Destination: destination, Response: response, Err: err}
	}
}

// feedbackNoteHintLine mirrors Rust #60542's action hints: the save action only
// appears when a local ZIP is available, the send action only when OpenAI
// uploads are enabled, and the labels shorten on narrow terminals.
func (m *Model) feedbackNoteHintLine() string {
	long := m.feedbackNoteHintActions("save ZIP", "send to OpenAI")
	if len([]rune(long)) <= 72 {
		return long
	}
	return m.feedbackNoteHintActions("save", "send")
}

func (m *Model) feedbackNoteHintActions(save string, send string) string {
	actions := make([]string, 0, 3)
	if m.feedbackLocalSaveAvailable {
		actions = append(actions, "Ctrl+S "+save)
	}
	if m.feedbackEnabled {
		actions = append(actions, "Enter "+send)
	}
	actions = append(actions, "Esc cancel")
	return strings.Join(actions, "  ")
}

func (m *Model) applyFeedbackSubmitResult(message FeedbackSubmitResultMsg) {
	if message.Err != nil {
		if message.Destination == feedbackDestinationLocalZip {
			m.applyHistoryCell(historycell.NewErrorEvent("Failed to save feedback ZIP: " + message.Err.Error()))
			return
		}
		m.applyHistoryCell(historycell.NewErrorEvent("Failed to submit feedback: " + message.Err.Error()))
		return
	}
	if message.Destination == feedbackDestinationLocalZip {
		// Rust #60542 shows the archive path on completion.
		path := strings.TrimSpace(message.SavedPath)
		if path == "" {
			path = "(unknown path)"
		}
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{"Feedback ZIP saved to " + path}))
		return
	}
	threadID := strings.TrimSpace(message.Response.ThreadID)
	if threadID == "" && m.State != nil {
		threadID = strings.TrimSpace(m.State.ThreadID)
	}
	prefix := "Feedback uploaded."
	if !message.IncludeLogs {
		prefix = "Feedback recorded (no logs)."
	}
	lines := []string{prefix}
	if message.Category == feedbackCategoryGoodResult {
		lines[0] += " Thanks for the feedback!"
		if threadID != "" {
			lines = append(lines, "", "  Thread ID: "+threadID)
		}
	} else {
		url := "https://github.com/openai/codex/issues/new?template=3-cli.yml&steps=Uploaded%20thread:%20" + threadID
		lines[0] += " Please open an issue using the following URL:"
		lines = append(lines, "", "  "+url)
		if threadID != "" {
			lines = append(lines, "", "  Or mention your thread ID "+threadID+" in an existing issue.")
		}
	}
	m.applyHistoryCell(historycell.NewPlainHistoryCell(lines))
}
