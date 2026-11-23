package compact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func explicitRetention(retain bool) map[string]any {
	return map[string]any{"harness_metadata": map[string]any{"retain_across_windows": retain}}
}

func retainedHistoryIDs(items []Item) string {
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	return strings.Join(ids, ",")
}

// Rust #61344 `compact_retained_tool_outputs::is_retained_tool_output`: the
// explicit policy overrides the legacy client-authored marker.
func TestIsRetainedToolOutputHonorsExplicitPolicyLikeRust(t *testing.T) {
	cases := []struct {
		name string
		item Item
		want bool
	}{
		{
			name: "explicit always without legacy marker",
			item: Item{Type: "function_call_output", Name: "shell", Text: "keep", Data: explicitRetention(true)},
			want: true,
		},
		{
			name: "explicit default overrides legacy marker",
			item: Item{Type: "function_call_output", Name: "shell", Text: "keep", Data: map[string]any{"harness_metadata": map[string]any{
				"client_authored": true, "retain_across_windows": false,
			}}},
			want: false,
		},
		{
			name: "legacy marker still interprets older rollouts",
			item: Item{Type: "function_call_output", Name: "shell", Text: "keep", Data: map[string]any{"harness_metadata": map[string]any{"client_authored": true}}},
			want: true,
		},
		{
			name: "explicit always still needs no call id",
			item: Item{Type: "function_call_output", CallID: "call-1", Name: "shell", Text: "keep", Data: explicitRetention(true)},
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsRetainedToolOutput(testCase.item); got != testCase.want {
				t.Fatalf("IsRetainedToolOutput() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// Rust #61344 keeps an explicitly retained tool exchange together and in
// history order.
func TestRemoteRetentionKeepsExplicitToolExchangeTogetherLikeRust(t *testing.T) {
	call := Item{ID: "call", Type: "function_call", CallID: "release-check", Name: "check_release", Text: "{}", Data: explicitRetention(true)}
	output := Item{ID: "output", Type: "function_call_output", CallID: "release-check", Name: "check_release", Text: "The release candidate passed.", Data: explicitRetention(true)}
	assistant := Item{ID: "assistant", Type: "assistant_message", Role: "assistant", Text: "done"}

	retained := retainedMessagesForRemoteCompaction([]Item{call, output, assistant}, RemoteRetainedMessageTokenBudget, retentionPolicy{})
	if got, want := retainedHistoryIDs(retained), "call,output"; got != want {
		t.Fatalf("retained ids = %s, want %s: %#v", got, want, retained)
	}
}

// Rust #62677 keeps an explicitly pinned exchange outside the carryover budget,
// so it survives whole even when the soft budget cannot fit it, while the
// remaining budget still retains ordinary user history.
func TestRemoteRetentionKeepsOversizedExplicitExchangeLikeRust(t *testing.T) {
	older := Item{ID: "older", Type: "message", Role: "user", Kind: "user_message", Text: "older instruction"}
	call := Item{ID: "call", Type: "function_call", CallID: "release-check", Name: "check_release", Text: strings.Repeat("x", 4_000), Data: explicitRetention(true)}
	output := Item{ID: "output", Type: "function_call_output", CallID: "release-check", Name: "check_release", Text: strings.Repeat("y", 4_000), Data: explicitRetention(true)}
	budget := EstimateItemTokens(&call) + EstimateItemTokens(&output) - 1

	retained := retainedMessagesForRemoteCompaction([]Item{older, call, output}, budget, retentionPolicy{})
	if got, want := retainedHistoryIDs(retained), "older,call,output"; got != want {
		t.Fatalf("retained ids = %s, want %s: %#v", got, want, retained)
	}
	if !strings.HasPrefix(retained[1].Text, "xxxxxxxx") || !strings.HasPrefix(retained[2].Text, "yyyyyyyy") {
		t.Fatalf("pinned exchange lost its payload: %#v", retained)
	}
}

// An explicit retention decision beats the structured agent-message size cap.
func TestRemoteRetentionKeepsExplicitAgentMessageWholeLikeRust(t *testing.T) {
	mail := structuredAgentMessageItem(t, "mail", "Message Type: MESSAGE\nTask name: /root\nSender: /root/worker\nPayload:\n", strings.Repeat("z", 80_000))
	mail.Data = explicitRetention(true)
	if EstimateItemTokens(&mail) <= MaxRetainedAgentMessageTokens {
		t.Fatal("fixture must exceed the ordinary agent-message cap")
	}
	retained := retainedMessagesForRemoteCompaction([]Item{mail}, RemoteRetainedMessageTokenBudget, retentionPolicy{})
	if got := retainedHistoryIDs(retained); got != "mail" {
		t.Fatalf("retained ids = %s, want the explicitly retained agent message", got)
	}
}

// Rust keeps the envelope metadata when a retained message is truncated to the
// remaining budget, so the item stays retained on the next context window.
func TestRemoteRetentionTruncatedMessageKeepsPolicyLikeRust(t *testing.T) {
	message := Item{ID: "retained", Type: "message", Role: "developer", Text: strings.Repeat("a", 40_000), Data: explicitRetention(true)}
	retained := retainedMessagesForRemoteCompaction([]Item{message}, 100, retentionPolicy{})
	if len(retained) != 1 {
		t.Fatalf("retained = %#v, want one truncated message", retained)
	}
	if retained[0].Text == message.Text {
		t.Fatalf("retained message was not truncated: %#v", retained[0])
	}
	if !IsAlwaysRetained(retained[0]) {
		t.Fatalf("truncated message lost its retention policy: %#v", retained[0].Data)
	}
}

// Rust #61344 folds an image-resize notice into the item it describes and
// charges or drops them together.
func TestRemoteRetentionKeepsAttachedImageResizeNoticeLikeRust(t *testing.T) {
	output := Item{ID: "output", Type: "function_call_output", Name: "screenshot", Text: "captured", Data: explicitRetention(true)}
	notice := Item{
		ID:   "notice",
		Type: "message",
		Role: "developer",
		Text: "<image_resize_notice>\nImage 1 of 1 in the preceding tool output was resized from 10x10 to 5x5 pixels.\n</image_resize_notice>",
		Content: []ContentPart{{
			Type: "input_text",
			Text: "<image_resize_notice>\nImage 1 of 1 in the preceding tool output was resized from 10x10 to 5x5 pixels.\n</image_resize_notice>",
		}},
	}
	retained := retainedMessagesForRemoteCompaction([]Item{output, notice}, RemoteRetainedMessageTokenBudget, retentionPolicy{})
	if got, want := retainedHistoryIDs(retained), "output,notice"; got != want {
		t.Fatalf("retained ids = %s, want %s: %#v", got, want, retained)
	}
}

// A client that sends the same text with its own policy keeps it as a separate
// item instead of having it folded into its neighbour.
func TestRemoteRetentionDoesNotAttachClientMarkedNoticeLikeRust(t *testing.T) {
	ordinary := Item{ID: "ordinary", Type: "message", Role: "developer", Text: "ordinary guidance"}
	notice := Item{
		ID:   "notice",
		Type: "message",
		Role: "developer",
		Text: "<image_resize_notice>\nclient-marked\n</image_resize_notice>",
		Content: []ContentPart{{
			Type: "input_text",
			Text: "<image_resize_notice>\nclient-marked\n</image_resize_notice>",
		}},
		Data: explicitRetention(true),
	}
	retained := retainedMessagesForRemoteCompaction([]Item{ordinary, notice}, RemoteRetainedMessageTokenBudget, retentionPolicy{})
	if got := retainedHistoryIDs(retained); got != "notice" {
		t.Fatalf("retained ids = %s, want only the explicitly retained notice", got)
	}
}

// Local compaction keeps explicitly retained non-message items in history
// order and does not duplicate a user message that both budgets select.
func TestLocalCompactionKeepsExplicitRetentionLikeRust(t *testing.T) {
	history := []Item{
		{ID: "old-user", Type: "message", Role: "user", Kind: "user_message", Text: "start"},
		{ID: "retained-dev", Type: "message", Role: "developer", Text: "keep this instruction", Data: explicitRetention(true)},
		{ID: "always-user", Type: "message", Role: "user", Kind: "user_message", Text: "remember this", Data: explicitRetention(true)},
	}
	result, err := CompactLocally(&Request{
		ThreadID: "thread-local-retention",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History:  history,
	}, 1000, nil, false)
	if err != nil {
		t.Fatalf("CompactLocally() error = %v", err)
	}
	if got, want := retainedHistoryIDs(result.NewHistory), "old-user,retained-dev,always-user,compaction-summary"; got != want {
		t.Fatalf("history ids = %s, want %s: %#v", got, want, result.NewHistory)
	}
}

// Rust #61344: a pending agent message that is also explicitly retained is
// delivered once, at its pending position.
func TestLocalCompactionDeduplicatesRetainedPendingMailLikeRust(t *testing.T) {
	pending := pendingAgentMailItem(t, "pending-mail", 3, "worker result")
	pending.Data = explicitRetention(true)
	result, err := CompactLocally(&Request{
		ThreadID: "thread-pending-retained",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History: []Item{
			{ID: "old-user", Type: "message", Role: "user", Kind: "user_message", Text: "start", HistorySequence: 1, SampledThroughSequence: 2},
			pending,
			{ID: "later-user", Type: "message", Role: "user", Kind: "user_message", Text: "continue", HistorySequence: 4},
		},
	}, 1000, nil, false)
	if err != nil {
		t.Fatalf("CompactLocally() error = %v", err)
	}
	if got, want := pendingMailHistoryIDs(result.NewHistory), "old-user,compaction-summary,pending-mail,later-user"; got != want {
		t.Fatalf("history ids = %s, want %s: %#v", got, want, result.NewHistory)
	}
}

// A retained client item forms the same context-window rewrite boundary as a
// user or developer message.
func TestTrimFunctionCallHistoryStopsAtExplicitRetentionLikeRust(t *testing.T) {
	history := []Item{
		{ID: "old-output", Type: "function_call_output", CallID: "call-old", Name: "shell", Text: strings.Repeat("o", 40_000)},
		{ID: "retained", Type: "function_call_output", Name: "shell", Text: "keep me", Data: explicitRetention(true)},
		{ID: "new-output", Type: "function_call_output", CallID: "call-new", Name: "shell", Text: strings.Repeat("n", 40_000)},
	}
	trimmed, rewritten := TrimFunctionCallHistoryToFitContextWindow(history, 100, "", false)
	if rewritten != 1 {
		t.Fatalf("rewritten = %d, want the newest output only", rewritten)
	}
	if trimmed[2].Text != ContextWindowTruncatedOutputMessage {
		t.Fatalf("newest output = %#v, want the truncation placeholder", trimmed[2])
	}
	if trimmed[1].Text != "keep me" || trimmed[0].Text != history[0].Text {
		t.Fatalf("history crossed the retention boundary: %#v", trimmed)
	}
}

// Rust #61344 groups an attached image-resize notice with its tool output: the
// notice is not a rewrite boundary, and rewriting the output drops it.
func TestTrimFunctionCallHistoryFoldsAttachedResizeNoticeLikeRust(t *testing.T) {
	output := Item{ID: "output", Type: "function_call_output", CallID: "call-1", Name: "screenshot", Text: strings.Repeat("o", 4_000)}
	noticeText := "<image_resize_notice>\nImage 1 of 1 in the preceding tool output was resized.\n</image_resize_notice>"
	notice := Item{ID: "notice", Type: "message", Role: "developer", Text: noticeText,
		Content: []ContentPart{{Type: "input_text", Text: noticeText}}}
	trimmed, rewritten := TrimFunctionCallHistoryToFitContextWindow([]Item{output, notice}, 100, "", false)
	if rewritten != 1 {
		t.Fatalf("rewritten = %d, want 1", rewritten)
	}
	if len(trimmed) != 1 || trimmed[0].Text != ContextWindowTruncatedOutputMessage {
		t.Fatalf("trimmed = %#v, want only the truncated output", trimmed)
	}
}

// An explicit default opts a client-authored output out of the feature-level
// retention that would otherwise keep it.
func TestRemoteRetentionExplicitDefaultOverridesFeatureLikeRust(t *testing.T) {
	output := Item{
		ID: "output", Type: "function_call_output", Name: "shell", Text: "legacy instruction",
		Data: map[string]any{"harness_metadata": map[string]any{
			"client_authored": true, "retain_across_windows": false,
		}},
	}
	retained := retainedMessagesForRemoteCompaction([]Item{output}, RemoteRetainedMessageTokenBudget, retentionPolicy{clientToolOutputs: true})
	if len(retained) != 0 {
		t.Fatalf("retained = %#v, want the explicit default to opt out", retained)
	}
}

// Rust #61344's compaction scenario keeps an explicitly retained tool exchange
// across repeated remote compaction and a persisted-history round trip.
func TestRemoteRetentionSurvivesRepeatedCompactionAndRestartLikeRust(t *testing.T) {
	call := Item{ID: "call", Type: "function_call", CallID: "release-check", Name: "check_release", Text: "{}", Data: explicitRetention(true)}
	output := Item{ID: "output", Type: "function_call_output", CallID: "release-check", Name: "check_release", Text: "The release candidate passed.", Data: explicitRetention(true)}
	history := []Item{
		{ID: "user", Type: "message", Role: "user", Kind: "user_message", Text: "start the release"},
		call,
		output,
		{ID: "assistant", Type: "assistant_message", Role: "assistant", Text: "done"},
	}
	runner := remoteRunnerFunc(func(context.Context, *Request) (*Result, error) {
		return &Result{Status: StatusCompleted, Summary: "release context", Source: SourceRemote}, nil
	})
	compactOnce := func(items []Item) []Item {
		t.Helper()
		result, err := CompactRemotely(context.Background(), &Request{
			ThreadID: "thread-retained-restart",
			Trigger:  TriggerAuto,
			Reason:   ReasonTokenLimit,
			Phase:    PhaseMidTurn,
			History:  items,
		}, &RemoteOptions{Runner: runner})
		if err != nil {
			t.Fatalf("CompactRemotely() error = %v", err)
		}
		return result.NewHistory
	}

	first := compactOnce(history)
	if got, want := retainedHistoryIDs(first), "user,call,output,compaction-summary"; got != want {
		t.Fatalf("first compaction ids = %s, want %s: %#v", got, want, first)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	var restarted []Item
	if err := json.Unmarshal(encoded, &restarted); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	second := compactOnce(restarted)
	if got, want := retainedHistoryIDs(second), "user,call,output,compaction-summary"; got != want {
		t.Fatalf("second compaction ids = %s, want %s: %#v", got, want, second)
	}
}

// retentionScopes builds harness metadata for the #62654 policies. A nil value
// omits that scope, which is how `always` and `default` are persisted.
func retentionScopes(retainAcrossWindows any, retainInChildren any) map[string]any {
	metadata := map[string]any{}
	if retainAcrossWindows != nil {
		metadata[RetainAcrossWindowsMetadataKey] = retainAcrossWindows
	}
	if retainInChildren != nil {
		metadata[RetainInChildrenMetadataKey] = retainInChildren
	}
	return map[string]any{"harness_metadata": metadata}
}

// Rust #62654 `ResponseItemEnvelope::child_retention`: an explicit
// retain_in_children wins, and an older rollout that only pinned the item across
// windows inherits it too, so `always` keeps meaning both scopes.
func TestChildRetentionResolvesBothScopesLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		data       map[string]any
		want       bool
		wantStated bool
	}{
		{name: "no metadata", data: nil, want: false, wantStated: false},
		{name: "default", data: retentionScopes(false, nil), want: false, wantStated: false},
		{name: "legacy always", data: retentionScopes(true, nil), want: true, wantStated: true},
		{name: "alwaysInThread", data: retentionScopes(true, false), want: false, wantStated: true},
		{name: "alwaysInChildren", data: retentionScopes(false, true), want: true, wantStated: true},
		{name: "explicit child exclusion alone", data: retentionScopes(nil, false), want: false, wantStated: true},
		{name: "explicit child inclusion alone", data: retentionScopes(nil, true), want: true, wantStated: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			item := Item{Type: "message", Role: "developer", Text: "policy", Data: testCase.data}
			inherit, stated := ChildRetention(item)
			if inherit != testCase.want || stated != testCase.wantStated {
				t.Fatalf("ChildRetention() = %v, %v; want %v, %v", inherit, stated, testCase.want, testCase.wantStated)
			}
			if got := IsRetainedInChildren(item); got != (testCase.want && testCase.wantStated) {
				t.Fatalf("IsRetainedInChildren() = %v", got)
			}
			if got := IsExcludedFromChildren(item); got != (testCase.wantStated && !testCase.want) {
				t.Fatalf("IsExcludedFromChildren() = %v", got)
			}
		})
	}
}

// Rust #62654 keeps the two scopes independent: `alwaysInThread` stays in this
// thread's later windows but never reaches a spawned agent, while
// `alwaysInChildren` reaches a spawned agent and leaves window retention to the
// ordinary rules.
func TestRetentionScopesSelectIndependentlyLikeRust(t *testing.T) {
	items := []Item{
		{ID: "thread-only", Type: "message", Role: "developer", Text: "thread only", Data: retentionScopes(true, false)},
		{ID: "children-only", Type: "message", Role: "developer", Text: "children only", Data: retentionScopes(false, true)},
		{ID: "shared", Type: "message", Role: "developer", Text: "shared", Data: retentionScopes(true, nil)},
		{ID: "ordinary", Type: "message", Role: "assistant", Text: "ordinary"},
	}
	positions := func(selected map[int]Item) string {
		ids := make([]string, 0, len(selected))
		for position := 0; position < len(items); position++ {
			item, ok := selected[position]
			if !ok {
				continue
			}
			ids = append(ids, item.ID)
		}
		return strings.Join(ids, ",")
	}

	if got := positions(SelectAlwaysRetainedPositions(items, RemoteRetainedMessageTokenBudget, nil)); got != "thread-only,shared" {
		t.Fatalf("window-retained items = %q, want thread-only,shared", got)
	}
	if got := positions(SelectChildRetainedPositions(items, RemoteRetainedMessageTokenBudget, nil)); got != "children-only,shared" {
		t.Fatalf("child-retained items = %q, want children-only,shared", got)
	}
}

// A child-retained tool exchange is admitted whole even though the parent keeps
// it out of later context windows: pairing follows the same scope as selection.
func TestChildRetentionPairsToolExchangeLikeRust(t *testing.T) {
	data := retentionScopes(false, true)
	items := []Item{
		{ID: "call", Type: "function_call", Name: "shell", CallID: "call-1", Text: "{}", Data: data},
		{ID: "output", Type: "function_call_output", Name: "shell", CallID: "call-1", Text: "result", Data: data},
	}
	if got := SelectAlwaysRetainedPositions(items, RemoteRetainedMessageTokenBudget, nil); len(got) != 0 {
		t.Fatalf("window-retained exchange = %#v, want none", got)
	}
	selected := SelectChildRetainedPositions(items, RemoteRetainedMessageTokenBudget, nil)
	if len(selected) != 2 {
		t.Fatalf("child-retained exchange = %#v, want the call and its output", selected)
	}
	if selected[0].ID != "call" || selected[1].ID != "output" {
		t.Fatalf("child-retained exchange = %#v, want call first", selected)
	}
}
