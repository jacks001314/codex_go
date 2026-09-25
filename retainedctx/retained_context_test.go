package retainedctx

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func stringPtr(value string) *string { return &value }

func uintPtr(value uint64) *uint64 { return &value }

func publishAnswer() RetainedContextEvent {
	return RetainedContextEvent{Answer: VerifiedAnswer{
		TurnID: "turn-1",
		CallID: "ask-1",
		Questions: []VerifiedQuestionAnswer{{
			Question: "Publish?",
			Answer:   "Yes, but never publicly.",
		}},
	}}
}

func roundTripContext(t *testing.T, context *RetainedContext) *RetainedContext {
	t.Helper()
	data, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("marshal retained answer fixture: %v", err)
	}
	var out RetainedContext
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal retained answer fixture: %v", err)
	}
	return &out
}

// contextValue canonicalizes a snapshot through its wire form so Go's nil and
// empty slices compare equal, as Rust's empty deques do.
func contextValue(t *testing.T, context *RetainedContext) any {
	t.Helper()
	data, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("marshal retained context: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal retained context: %v", err)
	}
	return out
}

func sameContext(t *testing.T, got *RetainedContext, want *RetainedContext) bool {
	t.Helper()
	return reflect.DeepEqual(contextValue(t, got), contextValue(t, want))
}

func orderedTexts(entries []OrderedEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Entry.UserMessage != nil:
			out = append(out, entry.Entry.UserMessage.Text)
		case entry.Entry.AssistantMessage != nil:
			out = append(out, entry.Entry.AssistantMessage.Text)
		case entry.Entry.VerifiedAnswer != nil:
			out = append(out, entry.Entry.VerifiedAnswer.Questions[0].Answer)
		}
	}
	return out
}

func TestRetainedEvidencePreservesOrderThroughRecoveryCheckpointAndRollback(t *testing.T) {
	context := &RetainedContext{}
	first := publishAnswer()
	if !context.Record(first) {
		t.Fatal("recording a fresh verified answer must report a change")
	}
	beforeRestriction := context.Clone()
	context.RecordUserMessage(RetainedUserMessage{
		TurnID:    "revocation-turn",
		MessageID: stringPtr("revocation"),
		Complete:  false,
	}, LocalInputSource(nil))
	expected := context.Clone()
	expected.userMessages[0].Value.Text = "Do not publish after all."
	context.RecoverUserMessageExcerpts(func(id string) (string, bool) {
		if id != "revocation" {
			t.Fatalf("recovered id = %q, want revocation", id)
		}
		return "Do not publish after all.", true
	})
	if !sameContext(t, context, expected) {
		t.Fatalf("recovered excerpts drifted:\n got %#v\nwant %#v", context, expected)
	}
	context.RecoverUserMessageExcerpts(func(string) (string, bool) {
		t.Fatal("existing text must not be replaced")
		return "", false
	})
	snapshot := context.Clone()
	if context.Record(first) {
		t.Fatal("re-recording the same verified answer must be idempotent")
	}
	if !sameContext(t, context, snapshot) {
		t.Fatal("idempotent recording mutated the snapshot")
	}
	checkpoint := roundTripContext(t, context)
	restored := &RetainedContext{}
	restored.Restore(checkpoint, nil)
	if !sameContext(t, restored, snapshot) {
		t.Fatalf("checkpoint restore drifted:\n got %#v\nwant %#v", restored, snapshot)
	}
	if got, want := orderedTexts(restored.OrderedEntries()), []string{"Yes, but never publicly.", "Do not publish after all."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered texts = %v, want %v", got, want)
	}
	restored.Rollback([]string{"revocation-turn"}, stringPtr("revocation"), LocalInputSource(nil))
	want := beforeRestriction.Clone()
	want.nextOrder = snapshot.nextOrder
	if !sameContext(t, restored, want) {
		t.Fatalf("rollback drifted:\n got %#v\nwant %#v", restored, want)
	}
}

func TestRetainedFamiliesEnforceStorageLimitsWithoutChangingSnapshots(t *testing.T) {
	context := &RetainedContext{}
	first := publishAnswer()
	context.Record(first)
	snapshot := context.Clone()

	for index := 2; index <= 10; index++ {
		context.Record(RetainedContextEvent{Answer: VerifiedAnswer{
			TurnID: "turn-2",
			CallID: fmt.Sprintf("ask-%d", index),
			Questions: []VerifiedQuestionAnswer{{
				Question: "Continue?",
				Answer:   "Yes",
			}},
		}})
	}
	if context.VerifiedAnswersComplete() {
		t.Fatal("eviction must mark the verified-answer family incomplete")
	}
	if got := len(context.VerifiedAnswers()); got != MaxFamilyRecords {
		t.Fatalf("verified answers = %d, want %d", got, MaxFamilyRecords)
	}
	context.Rollback([]string{"turn-2"}, nil, LocalInputSource(nil))
	if got := len(context.VerifiedAnswers()); got != 0 {
		t.Fatalf("verified answers after rollback = %d, want 0", got)
	}
	if context.VerifiedAnswersComplete() {
		t.Fatal("a rolled-back family must stay incomplete")
	}

	restored := snapshot.Clone()
	oversized := publishAnswer()
	oversized.Answer.Questions[0].Answer = strings.Repeat("a", MaxRecordBytes)
	restored.Record(oversized)
	if restored.VerifiedAnswersComplete() {
		t.Fatal("an oversized record must mark the family incomplete")
	}
	if answers := restored.VerifiedAnswers(); len(answers) != 1 || len(answers[0].Questions) != 0 {
		t.Fatalf("oversized answer = %#v, want one entry with no questions", answers)
	}
	if got := snapshot.VerifiedAnswers()[0].Questions[0].Answer; got != "Yes, but never publicly." {
		t.Fatalf("snapshot answer = %q, want the original text", got)
	}

	for index := 0; index <= MaxFamilyRecords; index++ {
		restored.RecordUserMessage(RetainedUserMessage{
			TurnID:    "later-turn",
			MessageID: stringPtr(fmt.Sprintf("message-%d", index)),
			Text:      "Keep the repository private.",
			Complete:  true,
		}, LocalInputSource(nil))
	}
	if restored.UserMessagesComplete() {
		t.Fatal("eviction must mark the user-message family incomplete")
	}
	userMessageCount := 0
	for _, entry := range restored.OrderedEntries() {
		if entry.Entry.UserMessage != nil {
			userMessageCount++
		}
	}
	if userMessageCount != MaxFamilyRecords {
		t.Fatalf("retained user messages = %d, want %d", userMessageCount, MaxFamilyRecords)
	}
	recent := restored.Clone()
	restored.RecordUserMessage(RetainedUserMessage{
		TurnID:    "earlier-turn",
		MessageID: stringPtr("delayed-message"),
		Text:      "An older queued instruction.",
		Complete:  true,
	}, LocalInputSource(uintPtr(0)))
	if !sameContext(t, restored, recent) {
		t.Fatal("delayed old input must not evict newer evidence")
	}

	restored.RecordUserMessage(RetainedUserMessage{
		TurnID:    "oversized-turn",
		MessageID: stringPtr("oversized-message"),
		Text:      strings.Repeat("restriction ", MaxRecordBytes),
		Complete:  true,
	}, LocalInputSource(nil))
	entries := restored.OrderedEntries()
	last := entries[len(entries)-1].Entry.UserMessage
	if last == nil {
		t.Fatal("latest user evidence must be a user message")
	}
	if last.Text != "" || last.Complete {
		t.Fatalf("oversized user message = (%q, %v), want empty and incomplete", last.Text, last.Complete)
	}

	restrictions := cloneOrderedUserMessages(restored.userMessages)
	base := restrictions[len(restrictions)-1].Value
	for index := 0; index <= MaxFamilyRecords; index++ {
		order := restored.ReserveOrder()
		message := base
		message.MessageID = stringPtr(fmt.Sprintf("assistant-%d", index))
		message.Text = "Run smoke tests?"
		message.Complete = true
		restored.RecordAssistantMessage(message, LocalInputSource(&order))
	}
	if !reflect.DeepEqual(restored.userMessages, restrictions) {
		t.Fatal("assistant records must not evict user restrictions")
	}
	if got := len(restored.assistantMessages); got != MaxFamilyRecords {
		t.Fatalf("assistant messages = %d, want %d", got, MaxFamilyRecords)
	}
	if !restored.HasOmittedAssistantMessages() {
		t.Fatal("evicted assistant context must be reported as omitted")
	}
}

func TestRecoveredExcerptsObeyRecordAndFamilyLimits(t *testing.T) {
	context := &RetainedContext{}
	for index := 0; index < MaxFamilyRecords; index++ {
		context.RecordUserMessage(RetainedUserMessage{
			TurnID:    "turn-1",
			MessageID: stringPtr(fmt.Sprintf("message-%d", index)),
			Complete:  false,
		}, LocalInputSource(nil))
	}
	unchanged := context.Clone()
	context.RecoverUserMessageExcerpts(func(string) (string, bool) {
		return strings.Repeat("x", MaxRecordBytes), true
	})
	if !sameContext(t, context, unchanged) {
		t.Fatal("an over-budget excerpt must be bounded back to the stored record")
	}
	context.RecoverUserMessageExcerpts(func(string) (string, bool) {
		return strings.Repeat("x", MaxRecordBytes-1_024), true
	})
	if !context.userMessagesIncomplete {
		t.Fatal("family eviction during recovery must mark user messages incomplete")
	}
	if len(context.userMessages) >= MaxFamilyRecords {
		t.Fatalf("recovered family length = %d, want an eviction", len(context.userMessages))
	}
	if size := familyBytes(context.userMessages); size > MaxFamilyBytes {
		t.Fatalf("recovered family bytes = %d, want <= %d", size, MaxFamilyBytes)
	}
}

func TestLegacyCheckpointsMarkUserMessagesIncomplete(t *testing.T) {
	legacyWire := `{"verified_answers":[{"turn_id":"turn-1","call_id":"ask-1",` +
		`"questions":[{"question":"Publish?","answer":"Yes, but never publicly."}]}],"incomplete":false}`
	var legacy RetainedContext
	if err := json.Unmarshal([]byte(legacyWire), &legacy); err != nil {
		t.Fatalf("legacy retained-answer checkpoint: %v", err)
	}
	if !legacy.VerifiedAnswersComplete() {
		t.Fatal("a legacy answer checkpoint with questions is complete")
	}
	if legacy.UserMessagesComplete() {
		t.Fatal("a legacy checkpoint without retained user restrictions is incomplete")
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy checkpoint: %v", err)
	}
	wantWire := `{"verified_answers":[{"turn_id":"turn-1","call_id":"ask-1",` +
		`"questions":[{"question":"Publish?","answer":"Yes, but never publicly."}],` +
		`"order":0}],"incomplete":false,"user_messages":[],"user_messages_incomplete":true,` +
		`"assistant_messages":[],"assistant_messages_incomplete":false,"next_order":0}`
	var gotValue, wantValue any
	if err := json.Unmarshal(encoded, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wantWire), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("legacy checkpoint wire = %s, want %s", encoded, wantWire)
	}
	restored := &RetainedContext{}
	restored.Restore(nil, nil)
	if restored.UserMessagesComplete() {
		t.Fatal("a missing checkpoint cannot establish complete historical user instructions")
	}
}

func TestRestoredInputOrderAccountsForSurvivingLocalSources(t *testing.T) {
	surviving := []*HarnessMetadata{
		{UserInputOrder: uintPtr(7)},
		{UserInputOrder: uintPtr(100), InheritedUserMessage: true},
		{},
	}
	checkpoint := &RetainedContext{nextOrder: 20}
	cases := []struct {
		name          string
		checkpoint    *RetainedContext
		expectedOrder uint64
		complete      bool
	}{
		{name: "no checkpoint", expectedOrder: 8, complete: false},
		{name: "checkpoint", checkpoint: checkpoint, expectedOrder: 20, complete: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restored := &RetainedContext{}
			restored.Restore(testCase.checkpoint, surviving)
			if got := restored.ReserveOrder(); got != testCase.expectedOrder {
				t.Fatalf("reserved order = %d, want %d", got, testCase.expectedOrder)
			}
			if got := len(restored.OrderedEntries()); got != 0 {
				t.Fatalf("retained entries = %d, want 0", got)
			}
			if got := restored.UserMessagesComplete(); got != testCase.complete {
				t.Fatalf("user messages complete = %v, want %v", got, testCase.complete)
			}
		})
	}
}

func TestAcceptedOrderSurvivesDelayedRecordingAndCheckpointReplay(t *testing.T) {
	context := &RetainedContext{}
	// Rejected/canceled input consumes a sequence but never becomes evidence.
	context.ReserveOrder()
	steerOrder := context.ReserveOrder()
	answerOrder := context.ReserveOrder()
	event := publishAnswer()
	event.AcceptanceOrder = &answerOrder
	context.Record(event)
	instruction := RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: stringPtr("steer"),
		Text:      "Keep the repository private.",
		Complete:  true,
	}
	// Assistant delivery after accepted steering must not move the steering past it.
	assistantOrder := context.ReserveOrder()
	assistant := instruction
	assistant.MessageID = stringPtr("assistant")
	assistant.Text = "Publish publicly?"
	context.RecordAssistantMessage(assistant, LocalInputSource(&assistantOrder))
	checkpoint := context.Clone()
	context.RecordUserMessage(instruction, LocalInputSource(&steerOrder))

	resumed := &RetainedContext{}
	resumed.Restore(checkpoint, nil)
	resumed.RecordUserMessage(instruction, LocalInputSource(&steerOrder))
	// This suffix has no captured version metadata, so replay creates a fresh
	// revision. Its original contents and acceptance order still reconstruct
	// identically.
	expected := context.Clone()
	if resumed.userMessages[0].Revision == nil || expected.userMessages[0].Revision == nil {
		t.Fatal("retained user evidence must carry a revision")
	}
	if *resumed.userMessages[0].Revision == *expected.userMessages[0].Revision {
		t.Fatal("replay must mint a fresh revision")
	}
	expected.userMessages[0].Revision = resumed.userMessages[0].Revision
	if !sameContext(t, resumed, expected) {
		t.Fatalf("replayed suffix drifted:\n got %#v\nwant %#v", resumed, expected)
	}
	source := context.Source(RetainedContextEntry{UserMessage: &context.userMessages[0].Value})
	if source == nil {
		t.Fatal("the original envelope must expose its captured source")
	}
	if !resumed.RestoreSourceRevision(source) {
		t.Fatal("restoring the captured revision must find the replayed message")
	}
	if !sameContext(t, resumed, context) {
		t.Fatal("restoring the captured revision must reproduce the original")
	}
	entryOrders := make([]RetainedContextOrder, 0, 3)
	for _, entry := range resumed.OrderedEntries() {
		entryOrders = append(entryOrders, entry.Order)
	}
	wantOrders := []RetainedContextOrder{
		{Order: steerOrder},
		{Order: answerOrder},
		{Order: assistantOrder},
	}
	if !reflect.DeepEqual(entryOrders, wantOrders) {
		t.Fatalf("ordered keys = %#v, want %#v", entryOrders, wantOrders)
	}
	if got, want := orderedTexts(resumed.OrderedEntries()), []string{
		"Keep the repository private.",
		"Yes, but never publicly.",
		"Publish publicly?",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered texts = %v, want %v", got, want)
	}
	// This checkpoint predates model delivery of the queued instruction. Rollback
	// still removes its later-accepted answer using the persisted boundary order.
	beforeDelivery := checkpoint.Clone()
	beforeDelivery.Rollback([]string{"turn-1"}, stringPtr("steer"), LocalInputSource(&steerOrder))
	resumed.Rollback([]string{"turn-1"}, stringPtr("steer"), LocalInputSource(&steerOrder))
	if !sameContext(t, beforeDelivery, resumed) {
		t.Fatalf("checkpoint and live rollback disagree:\n got %#v\nwant %#v", beforeDelivery, resumed)
	}
	want := &RetainedContext{nextOrder: 4}
	if !sameContext(t, resumed, want) {
		t.Fatalf("rolled-back context = %#v, want %#v", resumed, want)
	}
}

func TestAdoptedInstructionsPreserveLocalOrderAndRollbackScope(t *testing.T) {
	context := &RetainedContext{}
	context.Record(publishAnswer())
	for index := 0; index < 2; index++ {
		message := RetainedUserMessage{
			TurnID:    "parent-turn",
			MessageID: stringPtr(fmt.Sprintf("parent-%d", index)),
			Text:      fmt.Sprintf("Parent instruction %d", index),
			Complete:  true,
		}
		context.RecordUserMessage(message, InheritedInputSource())
		context.RecordUserMessage(message, InheritedInputSource())
		context.RecordAssistantMessage(message, InheritedInputSource())
	}
	if got := context.ReserveOrder(); got != 1 {
		t.Fatalf("reserved order = %d, want 1", got)
	}
	if context.VerifiedAnswersComplete() {
		t.Fatal("an adopted prefix must leave authorization evidence incomplete")
	}
	orders := make([]RetainedContextOrder, 0, 5)
	for _, entry := range context.OrderedEntries() {
		orders = append(orders, entry.Order)
	}
	wantOrders := []RetainedContextOrder{
		{Inherited: true, Order: 0},
		{Inherited: true, Order: 1},
		{Inherited: true, Order: 2},
		{Inherited: true, Order: 3},
		{Order: 0},
	}
	if !reflect.DeepEqual(orders, wantOrders) {
		t.Fatalf("ordered keys = %#v, want %#v", orders, wantOrders)
	}
	checkpoint := roundTripContext(t, context)
	restored := &RetainedContext{}
	restored.Restore(checkpoint, nil)
	if !sameContext(t, restored, context) {
		t.Fatal("checkpoint restore drifted")
	}
	// Older roots adopted complete instructions without recording the missing
	// parent answers.
	legacy := checkpoint.Clone()
	legacy.verifiedAnswersIncomplete = false
	restored.Restore(legacy, nil)
	if !sameContext(t, restored, context) {
		t.Fatal("restore must repair a pre-adoption checkpoint's answer gap")
	}
	restored.Rollback([]string{"turn-1"}, nil, LocalInputSource(uintPtr(0)))
	expected := context.Clone()
	expected.verifiedAnswers = nil
	if !sameContext(t, restored, expected) {
		t.Fatalf("local rollback drifted:\n got %#v\nwant %#v", restored, expected)
	}
	restored.Rollback([]string{"parent-turn"}, stringPtr("parent-1"), InheritedInputSource())
	expected.userMessages = expected.userMessages[:len(expected.userMessages)-1]
	expected.assistantMessages = expected.assistantMessages[:len(expected.assistantMessages)-1]
	if !sameContext(t, restored, expected) {
		t.Fatalf("inherited rollback drifted:\n got %#v\nwant %#v", restored, expected)
	}
}

func TestAssistantMessagesSkipEmptyCompleteAndKeepOmissionNotices(t *testing.T) {
	context := &RetainedContext{}
	base := RetainedUserMessage{TurnID: "turn-1", Text: "Run smoke tests?", Complete: true}

	order := context.ReserveOrder()
	emptyComplete := base
	emptyComplete.MessageID = stringPtr("assistant-empty")
	emptyComplete.Text = ""
	if source := context.RecordAssistantMessage(emptyComplete, LocalInputSource(&order)); source != nil {
		t.Fatal("a complete, empty assistant message must not be recorded")
	}
	if len(context.assistantMessages) != 0 {
		t.Fatal("a complete, empty assistant message must not create an entry")
	}

	order = context.ReserveOrder()
	real := base
	real.MessageID = stringPtr("assistant-real")
	source := context.RecordAssistantMessage(real, LocalInputSource(&order))
	if source == nil {
		t.Fatal("real assistant context must record its source")
	}
	if !reflect.DeepEqual(context.RecordAssistantMessage(real, LocalInputSource(&order)), source) {
		t.Fatal("an unchanged assistant delivery must dedupe to the original source")
	}
	if got := len(context.assistantMessages); got != 1 {
		t.Fatalf("assistant messages = %d, want 1", got)
	}

	// An incomplete, empty message keeps its omission notice.
	order = context.ReserveOrder()
	omitted := base
	omitted.MessageID = stringPtr("assistant-omitted")
	omitted.Complete = false
	omitted.Text = ""
	if source := context.RecordAssistantMessage(omitted, LocalInputSource(&order)); source == nil {
		t.Fatal("an incomplete assistant message must be recorded as an omission")
	}
	if !context.HasOmittedAssistantMessages() {
		t.Fatal("an incomplete assistant message must report an omission")
	}

	// Older unsequenced sources cannot establish order relative to queued replies.
	if source := context.RecordAssistantMessage(real, LocalInputSource(nil)); source != nil {
		t.Fatal("unsequenced assistant context is left to legacy transcript selection")
	}
}
