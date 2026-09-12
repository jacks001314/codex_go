package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

func recapMessages(entries ...codextui.Message) []codextui.Message {
	return entries
}

func recapUser(text string) codextui.Message {
	return codextui.Message{Role: codextui.RoleUser, Text: text}
}

func recapAssistant(text string) codextui.Message {
	return codextui.Message{Role: codextui.RoleAssistant, Text: text}
}

func TestRecapHistoryPreservesChronologicalUserAndAssistantMessages(t *testing.T) {
	messages := recapMessages(
		recapUser("First request"),
		recapAssistant("First response"),
		codextui.Message{Role: codextui.RoleHistory, Text: "tool output"},
		recapUser("Second request"),
		recapAssistant("Streaming response"),
	)
	want := "User: First request\n\nAssistant: First response\n\nUser: Second request\n\nAssistant: Streaming response"
	if got := RecapHistory(messages); got != want {
		t.Fatalf("RecapHistory = %q, want %q", got, want)
	}
}

func TestRecapHistoryKeepsOnlyTheMostRecentEightUserTurns(t *testing.T) {
	totalTurns := recapHistoryMaxTurns + 2
	var messages []codextui.Message
	for index := 0; index < totalTurns; index++ {
		messages = append(messages, recapUser(fmt.Sprintf("question-%d", index)))
		messages = append(messages, recapAssistant(fmt.Sprintf("answer-%d", index)))
	}
	var expected []string
	for index := 2; index < totalTurns; index++ {
		expected = append(expected, fmt.Sprintf("User: question-%d\n\nAssistant: answer-%d", index, index))
	}
	if got := RecapHistory(messages); got != strings.Join(expected, "\n\n") {
		t.Fatalf("RecapHistory = %q", got)
	}
}

func TestRecapHistoryIgnoresActivityAndEmptyMessages(t *testing.T) {
	messages := recapMessages(
		recapUser("   "),
		codextui.Message{Role: codextui.RoleHistory, Text: "tool output"},
		recapUser("Implement recap"),
		recapAssistant(" \n "),
		recapAssistant("Done"),
	)
	if got := RecapHistory(messages); got != "User: Implement recap\n\nAssistant: Done" {
		t.Fatalf("RecapHistory = %q", got)
	}
	if got := RecapHistory(nil); got != "" {
		t.Fatalf("RecapHistory(nil) = %q, want empty", got)
	}
}

func TestRecapHistoryPreservesLatestUserTurnWhenLatestResponseIsOversized(t *testing.T) {
	oversized := strings.Repeat("\U0001f31f", RecapPromptMaxBytes*2)
	prompt := RecapPrompt(RecapHistory(recapMessages(recapUser("Keep this latest request"), recapAssistant(oversized))))
	if len(prompt) > RecapPromptMaxBytes {
		t.Fatalf("prompt bytes = %d, want <= %d", len(prompt), RecapPromptMaxBytes)
	}
	if !strings.Contains(prompt, "User: Keep this latest request") {
		t.Fatalf("prompt missing the latest user request:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Assistant: \U0001f31f") {
		t.Fatalf("prompt missing the assistant prefix:\n%s", prompt)
	}
}

func TestRecapHistoryCapsUTF8BytesWithoutSplittingCharacters(t *testing.T) {
	content := strings.Repeat("最新の進捗\U0001f31f", RecapPromptMaxBytes)
	history := RecapHistory(recapMessages(recapUser(content)))
	if prompt := RecapPrompt(history); len(prompt) > RecapPromptMaxBytes {
		t.Fatalf("prompt bytes = %d, want <= %d", len(prompt), RecapPromptMaxBytes)
	}
	if !strings.HasPrefix(history, "User: 最新の進捗\U0001f31f") {
		t.Fatalf("history = %q", history)
	}
	if !utf8.ValidString(history) {
		t.Fatalf("history split a character: %q", history)
	}
}

func TestRenderRecapMessageBoundsAtUTF8Boundary(t *testing.T) {
	if _, ok := RenderRecapMessage("User", "hello", 3); ok {
		t.Fatal("a budget smaller than the role prefix should fail")
	}
	rendered, ok := RenderRecapMessage("User", "hello", len("User: "))
	if !ok || rendered != "User: " {
		t.Fatalf("rendered = %q ok=%v", rendered, ok)
	}
	rendered, ok = RenderRecapMessage("User", "最新の進捗", len("User: ")+4)
	if !ok || rendered != "User: 最" {
		t.Fatalf("utf8 rendered = %q ok=%v", rendered, ok)
	}
}

func TestParseRecapIsNormalizedAndBounded(t *testing.T) {
	bounded := strings.Repeat("\U0001f31f", RecapMaxChars)
	cases := []struct {
		response string
		want     string
		ok       bool
	}{
		{`{"recap":"  Fixed the parser.  \n"}`, "Fixed the parser.", true},
		{`{"recap":"` + bounded + `discarded"}`, bounded, true},
		{"not json", "", false},
		{`{"recap":"  \t  "}`, "", false},
	}
	for _, test := range cases {
		got, ok := ParseRecap(test.response)
		if ok != test.ok || got != test.want {
			t.Fatalf("ParseRecap(%q) = %q,%v want %q,%v", test.response, got, ok, test.want, test.ok)
		}
	}
}

func TestRecapOutputSchemaRequiresARecapString(t *testing.T) {
	schema := RecapOutputSchema()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema = %#v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	recap, ok := properties["recap"].(map[string]any)
	if !ok || recap["type"] != "string" || recap["minLength"] != 1 || recap["maxLength"] != RecapMaxChars {
		t.Fatalf("recap property = %#v", properties["recap"])
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "recap" {
		t.Fatalf("required = %#v", schema["required"])
	}
}

func TestRecapRequiresFocusLossAndThreeCompletedTurns(t *testing.T) {
	now := time.Now()
	var state RecapState
	state.NoteTurnFinished(appserver.TurnStatusCompleted, now)
	state.NoteTurnFinished(appserver.TurnStatusCompleted, now)
	if state.ShouldGenerate(now.Add(RecapDelay)) {
		t.Fatal("two completed turns are not enough")
	}
	state.NoteFocusLost(now)
	if state.ShouldGenerate(now.Add(RecapDelay)) {
		t.Fatal("focus loss alone is not enough")
	}
	state.NoteTurnFinished(appserver.TurnStatusCompleted, now)
	if !state.ShouldGenerate(now.Add(RecapDelay)) {
		t.Fatal("three completed turns after focus loss should be ready")
	}
}

func TestRecapWaitsAfterFocusLossEvenIfTurnCompletedEarlier(t *testing.T) {
	started := time.Now()
	var state RecapState
	for i := 0; i < 3; i++ {
		state.NoteTurnFinished(appserver.TurnStatusCompleted, started)
	}
	focusLost := started.Add(RecapDelay)
	state.NoteFocusLost(focusLost)
	if state.ShouldGenerate(focusLost) {
		t.Fatal("immediately after focus loss should not generate")
	}
	if state.ShouldGenerate(focusLost.Add(RecapDelay - time.Second)) {
		t.Fatal("before the delay should not generate")
	}
	if !state.ShouldGenerate(focusLost.Add(RecapDelay)) {
		t.Fatal("at the delay should generate")
	}
}

func TestRecapCompletedTurnResetsDeadlineWhileUnfocused(t *testing.T) {
	focusLost := time.Now()
	var state RecapState
	state.NoteFocusLost(focusLost)
	for i := 0; i < 3; i++ {
		state.NoteTurnFinished(appserver.TurnStatusCompleted, focusLost)
	}
	lastCompleted := focusLost.Add(30 * time.Second)
	state.NoteTurnFinished(appserver.TurnStatusCompleted, lastCompleted)
	if state.ShouldGenerate(focusLost.Add(RecapDelay)) {
		t.Fatal("the new turn should push the deadline out")
	}
	if !state.ShouldGenerate(lastCompleted.Add(RecapDelay)) {
		t.Fatal("the deadline should follow the latest completed turn")
	}
}

func TestRecapFailedTurnInvalidatesReadyRecapWithoutCounting(t *testing.T) {
	focusLost := time.Now()
	var state RecapState
	state.NoteFocusLost(focusLost)
	for i := 0; i < 3; i++ {
		state.NoteTurnFinished(appserver.TurnStatusCompleted, focusLost)
	}
	failedAt := focusLost.Add(RecapDelay)
	state.NoteTurnFinished(appserver.TurnStatusFailed, failedAt)
	if state.CompletedTurns != 3 || state.TurnRevision != 4 {
		t.Fatalf("state = %#v", state)
	}
	if state.ShouldGenerate(failedAt) {
		t.Fatal("a failed turn should invalidate the ready recap")
	}
	if !state.ShouldGenerate(failedAt.Add(RecapDelay)) {
		t.Fatal("the deadline should follow the failed turn")
	}
}

func TestRecapRepeatedFocusLossDoesNotRestartDelay(t *testing.T) {
	focusLost := time.Now()
	var state RecapState
	state.NoteFocusLost(focusLost)
	for i := 0; i < 3; i++ {
		state.NoteTurnFinished(appserver.TurnStatusCompleted, focusLost)
	}
	state.NoteFocusLost(focusLost.Add(30 * time.Second))
	if !state.ShouldGenerate(focusLost.Add(RecapDelay)) {
		t.Fatal("repeated focus loss must not restart the delay")
	}
}

func TestRecapRegainingFocusPreventsGeneration(t *testing.T) {
	now := time.Now()
	var state RecapState
	state.NoteFocusLost(now)
	for i := 0; i < 3; i++ {
		state.NoteTurnFinished(appserver.TurnStatusCompleted, now)
	}
	state.NoteFocusGained()
	if state.ShouldGenerate(now.Add(RecapDelay)) {
		t.Fatal("regaining focus should prevent generation")
	}
}

func TestRecapAnotherRecapRequiresTwoAdditionalCompletedTurns(t *testing.T) {
	now := time.Now()
	var state RecapState
	state.NoteFocusLost(now)
	state.SeedFromProgress(RecapProgress{CompletedTurns: 3}, now)
	state.MarkRecapped(3)
	if state.ShouldGenerate(now.Add(RecapDelay)) {
		t.Fatal("one recap per three turns")
	}
	fourth := now.Add(RecapDelay)
	state.NoteTurnFinished(appserver.TurnStatusCompleted, fourth)
	if state.ShouldGenerate(fourth.Add(RecapDelay)) {
		t.Fatal("a fourth turn is not enough for another recap")
	}
	fifth := fourth.Add(time.Second)
	state.NoteTurnFinished(appserver.TurnStatusCompleted, fifth)
	if !state.ShouldGenerate(fifth.Add(RecapDelay)) {
		t.Fatal("a fifth turn should allow another recap")
	}
}

func TestRecapResetForNewThreadPreservesFocus(t *testing.T) {
	now := time.Now()
	var state RecapState
	state.NoteFocusLost(now)
	three := 3
	state.SeedFromProgress(RecapProgress{CompletedTurns: 3, LastRecappedTurnCount: &three}, now)
	replacedAt := now.Add(30 * time.Second)
	state.ResetForNewThread(replacedAt)
	if state.Progress() != (RecapProgress{}) {
		t.Fatalf("progress = %#v", state.Progress())
	}
	if state.UnfocusedSince == nil || !state.UnfocusedSince.Equal(replacedAt) {
		t.Fatalf("unfocused since = %v", state.UnfocusedSince)
	}
	if state.ShouldGenerate(replacedAt.Add(RecapDelay)) {
		t.Fatal("a replaced thread starts fresh")
	}
}

func TestRecapSeedFromTurnsCountsOnlyCompletedTurns(t *testing.T) {
	now := time.Now()
	var state RecapState
	state.NoteFocusLost(now)
	state.SeedFromTurns([]appserver.Turn{
		{Status: appserver.TurnStatusCompleted},
		{Status: appserver.TurnStatusFailed},
		{Status: appserver.TurnStatusCompleted},
		{Status: appserver.TurnStatusInterrupted},
		{Status: appserver.TurnStatusInProgress},
		{Status: appserver.TurnStatusCompleted},
	}, now)
	if state.CompletedTurns != 3 {
		t.Fatalf("completed turns = %d, want 3", state.CompletedTurns)
	}
	if !state.ShouldGenerate(now.Add(RecapDelay)) {
		t.Fatal("three restored completed turns should be ready")
	}
}

func TestRecapSeedNeverReducesObservedCompletedTurns(t *testing.T) {
	now := time.Now()
	var state RecapState
	for i := 0; i < 4; i++ {
		state.NoteTurnFinished(appserver.TurnStatusCompleted, now)
	}
	state.SeedFromTurns([]appserver.Turn{
		{Status: appserver.TurnStatusCompleted},
		{Status: appserver.TurnStatusCompleted},
		{Status: appserver.TurnStatusCompleted},
	}, now)
	if state.CompletedTurns != 4 {
		t.Fatalf("completed turns = %d, want 4", state.CompletedTurns)
	}
}

func TestRecapBeginRetryGatesPerTurnRevision(t *testing.T) {
	var state RecapState
	state.TurnRevision = 2
	if !state.BeginRetry(2) {
		t.Fatal("the first retry for a revision should proceed")
	}
	if state.BeginRetry(2) {
		t.Fatal("a repeated retry for the same revision should be skipped")
	}
	if state.BeginRetry(3) {
		t.Fatal("a stale revision should not schedule a retry")
	}
	state.TurnRevision = 3
	if !state.BeginRetry(3) {
		t.Fatal("a new revision should schedule a retry")
	}
}
