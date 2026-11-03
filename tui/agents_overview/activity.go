package agentsoverview

import (
	"strings"
	"unicode"
)

// Rust parity: codex-rs/tui/src/app/agents_overview_details.rs (#42455). Live
// task details are built from reads the dashboard already made and events it
// already received; observing activity never attaches to a thread or fetches
// additional history.

// AgentsOverviewActivityPreviewChars bounds every activity preview (Rust
// PREVIEW_CHARS).
const AgentsOverviewActivityPreviewChars = 512

// PreviewActivityText mirrors Rust preview_text: whitespace becomes a single
// space, control characters are dropped, and the result is bounded.
func PreviewActivityText(text string) string {
	var builder strings.Builder
	kept := 0
	for _, r := range text {
		if kept >= AgentsOverviewActivityPreviewChars {
			break
		}
		switch {
		case unicode.IsSpace(r):
			builder.WriteRune(' ')
		case unicode.IsControl(r):
			continue
		default:
			builder.WriteRune(r)
		}
		kept++
	}
	return builder.String()
}

// AgentsOverviewEventKind is the subset of delivered notifications the
// command center turns into bounded detail previews.
type AgentsOverviewEventKind int

const (
	AgentsOverviewEventReasoningDelta AgentsOverviewEventKind = iota
	AgentsOverviewEventAgentMessage
	AgentsOverviewEventTurnStarted
	AgentsOverviewEventTurnCompleted
	AgentsOverviewEventReasoningPartAdded
	AgentsOverviewEventItemStarted
	AgentsOverviewEventStatusChanged
)

// AgentsOverviewEvent carries the fields TrackAgentsOverviewActivity needs from
// one delivered notification.
type AgentsOverviewEvent struct {
	Kind AgentsOverviewEventKind
	// TurnID/ItemID/SummaryIndex identify the reasoning summary being streamed
	// so deltas from a new item never extend the previous heading.
	TurnID       string
	ItemID       string
	SummaryIndex int
	// Delta is the streamed reasoning text.
	Delta string
	// Text is a completed agent message.
	Text string
	// StatusActive reports whether a thread status change keeps the task active;
	// a task that stopped being active loses its stale reasoning heading.
	StatusActive bool
}

// AgentsOverviewActivity holds the live previews rendered for one task.
type AgentsOverviewActivity struct {
	// ReasoningHeading is the first bold summary line of the running turn.
	ReasoningHeading string
	// LastMessage is the latest delivered agent message.
	LastMessage string
	// RequestPreview is the pending approval or user-input request text.
	RequestPreview string

	reasoningTurnID       string
	reasoningItemID       string
	reasoningSummaryIndex int
	reasoningBuffer       string
}

// AgentsOverviewActivityFor returns a copy of the tracked activity for a task,
// or the zero value when nothing was observed.
func (v *View) AgentsOverviewActivityFor(threadID string) AgentsOverviewActivity {
	if v == nil || v.activity == nil {
		return AgentsOverviewActivity{}
	}
	activity := v.activity[strings.TrimSpace(threadID)]
	if activity == nil {
		return AgentsOverviewActivity{}
	}
	return *activity
}

// HasAgentsOverviewActivity reports whether the task has any tracked detail.
func (v *View) HasAgentsOverviewActivity(threadID string) bool {
	if v == nil || v.activity == nil {
		return false
	}
	return v.activity[strings.TrimSpace(threadID)] != nil
}

// AgentsOverviewActivityThreads lists the tasks with tracked activity.
func (v *View) AgentsOverviewActivityThreads() []string {
	if v == nil || len(v.activity) == 0 {
		return nil
	}
	threads := make([]string, 0, len(v.activity))
	for threadID := range v.activity {
		threads = append(threads, threadID)
	}
	return threads
}

// TrackAgentsOverviewActivity folds one delivered notification into the task's
// previews and reports whether the dashboard must repaint (Rust
// track_agents_overview_activity). Notifications for tasks the dashboard does
// not track are ignored.
func (v *View) TrackAgentsOverviewActivity(threadID string, event AgentsOverviewEvent) bool {
	if v == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || !containsThreadID(v.Rows, threadID) {
		return false
	}
	if v.activity == nil {
		v.activity = map[string]*AgentsOverviewActivity{}
	}
	activity := v.activity[threadID]
	if activity == nil {
		activity = &AgentsOverviewActivity{}
		v.activity[threadID] = activity
	}
	changed := false
	switch event.Kind {
	case AgentsOverviewEventReasoningDelta:
		hadHeader := activity.ReasoningHeading != ""
		if activity.reasoningTurnID != event.TurnID ||
			activity.reasoningItemID != event.ItemID ||
			activity.reasoningSummaryIndex != event.SummaryIndex {
			activity.reasoningTurnID = event.TurnID
			activity.reasoningItemID = event.ItemID
			activity.reasoningSummaryIndex = event.SummaryIndex
			activity.reasoningBuffer = ""
		}
		if activity.ReasoningHeading != "" {
			// Rust stops buffering once the heading is known.
			return false
		}
		remaining := AgentsOverviewActivityPreviewChars - len([]rune(activity.reasoningBuffer))
		if remaining > 0 {
			runes := []rune(event.Delta)
			if len(runes) > remaining {
				runes = runes[:remaining]
			}
			activity.reasoningBuffer += string(runes)
		}
		activity.ReasoningHeading = firstBoldHeading(activity.reasoningBuffer)
		changed = hadHeader || activity.ReasoningHeading != ""
	case AgentsOverviewEventAgentMessage:
		activity.LastMessage = PreviewActivityText(event.Text)
		activity.ReasoningHeading = ""
		activity.reasoningBuffer = ""
		changed = true
	case AgentsOverviewEventTurnStarted,
		AgentsOverviewEventTurnCompleted,
		AgentsOverviewEventReasoningPartAdded,
		AgentsOverviewEventItemStarted:
		changed = activity.ReasoningHeading != "" || activity.reasoningBuffer != ""
		activity.ReasoningHeading = ""
		activity.reasoningBuffer = ""
	case AgentsOverviewEventStatusChanged:
		if !event.StatusActive {
			changed = activity.ReasoningHeading != "" || activity.reasoningBuffer != ""
			activity.ReasoningHeading = ""
			activity.reasoningBuffer = ""
		}
	}
	return changed
}

// SetAgentsOverviewRequestPreview records the pending approval or user-input
// request shown for a needs-attention task (Rust agents_overview_request_preview).
func (v *View) SetAgentsOverviewRequestPreview(threadID, preview string) bool {
	if v == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	preview = PreviewActivityText(preview)
	if preview == "" {
		return v.ClearAgentsOverviewRequestPreview(threadID)
	}
	if v.activity == nil {
		v.activity = map[string]*AgentsOverviewActivity{}
	}
	activity := v.activity[threadID]
	if activity == nil {
		activity = &AgentsOverviewActivity{}
		v.activity[threadID] = activity
	}
	if activity.RequestPreview == preview {
		return false
	}
	activity.RequestPreview = preview
	return true
}

// ClearAgentsOverviewRequestPreview drops a resolved request preview.
func (v *View) ClearAgentsOverviewRequestPreview(threadID string) bool {
	if v == nil || v.activity == nil {
		return false
	}
	activity := v.activity[strings.TrimSpace(threadID)]
	if activity == nil || activity.RequestPreview == "" {
		return false
	}
	activity.RequestPreview = ""
	v.pruneActivity()
	return true
}

// ClearAgentsOverviewActivity forgets every preview for a task, as Rust does
// when a task is archived, deleted or reconnected.
func (v *View) ClearAgentsOverviewActivity(threadID string) {
	if v == nil || v.activity == nil {
		return
	}
	delete(v.activity, strings.TrimSpace(threadID))
}

// pruneActivity drops entries with no remaining preview.
func (v *View) pruneActivity() {
	if v == nil || len(v.activity) == 0 {
		return
	}
	for threadID, activity := range v.activity {
		if activity == nil || (activity.ReasoningHeading == "" && activity.LastMessage == "" && activity.RequestPreview == "") {
			delete(v.activity, threadID)
		}
	}
}

// pruneActivityRows drops previews for tasks the dashboard no longer lists.
func (v *View) pruneActivityRows() {
	if v == nil || len(v.activity) == 0 {
		return
	}
	for threadID := range v.activity {
		if !containsThreadID(v.Rows, threadID) {
			delete(v.activity, threadID)
		}
	}
}

// firstBoldHeading returns the text of the first markdown bold span, mirroring
// Rust's extract_first_bold preview for a streamed reasoning summary.
func firstBoldHeading(text string) string {
	for offset := 0; offset < len(text); offset++ {
		index := strings.Index(text[offset:], "**")
		if index < 0 {
			return ""
		}
		start := offset + index + 2
		end := strings.Index(text[start:], "**")
		if end < 0 {
			return ""
		}
		header := strings.TrimSpace(text[start : start+end])
		if header != "" {
			return PreviewActivityText(header)
		}
		offset = start + end
	}
	return ""
}
