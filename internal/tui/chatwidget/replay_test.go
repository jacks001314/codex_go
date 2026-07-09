package chatwidget

import "testing"

func TestReplayThreadItemRoutesMatchRustCore(t *testing.T) {
	cases := []struct {
		name   string
		kind   ReplayThreadItemKind
		status string
		replay ReplayKind
		want   ReplayItemRoute
	}{
		{"user", ReplayThreadItemUserMessage, "", ReplayResumeInitialMessages, ReplayItemRouteCommittedUserMessage},
		{"agent", ReplayThreadItemAgentMessage, "", ReplayResumeInitialMessages, ReplayItemRouteAgentMessageCompleted},
		{"plan", ReplayThreadItemPlan, "", ReplayResumeInitialMessages, ReplayItemRoutePlanCompleted},
		{"command running", ReplayThreadItemCommandExecution, "in_progress", ReplayThreadSnapshot, ReplayItemRouteCommandStarted},
		{"command done", ReplayThreadItemCommandExecution, "completed", ReplayThreadSnapshot, ReplayItemRouteCommandCompleted},
		{"patch running ignored", ReplayThreadItemFileChange, "in_progress", ReplayThreadSnapshot, ReplayItemRouteIgnoreInProgressPatch},
		{"patch done", ReplayThreadItemFileChange, "completed", ReplayThreadSnapshot, ReplayItemRouteFileChangeCompleted},
		{"mcp running", ReplayThreadItemMcpToolCall, "in_progress", ReplayThreadSnapshot, ReplayItemRouteMcpToolStarted},
		{"mcp done", ReplayThreadItemMcpToolCall, "failed", ReplayThreadSnapshot, ReplayItemRouteMcpToolCompleted},
		{"web search", ReplayThreadItemWebSearch, "", ReplayThreadSnapshot, ReplayItemRouteWebSearchBeginEnd},
		{"image view", ReplayThreadItemImageView, "", ReplayThreadSnapshot, ReplayItemRouteViewImage},
		{"image generation", ReplayThreadItemImageGeneration, "", ReplayThreadSnapshot, ReplayItemRouteImageGenerationEnd},
		{"entered review replay", ReplayThreadItemEnteredReviewMode, "", ReplayThreadSnapshot, ReplayItemRouteEnterReviewMode},
		{"entered review live ignored", ReplayThreadItemEnteredReviewMode, "", ReplayNone, ReplayItemRouteUnknown},
		{"exited review", ReplayThreadItemExitedReviewMode, "", ReplayThreadSnapshot, ReplayItemRouteExitReviewMode},
		{"context compacted", ReplayThreadItemContextCompaction, "", ReplayThreadSnapshot, ReplayItemRouteContextCompactedInfo},
		{"hook prompt", ReplayThreadItemHookPrompt, "", ReplayThreadSnapshot, ReplayItemRouteIgnoreHookPrompt},
		{"collab", ReplayThreadItemCollabAgent, "", ReplayThreadSnapshot, ReplayItemRouteCollabAgentToolCall},
		{"subagent", ReplayThreadItemSubAgentActivity, "", ReplayThreadSnapshot, ReplayItemRouteSubAgentActivity},
		{"dynamic", ReplayThreadItemDynamicToolCall, "", ReplayThreadSnapshot, ReplayItemRouteIgnoreDynamicToolCall},
		{"sleep", ReplayThreadItemSleep, "", ReplayThreadSnapshot, ReplayItemRouteIgnoreSleep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyReplayThreadItem(tc.kind, tc.status, tc.replay, "turn-1", false)
			if got.Route != tc.want {
				t.Fatalf("route = %q, want %q", got.Route, tc.want)
			}
		})
	}
}

func TestReplayReasoningAndThreadSnapshotRedrawMatchRust(t *testing.T) {
	summary := ClassifyReplayThreadItem(ReplayThreadItemReasoning, "", ReplayThreadSnapshot, "", false)
	if summary.Route != ReplayItemRouteReasoningReplaySummary || !summary.ReplayReasoningDelta || !summary.RequestRedraw {
		t.Fatalf("summary reasoning = %#v", summary)
	}
	raw := ClassifyReplayThreadItem(ReplayThreadItemReasoning, "", ReplayThreadSnapshot, "turn-1", true)
	if raw.Route != ReplayItemRouteReasoningReplayRaw || !raw.ReplayReasoningDelta || raw.RequestRedraw {
		t.Fatalf("raw reasoning = %#v", raw)
	}
	live := ClassifyReplayThreadItem(ReplayThreadItemReasoning, "", ReplayNone, "", true)
	if live.Route != ReplayItemRouteReasoningFinalizeOnly || live.ReplayReasoningDelta || live.FromReplay {
		t.Fatalf("live reasoning = %#v", live)
	}
}

func TestReplaySuppressionKindsMatchRust(t *testing.T) {
	if !ShouldSuppressDuringReplay(ReplayResumeInitialMessages, NotificationTurnStarted) ||
		!ShouldSuppressDuringReplay(ReplayResumeInitialMessages, NotificationError) {
		t.Fatalf("resume initial replay should suppress turn started and error")
	}
	if ShouldSuppressDuringReplay(ReplayThreadSnapshot, NotificationTurnStarted) ||
		ShouldSuppressDuringReplay(ReplayResumeInitialMessages, NotificationTurnCompleted) {
		t.Fatalf("unexpected replay suppression")
	}
}
