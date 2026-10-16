package compact

import (
	"context"
	"strings"
	"testing"
)

func retainedToolOutputItemIDs(items []Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func clientAuthoredToolOutput(id string, name string, text string) Item {
	return Item{
		ID:   id,
		Type: "function_call_output",
		Name: name,
		Text: text,
		Data: map[string]any{"harness_metadata": map[string]any{"client_authored": true}},
	}
}

// #52937 (core/src/compact_retained_tool_outputs.rs:7-20): the predicate is
// exactly `client_authored && FunctionCallOutput { call_id: None, name: Some(_) }`,
// so each of the three conjuncts has to reject on its own.
func TestIsRetainedToolOutputMatchesRust(t *testing.T) {
	authored := map[string]any{"harness_metadata": map[string]any{"client_authored": true}}
	cases := []struct {
		name string
		item Item
		want bool
	}{
		{
			name: "client-authored named output without a call",
			item: Item{Type: "function_call_output", Name: "shell", Text: "run the migration", Data: authored},
			want: true,
		},
		{
			name: "call id present",
			item: Item{Type: "function_call_output", CallID: "call-1", Name: "shell", Text: "done", Data: authored},
			want: false,
		},
		{
			name: "name missing",
			item: Item{Type: "function_call_output", Text: "done", Data: authored},
			want: false,
		},
		{
			name: "not client authored",
			item: Item{Type: "function_call_output", Name: "shell", Text: "done", Data: map[string]any{"harness_metadata": map[string]any{"client_authored": false}}},
			want: false,
		},
		{
			name: "harness metadata missing",
			item: Item{Type: "function_call_output", Name: "shell", Text: "done"},
			want: false,
		},
		{
			name: "call id only in the payload",
			item: Item{Type: "function_call_output", Name: "shell", Text: "done", Data: map[string]any{"harness_metadata": map[string]any{"client_authored": true}, "call_id": "call-2"}},
			want: false,
		},
		{
			name: "custom tool call output is a different variant",
			item: Item{Type: "custom_tool_call_output", Name: "shell", Text: "done", Data: authored},
			want: false,
		},
		{
			name: "ordinary tool output of this turn",
			item: Item{Type: "tool_output", Name: "shell", Text: "done", Data: authored},
			want: false,
		},
	}
	for _, tc := range cases {
		if got := IsRetainedToolOutput(tc.item); got != tc.want {
			t.Fatalf("%s: IsRetainedToolOutput() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// #52937 (core/src/compact_remote_v2.rs:529-578 and core/src/session/mod.rs:4666-4678):
// the flag comes from the turn's resolved feature settings, and a retained
// output joins the retained set ahead of the injected initial context.
func TestRetainedToolOutputsFollowTheRegisteredFeatureLikeRust(t *testing.T) {
	retained := clientAuthoredToolOutput("retained", "shell", "remember to run the migration")
	called := Item{ID: "called", Type: "function_call_output", CallID: "call-1", Name: "shell", Text: "tool result of this turn"}
	user := Item{ID: "user", Type: "message", Role: "user", Kind: "user_message", Text: "hello"}
	runner := remoteRunnerFunc(func(context.Context, *Request) (*Result, error) {
		return &Result{
			Status:  StatusCompleted,
			Summary: "remote summary",
			NewHistory: []Item{
				{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: SummaryPrefix + "\nremote summary"},
			},
		}, nil
	})

	for _, enabled := range []bool{false, true} {
		result, err := CompactRemotely(context.Background(), &Request{
			ThreadID: "thread-retained-tool-output",
			Trigger:  TriggerManual,
			Reason:   ReasonUserRequested,
			Phase:    PhaseStandaloneTurn,
			History:  []Item{retained, called, user},
		}, &RemoteOptions{
			Runner:               runner,
			InitialContext:       []Item{{ID: "ctx", Type: "message", Role: "developer", Text: "context"}},
			InjectBeforeLastUser: true,
			FeatureSettings:      map[string]bool{RetainClientToolOutputsKey: enabled},
		})
		if err != nil {
			t.Fatalf("CompactRemotely(retain_client_tool_outputs=%v) error = %v", enabled, err)
		}
		ids := strings.Join(retainedToolOutputItemIDs(result.NewHistory), ",")
		want := "ctx,user,summary"
		if enabled {
			want = "retained,ctx,user,summary"
		}
		if ids != want {
			t.Fatalf("retain_client_tool_outputs=%v: history ids = %s, want %s", enabled, ids, want)
		}
	}
}

// #52937 (core/src/compact_remote_v2.rs:641-649): a retained output that does not
// fit is dropped whole and stops the scan, instead of being partially truncated.
func TestRetainedToolOutputsAreKeptWholeOrDroppedLikeRust(t *testing.T) {
	newer := clientAuthoredToolOutput("newer", "shell", "recent instruction")
	oversized := clientAuthoredToolOutput("oversized", "shell", strings.Repeat("x", 4_000))
	older := Item{ID: "older", Type: "message", Role: "user", Kind: "user_message", Text: "older instruction"}
	budget := EstimateItemTokens(&newer) + EstimateItemTokens(&oversized) - 1

	retained := retainedMessagesForRemoteCompaction(
		[]Item{older, oversized, newer},
		budget,
		retentionPolicy{clientToolOutputs: true},
	)
	ids := strings.Join(retainedToolOutputItemIDs(retained), ",")
	if ids != "newer" {
		t.Fatalf("retained = %v, want only the newest output kept whole", ids)
	}
	for _, item := range retained {
		if item.ID == oversized.ID {
			t.Fatalf("oversized retained output must be dropped whole, got %+v", item)
		}
		if strings.Contains(item.Text, "xxxxxxxx") {
			t.Fatalf("retained output was partially truncated: %+v", item)
		}
	}
}

// #52937 (core/src/compact.rs:657-663): the initial context lands before the last
// real user or agent message, or a retained standalone output.
func TestInitialContextLandsBeforeRetainedToolOutputLikeRust(t *testing.T) {
	initialContext := []Item{{ID: "ctx", Type: "message", Role: "developer", Text: "context"}}
	summary := Item{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: "summary"}

	retained := clientAuthoredToolOutput("retained", "shell", "instruction")
	got := InsertInitialContextBeforeLastUserOrSummary([]Item{summary, retained}, initialContext)
	if ids := strings.Join(retainedToolOutputItemIDs(got), ","); ids != "summary,ctx,retained" {
		t.Fatalf("ids = %s, want the context before the retained output", ids)
	}

	// An output that keeps its call id is not a boundary, so without a real user
	// message the context falls back before the summary.
	called := Item{ID: "called", Type: "function_call_output", CallID: "call-1", Name: "shell", Text: "result", Data: map[string]any{"harness_metadata": map[string]any{"client_authored": true}}}
	got = InsertInitialContextBeforeLastUserOrSummary([]Item{summary, called}, initialContext)
	if ids := strings.Join(retainedToolOutputItemIDs(got), ","); ids != "ctx,summary,called" {
		t.Fatalf("ids = %s, want the fallback summary boundary", ids)
	}
}
