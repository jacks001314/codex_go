package compact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstallCompactedHistoryPrefixPreservesLaterItemsLikeRust(t *testing.T) {
	history := []Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "first request"},
		{ID: "agent-1", Type: "agent_message", Text: "working"},
		{ID: "user-2", Type: "message", Role: "user", Text: "later request"},
		{ID: "agent-2", Type: "agent_message", Text: "later answer"},
	}
	replacement := []Item{
		{ID: "summary", Type: "message", Role: "user", Kind: "compaction_summary", Text: SummaryPrefix + "\nsummary"},
	}
	initialContext := []Item{{ID: "world-state", Type: "message", Role: "developer", Text: "current state"}}
	installed := InstallCompactedHistoryPrefix(history, replacement, CompactionPrefix{Len: 2}, nil, initialContext)
	if len(installed) != 4 {
		t.Fatalf("installed history = %#v", installed)
	}
	if installed[0].ID != "summary" || installed[1].ID != "world-state" ||
		installed[2].ID != "user-2" || installed[3].ID != "agent-2" {
		t.Fatalf("installed order = %#v", installed)
	}

	noRealLaterMessage := InstallCompactedHistoryPrefix(history[:2], replacement, CompactionPrefix{Len: 1}, nil, initialContext)
	if len(noRealLaterMessage) != 3 || noRealLaterMessage[0].ID != "summary" ||
		noRealLaterMessage[1].ID != "world-state" || noRealLaterMessage[2].ID != "agent-1" {
		t.Fatalf("summary boundary installation = %#v", noRealLaterMessage)
	}

	prefixContext := []Item{{ID: "catalog", Type: "additional_tools"}}
	withPrefix := InstallCompactedHistoryPrefix(history, replacement, CompactionPrefix{Len: 4}, prefixContext, initialContext)
	if len(withPrefix) != 3 || withPrefix[0].ID != "catalog" || withPrefix[1].ID != "world-state" || withPrefix[2].ID != "summary" {
		t.Fatalf("prefix context installation = %#v", withPrefix)
	}
	if history[0].ID != "user-1" || replacement[0].ID != "summary" || len(initialContext) != 1 {
		t.Fatal("prefix installation mutated its inputs")
	}
}

func TestRealUserOrAgentMessageExcludesFinalAnswersAndSummariesLikeRust(t *testing.T) {
	finalAnswer, err := json.Marshal(map[string]any{
		"type": "agent_message",
		"content": []any{map[string]any{
			"type": "input_text", "text": "Message Type: FINAL_ANSWER\nfinished",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	progress, err := json.Marshal(map[string]any{
		"type": "agent_message",
		"content": []any{map[string]any{
			"type": "input_text", "text": "Message Type: MESSAGE\nstill working",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		item Item
		want bool
	}{
		{name: "final answer", item: Item{Type: "agent_message", Raw: finalAnswer}, want: false},
		{name: "progress", item: Item{Type: "agent_message", Raw: progress}, want: true},
		{name: "user", item: Item{Type: "message", Role: "user", Text: "request"}, want: true},
		{name: "summary", item: Item{Type: "message", Role: "user", Kind: "compaction_summary", Text: SummaryPrefix + "\nsummary"}, want: false},
		{name: "developer", item: Item{Type: "message", Role: "developer", Text: "guidance"}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsRealUserOrAgentMessage(testCase.item); got != testCase.want {
				t.Fatalf("IsRealUserOrAgentMessage() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestTrimHistoryToContextWindowUsesCapturedWindowLikeRust(t *testing.T) {
	history := []Item{
		{ID: "one", Type: "message", Role: "user", Text: strings.Repeat("alpha ", 500)},
		{ID: "two", Type: "message", Role: "assistant", Text: strings.Repeat("beta ", 500)},
	}
	if got := TrimHistoryToContextWindow(history, 100_000, "instructions"); len(got) != 2 {
		t.Fatalf("under-window history = %#v", got)
	}
	trimmed := TrimHistoryToContextWindow(history, 100, "instructions")
	if len(trimmed) >= len(history) || trimmed[len(trimmed)-1].ID != "two" {
		t.Fatalf("trimmed history = %#v, want the newest item retained", trimmed)
	}
	if version := HistoryVersion(history); version == 0 || version != HistoryVersion(append([]Item(nil), history...)) {
		t.Fatalf("history version = %d, want stable non-zero fingerprint", version)
	}
	if HistoryVersion(history) == HistoryVersion(history[:1]) {
		t.Fatal("different histories produced the same version")
	}
}
