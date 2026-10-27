package appserver

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

func guardianHandoffUser(t *testing.T, id string, order uint64, text string) session.Item {
	t.Helper()
	return inheritedMetadataItem(t, sessionItemMessage(id, "user", text), fmt.Sprintf(`{"user_input_order":%d}`, order))
}

func guardianHandoffCall(t *testing.T, name string, target string) session.Item {
	t.Helper()
	return session.Item{
		ID:   "call-" + target,
		Type: "function_call",
		Name: name,
		Data: map[string]any{"arguments": fmt.Sprintf(`{"task_name":%q}`, target)},
	}
}

func TestGuardianRootHandoffSelectionUsesWorkerWindowsLikeRust(t *testing.T) {
	items := []session.Item{
		guardianHandoffUser(t, "user-1", 1, "initial"),
		guardianHandoffCall(t, "collaboration.spawn_agent", "worker"),
		guardianHandoffUser(t, "user-2", 2, "other branch"),
		guardianHandoffCall(t, "collaboration.send_message", "other"),
	}
	candidates := []guardianRootCandidate{
		{message: guardianRootUserMessage("initial"), order: 1, hasOrder: true},
		{message: guardianRootAssistantMessage("first answer"), order: 1, hasOrder: true},
		{message: guardianRootUserMessage("other branch"), order: 2, hasOrder: true},
		{message: guardianRootAssistantMessage("second answer"), order: 2, hasOrder: true},
		{message: guardianRootAssistantMessage("third answer"), order: 2, hasOrder: true},
		{message: guardianRootAssistantMessage("latest answer"), order: 3, hasOrder: true},
		{message: guardianRootUserMessage("unordered legacy")},
	}
	selected := guardianRootHandoffSelection(items, candidates, "/root/worker/sub", "collaboration")
	var got []string
	for _, candidate := range selected {
		got = append(got, candidate.message.Text)
	}
	// Rust keeps every user restriction (including another branch) and places
	// unordered legacy evidence first, per #50026 and prepare_root_context.
	want := []string{"unordered legacy", "initial", "first answer", "other branch", "second answer", "third answer", "latest answer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected messages = %#v, want %#v", got, want)
	}
}

func TestGuardianRootHandoffSelectionAppliesDuringHeartbeatLikeRust(t *testing.T) {
	heartbeat := guardianHandoffUser(t, "heartbeat", 2, "<heartbeat>\n<instructions>run</instructions>\n</heartbeat>")
	items := []session.Item{
		guardianHandoffUser(t, "user-1", 1, "initial"),
		heartbeat,
		guardianHandoffCall(t, "collaboration.spawn_agent", "worker"),
	}
	selected := guardianRootHandoffSelection(items, []guardianRootCandidate{
		{message: guardianRootUserMessage("initial"), order: 1, hasOrder: true},
		{message: guardianRootUserMessage("<heartbeat>\n<instructions>run</instructions>\n</heartbeat>"), order: 2, hasOrder: true},
	}, "/root/worker", "collaboration")
	var got []string
	for _, candidate := range selected {
		got = append(got, candidate.message.Text)
	}
	if want := []string{"initial", "<heartbeat>\n<instructions>run</instructions>\n</heartbeat>"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("heartbeat selection = %#v, want %#v", got, want)
	}
}

func TestGuardianRootHandoffSelectionFallsBackWithoutUsableWindowLikeRust(t *testing.T) {
	items := []session.Item{guardianHandoffUser(t, "user-1", 1, "initial")}
	if selected := guardianRootHandoffSelection(items, []guardianRootCandidate{
		{message: guardianRootUserMessage("initial"), order: 1, hasOrder: true},
	}, "/root/worker", "collaboration"); selected != nil {
		t.Fatalf("context without a handoff call selected messages: %#v", selected)
	}
}

func TestGuardianRootHandoffSelectionReadsRecordedNestedCallsLikeRust(t *testing.T) {
	items := []session.Item{
		guardianHandoffUser(t, "user-1", 1, "initial"),
		{
			ID:   "code-mode",
			Type: "message",
			Role: "assistant",
			Data: map[string]any{"executed_tool_calls": []any{map[string]any{
				"name":      "followup_task",
				"arguments": `{"target":"worker"}`,
			}}},
		},
	}
	selected := guardianRootHandoffSelection(items, []guardianRootCandidate{
		{message: guardianRootUserMessage("initial"), order: 1, hasOrder: true},
	}, "/root/worker", "collaboration")
	if len(selected) != 1 || selected[0].message.Text != "initial" {
		t.Fatalf("nested handoff selection = %#v", selected)
	}
}

func guardianRootUserMessage(text string) state.RootMessage {
	return state.RootMessage{Kind: state.RootMessageUser, Text: text}
}

func guardianRootAssistantMessage(text string) state.RootMessage {
	return state.RootMessage{Kind: state.RootMessageAssistant, Text: text}
}

func sessionItemMessage(id string, role string, text string) session.Item {
	return session.Item{ID: id, Type: "message", Role: role, Text: text, CreatedAt: time.Unix(1, 0).UTC()}
}
