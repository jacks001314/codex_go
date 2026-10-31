package appserver

import (
	"strings"
	"testing"

	codexctx "codex_go/context"
	"codex_go/retainedctx"
	"codex_go/state"
)

// Mirrors Rust #58454
// (guardian-context/src/preparation/source_tests.rs): elastic root preparation
// preserves every captured message and its full text, keeps legacy source
// deduplication, and still reports the omissions it makes.
func TestGuardianElasticRootPreparationPreservesCapturedMessagesLikeRust(t *testing.T) {
	const rootRecords = 20
	text := strings.Repeat("root evidence ", 1000)
	retainedText, _ := codexctx.GuardianTruncateText(text, 900)
	legacy := retainedctx.RetainedUserMessage{
		TurnID:   "legacy-turn",
		Text:     text,
		Complete: false,
	}
	retained := retainedctx.RetainedUserMessage{
		TurnID:   "legacy-turn",
		Text:     retainedText,
		Complete: false,
	}
	input := guardianRootPreparationInput{
		retainedUserSources:     []retainedctx.RetainedUserMessage{retained},
		missingRootInstructions: true,
	}
	for index := 0; index < rootRecords; index++ {
		input.userMessages = append(input.userMessages, guardianRootCandidate{
			message:  state.RootMessage{Kind: state.RootMessageUser, Text: text},
			order:    uint64(index),
			hasOrder: true,
		})
	}
	input.userMessages[0].message.Text = retainedText

	fixed := prepareGuardianRootContextWithBudgeting(input, []retainedctx.RetainedUserMessage{legacy}, codexctx.GuardianContextBudgetingFixed)
	elastic := prepareGuardianRootContextWithBudgeting(input, []retainedctx.RetainedUserMessage{legacy}, codexctx.GuardianContextBudgetingElastic)

	want := []state.RootMessage{
		{Kind: state.RootMessageRetainedContextScope},
		{Kind: state.RootMessageIncompleteRootInstructions},
		{Kind: state.RootMessageUser, Text: retainedText},
	}
	for index := 1; index < rootRecords; index++ {
		want = append(want, state.RootMessage{Kind: state.RootMessageUser, Text: text})
	}
	if len(elastic.messages) != len(want) {
		t.Fatalf("elastic messages = %d, want %d", len(elastic.messages), len(want))
	}
	for index := range want {
		if elastic.messages[index] != want[index] {
			t.Fatalf("elastic message %d = %#v, want %#v", index, elastic.messages[index], want[index])
		}
	}
	if len(fixed.messages) >= len(elastic.messages) {
		t.Fatalf("fixed messages = %d, want fewer than the %d elastic messages", len(fixed.messages), len(elastic.messages))
	}
	for _, message := range fixed.messages {
		if message.Text == text {
			t.Fatal("fixed preparation kept uncapped root text")
		}
	}
}
