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
	content := strings.Repeat("鏈€鏂般伄閫叉崡\U0001f31f", RecapPromptMaxBytes)
	history := RecapHistory(recapMessages(recapUser(content)))
	if prompt := RecapPrompt(history); len(prompt) > RecapPromptMaxBytes {
		t.Fatalf("prompt bytes = %d, want <= %d", len(prompt), RecapPromptMaxBytes)
	}
	if !strings.HasPrefix(history, "Pending user request: 鏈€鏂般伄閫叉崡\U0001f31f") {
		t.Fatalf("history = %q", history)
	}
	if !utf8.ValidString(history) {
		t.Fatalf("history split a character: %q", history)
	}
}

func TestExcerptRecapFieldKeepsBothEndsAtRuneBoundary(t *testing.T) {
	text := strings.Repeat("\U0001f31f", 20) + "MAGIC_TAIL"
	excerpted := excerptRecapField(text, 40)
	if !strings.Contains(excerpted, "...") || len(excerpted) > 40 {
		t.Fatalf("excerpt = %q len=%d", excerpted, len(excerpted))
	}
	if !utf8.ValidString(excerpted) {
		t.Fatalf("excerpt split a character: %q", excerpted)
	}
	if got := excerptRecapField("short", 40); got != "short" {
		t.Fatalf("short field = %q", got)
	}
}

func TestParseRecapRequiresStructuredSummaryAndNextAction(t *testing.T) {
	bounded := strings.Repeat("\U0001f31f", RecapMaxChars)
	tooLongNext := strings.Repeat("\U0001f31f", RecapNextMaxChars+1)
	cases := []struct {
		name     string
		response string
		wantSum  string
		wantNext *string
		ok       bool
	}{
		{"normalized", `{"summary":"  Fixed the parser.  \n","next_action":null}`, "Fixed the parser.", nil, true},
		{"next action", `{"summary":"Fixed.","next_action":"  Run tests.  "}`, "Fixed.", strPtr("Run tests."), true},
		{"blank next action", `{"summary":"Fixed.","next_action":"   "}`, "Fixed.", nil, true},
		{"max summary", `{"summary":"` + bounded + `","next_action":null}`, bounded, nil, true},
		{"missing next_action", `{"summary":"Fixed."}`, "", nil, false},
		{"null summary", `{"summary":null,"next_action":null}`, "", nil, false},
		{"blank summary", `{"summary":"  \t  ","next_action":null}`, "", nil, false},
		{"oversized summary", `{"summary":"` + bounded + `x","next_action":null}`, "", nil, false},
		{"oversized next action", `{"summary":"Fixed.","next_action":"` + tooLongNext + `"}`, "", nil, false},
		{"unknown field", `{"recap":"Fixed."}`, "", nil, false},
		{"extra field", `{"summary":"Fixed.","next_action":null,"extra":1}`, "", nil, false},
		{"not json", "not json", "", nil, false},
		{"trailing content", `{"summary":"Fixed.","next_action":null} {}`, "", nil, false},
	}
	for _, test := range cases {
		summary, next, ok := ParseRecap(test.response)
		if ok != test.ok || summary != test.wantSum {
			t.Fatalf("%s: ParseRecap(%q) = %q,next=%v,ok=%v want %q,ok=%v", test.name, test.response, summary, next, ok, test.wantSum, test.ok)
		}
		if (test.wantNext == nil) != (next == nil) || (test.wantNext != nil && *test.wantNext != *next) {
			t.Fatalf("%s: next action = %v, want %v", test.name, next, test.wantNext)
		}
	}
}

func strPtr(value string) *string { return &value }

func TestRecapOutputSchemaRequiresSummaryAndNullableNextAction(t *testing.T) {
	schema := RecapOutputSchema()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema = %#v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	summary, ok := properties["summary"].(map[string]any)
	if !ok || summary["type"] != "string" || summary["minLength"] != 1 || summary["maxLength"] != RecapMaxChars {
		t.Fatalf("summary property = %#v", properties["summary"])
	}
	nextAction, ok := properties["next_action"].(map[string]any)
	if !ok || nextAction["maxLength"] != RecapNextMaxChars {
		t.Fatalf("next_action property = %#v", properties["next_action"])
	}
	nextTypes, ok := nextAction["type"].([]string)
	if !ok || len(nextTypes) != 2 || nextTypes[0] != "string" || nextTypes[1] != "null" {
		t.Fatalf("next_action type = %#v", nextAction["type"])
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 2 || required[0] != "summary" || required[1] != "next_action" {
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

func TestRecapHistoryKeepsEightExchangesAndThePendingCorrectionOnce(t *testing.T) {
	var messages []codextui.Message
	for index := 0; index < 10; index++ {
		messages = append(messages, recapUser(fmt.Sprintf("request-%d", index)))
		messages = append(messages, recapAssistant(fmt.Sprintf("result-%d", index)))
	}
	messages = append(messages, recapUser("Keep this queued; do not implement it."))
	var blocks []string
	for index := 2; index < 10; index++ {
		blocks = append(blocks, fmt.Sprintf("User: request-%d\n\nAssistant: result-%d", index, index))
	}
	blocks = append(blocks, "Pending user request: Keep this queued; do not implement it.")
	if got, want := RecapHistory(messages), strings.Join(blocks, "\n\n"); got != want {
		t.Fatalf("RecapHistory = %q, want %q", got, want)
	}
}

func TestRecapHistoryPreservesSteeringAndIntermediateProgress(t *testing.T) {
	messages := recapMessages(
		recapAssistant("Orphaned older answer"),
		recapUser("Fix the parser"),
		recapUser("Keep the API unchanged"),
		recapAssistant("The parser fix is implemented"),
		recapAssistant("All twelve tests pass"),
		recapUser("Fix the parser"),
	)
	want := "User: Fix the parser\n\nKeep the API unchanged\n\nAssistant: The parser fix is implemented\n\nAll twelve tests pass\n\nPending user request: Fix the parser"
	if got := RecapHistory(messages); got != want {
		t.Fatalf("RecapHistory = %q, want %q", got, want)
	}
	if got := RecapHistory(recapMessages(recapAssistant("Orphaned answer"))); got != "" {
		t.Fatalf("assistant-only history = %q, want empty", got)
	}
}

func TestRecapHistoryDropsOldWholeExchangesBeforeClippingNewest(t *testing.T) {
	messages := recapMessages(
		recapUser("Old request"),
		recapAssistant(strings.Repeat("old ", RecapPromptMaxBytes)),
		recapUser("Current request"),
		recapAssistant("Implemented; what should happen on empty input?"),
	)
	want := "[Earlier exchanges omitted]\n\nUser: Current request\n\nAssistant: Implemented; what should happen on empty input?"
	if got := RecapHistory(messages); got != want {
		t.Fatalf("RecapHistory = %q, want %q", got, want)
	}
}

func TestRecapHistoryExcerptsBothEndsAndKeepsTheLatestCorrection(t *testing.T) {
	large := strings.Repeat("\u6700\u65b0\U0001f980", RecapPromptMaxBytes)
	messages := recapMessages(
		recapUser("Request start "+large+" request end"),
		recapAssistant("Answer start "+large+" what should empty input do?"),
		recapUser("Correction start "+large+" keep it queued"),
	)
	history := RecapHistory(messages)
	if prompt := RecapPrompt(history); len(prompt) > RecapPromptMaxBytes {
		t.Fatalf("prompt bytes = %d, want <= %d", len(prompt), RecapPromptMaxBytes)
	}
	for _, want := range []string{
		"User: Request start",
		"request end",
		"Assistant: Answer start",
		"what should empty input do?",
		"Pending user request: Correction start",
		"keep it queued",
	} {
		if !strings.Contains(history, want) {
			t.Fatalf("history missing %q:\n%s", want, history)
		}
	}
	if got := strings.Count(history, "[... excerpted ...]"); got != 3 {
		t.Fatalf("excerpt markers = %d, want 3", got)
	}
}

func TestRecapHistoryOversizedRequestUsesSpaceLeftByShortReply(t *testing.T) {
	messages := recapMessages(
		recapUser("Request start "+strings.Repeat("\U0001f980", RecapPromptMaxBytes)+" request end"),
		recapAssistant("Implemented; twelve tests pass."),
		recapUser("Keep further work queued."),
	)
	prompt := RecapPrompt(RecapHistory(messages))
	if len(prompt) > RecapPromptMaxBytes {
		t.Fatalf("prompt bytes = %d, want <= %d", len(prompt), RecapPromptMaxBytes)
	}
	if RecapPromptMaxBytes-len(prompt) >= 8 {
		t.Fatalf("prompt leaves %d bytes unused, want < 8", RecapPromptMaxBytes-len(prompt))
	}
	for _, want := range []string{
		"User: Request start",
		"request end",
		"Assistant: Implemented; twelve tests pass.",
		"Pending user request: Keep further work queued.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
