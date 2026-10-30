package guardiancontext

import "testing"

// Mirrors ConversationTranscriptEntryKind::role.
func TestConversationTranscriptEntryKindRoleLikeRust(t *testing.T) {
	cases := []struct {
		name string
		kind ConversationTranscriptEntryKind
		want string
	}{
		{"user", UserKind(), "user"},
		{"developer", DeveloperKind(), "developer"},
		{"assistant", AssistantKind(), "assistant"},
		{"protected assistant", ProtectedAssistantKind(), "assistant"},
		{"tool call", ToolCallKind("shell"), "shell"},
		{"tool output", ToolOutputKind("shell"), "shell"},
		{"node repl output", NodeReplToolOutputKind("js"), "js"},
		{"reasoning", ReasoningKind(), "reasoning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.kind.Role(); got != tc.want {
				t.Fatalf("Role() = %q, want %q", got, tc.want)
			}
		})
	}
}
