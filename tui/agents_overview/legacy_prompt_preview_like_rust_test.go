package agentsoverview

import (
	"strings"
	"testing"
)

// TestLegacyUserMessageBecomesPromptPreviewLikeRust covers Rust #61709: a
// legacy thread cannot cheaply re-read its saved prompt, so the completed
// user-message notification supplies the prompt preview and wins over the task
// list's preview.
func TestLegacyUserMessageBecomesPromptPreviewLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:    "t-legacy",
		CWD:         "/work/alpha",
		Preview:     "stale list preview",
		HistoryMode: "legacy",
		Group:       GroupWorking,
	}}, "", false)
	if !view.TrackAgentsOverviewActivity("t-legacy", AgentsOverviewEvent{
		Kind: AgentsOverviewEventUserMessage,
		Text: "compact the parser",
	}) {
		t.Fatal("a legacy user message did not repaint the prompt preview")
	}
	activity := view.AgentsOverviewActivityFor("t-legacy")
	if activity.LatestPrompt != "compact the parser" {
		t.Fatalf("latest prompt = %q", activity.LatestPrompt)
	}
	section := detailsSection(t, view.Render(140, 40), "Prompt")
	if len(section) == 0 || section[0] != "compact the parser" {
		t.Fatalf("prompt section = %#v, want the live prompt", section)
	}
	// Rust #61709: the notification-derived preview survives a refresh.
	view.ApplyRefresh([]Row{{
		ThreadID:    "t-legacy",
		CWD:         "/work/alpha",
		Preview:     "stale list preview",
		HistoryMode: "legacy",
		Group:       GroupWorking,
	}}, "t-legacy")
	if got := view.AgentsOverviewActivityFor("t-legacy").LatestPrompt; got != "compact the parser" {
		t.Fatalf("latest prompt after refresh = %q", got)
	}
}

// TestPaginatedUserMessageKeepsSavedPromptLikeRust covers the other half of
// Rust #61709: paginated threads read their prompt from turns/list, so live
// user-message notifications do not replace it.
func TestPaginatedUserMessageKeepsSavedPromptLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:      "t-paginated",
		CWD:           "/work/alpha",
		Preview:       "list preview",
		PromptPreview: "saved prompt",
		HistoryMode:   "paginated",
		Group:         GroupWorking,
	}}, "", false)
	if view.TrackAgentsOverviewActivity("t-paginated", AgentsOverviewEvent{
		Kind: AgentsOverviewEventUserMessage,
		Text: "live prompt",
	}) {
		t.Fatal("a paginated user message repainted the prompt preview")
	}
	section := detailsSection(t, view.Render(140, 40), "Prompt")
	if len(section) == 0 || section[0] != "saved prompt" {
		t.Fatalf("prompt section = %#v, want the saved prompt", section)
	}
}

// TestAgentMessagePreviewSurvivesThreadCloseLikeRust covers Rust #61709: the
// latest observed reply preview is retained after the thread closes instead of
// disappearing with the live status.
func TestAgentMessagePreviewSurvivesThreadCloseLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:     "t-live",
		CWD:          "/work/alpha",
		HistoryMode:  "paginated",
		Group:        GroupWorking,
		StatusType:   "active",
		StatusActive: true,
	}}, "", false)
	view.TrackAgentsOverviewActivity("t-live", AgentsOverviewEvent{
		Kind: AgentsOverviewEventAgentMessage,
		Text: "final reply",
	})
	// The thread closes: the dashboard now reports it as finished and idle.
	view.ApplyRefresh([]Row{{
		ThreadID:    "t-live",
		CWD:         "/work/alpha",
		HistoryMode: "paginated",
		Group:       GroupFinished,
		StatusType:  "idle",
	}}, "t-live")
	if got := view.AgentsOverviewActivityFor("t-live").LastMessage; got != "final reply" {
		t.Fatalf("last message after close = %q", got)
	}
	section := detailsSection(t, view.Render(140, 40), "Last message")
	if len(section) == 0 || !strings.Contains(section[0], "final reply") {
		t.Fatalf("last message section after close = %#v", section)
	}
}

// TestPromptPreviewPrecedenceKeepsListFallback covers the empty cases: a task
// with neither a saved nor a live prompt still shows the list preview.
func TestPromptPreviewPrecedenceKeepsListFallback(t *testing.T) {
	view := New([]Row{{
		ThreadID: "t-legacy",
		CWD:      "/work/alpha",
		Preview:  "list preview",
		Group:    GroupWorking,
	}}, "", false)
	section := detailsSection(t, view.Render(140, 40), "Prompt")
	if len(section) == 0 || !strings.Contains(section[0], "list preview") {
		t.Fatalf("prompt section = %#v, want the list preview", section)
	}
}
