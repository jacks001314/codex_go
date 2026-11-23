package retainedctx

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #62828: the checkpoint carries the owning thread's recorder policy, and
// a recorder with an Elastic entry limit admits whole assistant messages within
// that byte limit instead of the fixed 900-token projection.
func TestMessageRetentionAdmitsWholeAssistantMessagesLikeRust(t *testing.T) {
	limit := 20_000
	context := (&RetainedContext{}).WithRetention(&MessageRetention{
		ThreadID:                "thread-1",
		GuardianRetentionPolicy: GuardianRetentionPolicy{AssistantRecordBytes: &limit},
	})
	if !context.RetainsOriginalAssistantMessages() {
		t.Fatal("retention did not enable whole assistant messages")
	}
	if got, ok := context.ElasticAssistantRecordBytes(); !ok || got != limit {
		t.Fatalf("ElasticAssistantRecordBytes() = %d, %v, want %d", got, ok, limit)
	}
	long := strings.Repeat("a", 12_000)
	context.RecordAssistantMessage(RetainedUserMessage{Text: long, Complete: true, TurnID: "turn-1"}, LocalInputSource(uintPtr(0)))
	entries := context.OrderedEntries()
	if len(entries) != 1 || entries[0].Entry.AssistantMessage == nil {
		t.Fatalf("retained entries = %#v", entries)
	}
	if got := entries[0].Entry.AssistantMessage.Text; got != long {
		t.Fatalf("retained assistant text length = %d, want the whole %d-byte message", len(got), len(long))
	}
	if !entries[0].Entry.AssistantMessage.Complete {
		t.Fatal("whole assistant message recorded incomplete")
	}

	// Beyond the configured record limit the record keeps only an incomplete marker.
	overLimit := strings.Repeat("b", limit+1)
	context.RecordAssistantMessage(RetainedUserMessage{Text: overLimit, Complete: true, TurnID: "turn-2"}, LocalInputSource(uintPtr(1)))
	entries = context.OrderedEntries()
	last := entries[len(entries)-1].Entry.AssistantMessage
	if last == nil || last.Text != "" || last.Complete {
		t.Fatalf("oversized assistant record = %#v, want an incomplete marker", last)
	}
}

// A recorder with an Elastic entry limit keeps more than the fixed eight
// assistant records; the byte budget still bounds the family.
func TestMessageRetentionKeepsMoreThanFixedAssistantRecordsLikeRust(t *testing.T) {
	limit := 4_000
	context := (&RetainedContext{}).WithRetention(&MessageRetention{
		ThreadID:                "thread-1",
		GuardianRetentionPolicy: GuardianRetentionPolicy{AssistantRecordBytes: &limit},
	})
	for i := 0; i < MaxFamilyRecords+3; i++ {
		order := uint64(i)
		context.RecordAssistantMessage(RetainedUserMessage{
			Text:     "message " + strings.Repeat("x", 10),
			Complete: true,
			TurnID:   "turn",
		}, LocalInputSource(&order))
	}
	if got := len(context.assistantMessages); got != MaxFamilyRecords+3 {
		t.Fatalf("assistant family = %d records, want %d", got, MaxFamilyRecords+3)
	}
}

// Rust #62828 projects the message families for Fixed-budget consumers: eight
// records and the fixed per-record limit, leaving Elastic originals that already
// fit untouched.
func TestFixedMessageViewProjectsElasticOriginalsLikeRust(t *testing.T) {
	plain := &RetainedContext{}
	plain.RecordAssistantMessage(RetainedUserMessage{Text: "short", Complete: true, TurnID: "turn"}, LocalInputSource(uintPtr(0)))
	if plain.FixedMessageView() != plain {
		t.Fatal("a context already within the fixed window was cloned")
	}

	limit := 40_000
	elastic := (&RetainedContext{}).WithRetention(&MessageRetention{
		ThreadID:                "thread-1",
		GuardianRetentionPolicy: GuardianRetentionPolicy{AssistantRecordBytes: &limit},
	})
	long := strings.Repeat("c", MaxRecordBytes+2_000)
	for i := 0; i < MaxFamilyRecords+2; i++ {
		order := uint64(i)
		elastic.RecordAssistantMessage(RetainedUserMessage{Text: long, Complete: true, TurnID: "turn"}, LocalInputSource(&order))
	}
	view := elastic.FixedMessageView()
	if view == elastic {
		t.Fatal("oversized elastic context was not projected")
	}
	if view.RetainsOriginalAssistantMessages() {
		t.Fatal("projected view kept the Elastic policy")
	}
	if got := len(view.assistantMessages); got > MaxFamilyRecords {
		t.Fatalf("projected assistant family = %d, want at most %d", got, MaxFamilyRecords)
	}
	for _, entry := range view.assistantMessages {
		if len(entry.Value.Text) > MaxRecordBytes || entry.Value.Complete {
			t.Fatalf("projected record text=%d complete=%t, want an incomplete bounded record", len(entry.Value.Text), entry.Value.Complete)
		}
	}
}

// The policy round-trips through the checkpoint and a restore keeps the live
// recorder's own limits.
func TestMessageRetentionRoundTripsAndSurvivesRestoreLikeRust(t *testing.T) {
	limit := 12_345
	original := (&RetainedContext{}).WithRetention(&MessageRetention{
		ThreadID:                "thread-1",
		GuardianRetentionPolicy: GuardianRetentionPolicy{AssistantRecordBytes: &limit},
	})
	original.RecordUserMessage(RetainedUserMessage{Text: "instruction", Complete: true, TurnID: "turn-1"}, LocalInputSource(uintPtr(0)))
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded RetainedContext
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	retention := decoded.MessageRetentionPolicy()
	if retention == nil || retention.ThreadID != "thread-1" || retention.AssistantRecordBytes == nil || *retention.AssistantRecordBytes != limit {
		t.Fatalf("decoded retention = %#v", retention)
	}

	liveLimit := 900
	live := (&RetainedContext{}).WithRetention(&MessageRetention{
		ThreadID:                "thread-1",
		GuardianRetentionPolicy: GuardianRetentionPolicy{AssistantRecordBytes: &liveLimit},
	})
	live.Restore(&decoded, nil)
	if got := live.MessageRetentionPolicy(); got == nil || got.AssistantRecordBytes == nil || *got.AssistantRecordBytes != liveLimit {
		t.Fatalf("restore replaced the live policy with %#v", got)
	}
	if len(live.OrderedEntries()) != 1 {
		t.Fatalf("restore lost the checkpoint's retained evidence: %#v", live.OrderedEntries())
	}
}
