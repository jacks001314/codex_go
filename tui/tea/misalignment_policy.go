package tea

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

const (
	misalignmentPrecautionID = "misalignment-precaution"
	misalignmentReviewID     = "misalignment-review"
)

// misalignmentReview is the bounded, current-thread review state supplied by a
// resumable misalignment error (Rust #42410 MisalignmentReview).
type misalignmentReview struct {
	ThreadID string
	TurnID   string
	Details  appserver.MisalignmentErrorDetails
	Serial   uint64
}

func (r *misalignmentReview) continuationMessage() (string, bool) {
	if r == nil || r.Details.Steer == nil {
		return "", false
	}
	message := strings.TrimSpace(r.Details.Steer.Message)
	if message == "" || len(message) > 1024 {
		return "", false
	}
	return message, true
}

func parseMisalignmentReview(message string, threadID string, serial uint64) *misalignmentReview {
	var payload struct {
		TurnID       string                              `json:"turnId"`
		Misalignment *appserver.MisalignmentErrorDetails `json:"misalignment"`
		Error        struct {
			TurnID       string                              `json:"turnId"`
			Misalignment *appserver.MisalignmentErrorDetails `json:"misalignment"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(message), &payload); err != nil {
		return nil
	}
	details := payload.Misalignment
	turnID := strings.TrimSpace(payload.TurnID)
	if payload.Error.Misalignment != nil {
		details = payload.Error.Misalignment
		turnID = firstNonEmpty(strings.TrimSpace(payload.Error.TurnID), turnID)
	}
	if details == nil || details.DetailedExplanation == nil {
		return nil
	}
	explanation := strings.TrimSpace(*details.DetailedExplanation)
	if explanation == "" || len(*details.DetailedExplanation) > 64*1024 {
		return nil
	}
	return &misalignmentReview{
		ThreadID: strings.TrimSpace(threadID),
		TurnID:   turnID,
		Details:  *details,
		Serial:   serial,
	}
}

func (m *Model) onMisalignmentError(message string) {
	if m == nil {
		return
	}
	m.misalignmentReviewSerial++
	m.misalignmentReview = parseMisalignmentReview(message, m.currentThreadID(), m.misalignmentReviewSerial)
}

func (m *Model) isCurrentMisalignmentReview(review *misalignmentReview) bool {
	if m == nil ||
		review == nil ||
		!m.misalignmentPolicyStopped ||
		m.misalignmentReview != review ||
		m.currentThreadID() != review.ThreadID {
		return false
	}
	if turnID := strings.TrimSpace(m.currentTurnID); turnID != "" && review.TurnID != "" {
		return turnID == review.TurnID
	}
	return true
}

func (m *Model) showMisalignmentPrecaution() bubbletea.Cmd {
	if m == nil || !m.misalignmentPolicyStopped {
		return nil
	}
	options := make([]ModalOption, 0, 4)
	if m.misalignmentReview != nil {
		options = append(options, ModalOption{
			ID:    "review",
			Label: "Review findings",
		})
	}
	if m.remoteAppServer {
		options = append(options, ModalOption{
			ID:    "agents",
			Label: "Agent command center",
		})
	}
	options = append(options,
		ModalOption{ID: "new_chat", Label: "New chat"},
		ModalOption{ID: "resume", Label: "Resume another chat"},
	)
	body := "We couldn’t confirm the agent was acting safely and following your instructions. To continue working, start or resume another chat."
	if m.misalignmentReview != nil {
		body = "We couldn’t confirm the agent was interpreting your instructions correctly. Review what we detected before deciding to continue."
	}
	m.openModal(ModalRequestMsg{
		ID:      misalignmentPrecautionID,
		Kind:    ModalKindMisalignment,
		Title:   "Chat paused as a precaution",
		Body:    body,
		Options: options,
	})
	return nil
}

func (m *Model) openMisalignmentReview() bubbletea.Cmd {
	review := m.misalignmentReview
	if !m.isCurrentMisalignmentReview(review) {
		return nil
	}
	message, canContinue := review.continuationMessage()
	lines := make([]string, 0, 4)
	if canContinue {
		lines = append(lines, "Continuation request (quoted)", strconv.Quote(message), "")
	}
	if review.Details.DetailedExplanation != nil {
		lines = append(lines, strings.TrimSpace(*review.Details.DetailedExplanation))
	}
	options := []ModalOption{
		{
			ID:                  "continue",
			Label:               "Acknowledge findings and continue",
			Disabled:            !canContinue,
			DisabledReason:      "Continuation instructions are unavailable.",
			RequireConfirmation: true,
		},
		{ID: "back", Label: "Back"},
	}
	m.openModal(ModalRequestMsg{
		ID:      misalignmentReviewID,
		Kind:    ModalKindMisalignment,
		Title:   "What we detected",
		Body:    strings.Join(lines, "\n"),
		Options: options,
	})
	return nil
}

func (m *Model) applyMisalignmentModalOption(id string, optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch id {
	case misalignmentPrecautionID:
		switch optionID {
		case "review":
			return m.openMisalignmentReview()
		case "agents":
			m.clearMisalignmentState()
			return m.applyAgentsCommand()
		case "new_chat":
			m.clearMisalignmentState()
			m.startFreshNamedSession("", "Started a new local thread.")
		case "resume":
			m.clearMisalignmentState()
			return m.openSessionPicker(codextui.SessionPickerResume)
		}
	case misalignmentReviewID:
		switch optionID {
		case "continue":
			return m.continueMisalignment()
		case "back":
			return m.showMisalignmentPrecaution()
		}
	}
	return nil
}

func (m *Model) continueMisalignment() bubbletea.Cmd {
	review := m.misalignmentReview
	if !m.isCurrentMisalignmentReview(review) {
		return nil
	}
	message, ok := review.continuationMessage()
	if !ok {
		m.notice = "Couldn’t continue this chat. Review its latest status before trying again."
		m.addErrorHistoryMessage(m.notice)
		m.showMisalignmentPrecaution()
		return nil
	}
	metadata := map[string]string{
		"misalignment_override": fmt.Sprintf(`{"timestamp":%d}`, time.Now().UnixMilli()),
	}
	m.clearMisalignmentState()
	return m.submitRequest(SubmitRequest{
		Prompt:                     message,
		ResponsesAPIClientMetadata: metadata,
	}, false)
}

func (m *Model) clearMisalignmentState() {
	if m == nil {
		return
	}
	m.misalignmentPolicyStopped = false
	m.misalignmentReview = nil
	m.composer.Focus()
}

// clearMisalignmentForNewTurn mirrors Rust's clear_misalignment_for_new_turn:
// a new server turn with a different id supersedes a stale stopped-chat view.
func (m *Model) clearMisalignmentForNewTurn(turnID string) {
	if m == nil || !m.misalignmentPolicyStopped || m.misalignmentReview == nil {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if m.misalignmentReview.TurnID != "" && m.misalignmentReview.TurnID != turnID {
		m.clearMisalignmentState()
		if m.modal != nil && m.modal.kind == ModalKindMisalignment {
			m.modal = nil
		}
	}
}
