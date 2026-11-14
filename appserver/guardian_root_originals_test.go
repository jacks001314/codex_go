package appserver

import (
	codexctx "codex_go/context"
	"codex_go/state"
	"reflect"
	"strings"
	"testing"
)

// Rust #61185: preparing a fixed request must not consume or truncate the
// source evidence needed by a subsequent elastic request.
func TestGuardianFixedPreparationKeepsRootOriginalsForElasticRetry(t *testing.T) {
	original := strings.Repeat("retain the original authorization evidence. ", 2000)
	input := guardianRootPreparationInput{}
	for i := 0; i < guardianMaxRootMessages+4; i++ {
		input.userMessages = append(input.userMessages, guardianRootCandidate{
			message: state.RootMessage{Kind: state.RootMessageUser, Text: original},
			order:   uint64(i), hasOrder: true,
		})
	}
	before := append([]guardianRootCandidate(nil), input.userMessages...)
	first := prepareGuardianRootContext(input, nil)
	if !reflect.DeepEqual(input.userMessages, before) {
		t.Fatal("fixed preparation mutated source evidence")
	}
	elastic := prepareGuardianRootContextWithBudgeting(input, nil, codexctx.GuardianContextBudgetingElastic)
	count := 0
	for _, message := range elastic.messages {
		if message.Kind == state.RootMessageUser {
			count++
			if message.Text != original {
				t.Fatal("elastic preparation received truncated evidence")
			}
		}
	}
	if count != len(before) {
		t.Fatalf("elastic records=%d, want %d", count, len(before))
	}
	second := prepareGuardianRootContext(input, nil)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("elastic request changed later fixed preparation")
	}
	for _, message := range first.messages {
		if message.Kind == state.RootMessageUser && message.Text == original {
			t.Fatal("fixed preparation did not apply its cap")
		}
	}
}
