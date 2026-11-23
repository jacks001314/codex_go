package appserver

import (
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
	"codex_go/state"
	"codex_go/utils"
)

// Rust #62829: the configured retained-instruction entry limit admits the whole
// user message, while fixed mode keeps the bounded projection.
func TestUserInstructionsKeepElasticOriginalsLikeRust(t *testing.T) {
	longText := strings.Repeat("i", utils.ApproxBytesForTokens(2000))
	item := sessionItemForRetainedTest("user-long", longText, []string{"user.text"}, []string{"input_text"})
	limit := utils.ApproxBytesForTokens(4000)
	router, record := newRetentionThreadFixture(t, []session.Item{item}, &retainedctx.GuardianRetentionPolicy{
		AssistantRecordBytes: &limit,
		UserRecordBytes:      &limit,
	})
	context := router.retainedContextForThread(string(record.ID))
	if context == nil || !context.RetainsOriginalUserMessages() {
		t.Fatalf("retained context = %#v, want the Elastic user policy", context)
	}
	var found bool
	for _, entry := range context.OrderedEntries() {
		message := entry.Entry.UserMessage
		if message == nil {
			continue
		}
		found = true
		if message.Text != longText {
			t.Fatalf("retained instruction length = %d, want the whole %d-byte message", len(message.Text), len(longText))
		}
		if !message.Complete {
			t.Fatal("whole instruction recorded incomplete")
		}
	}
	if !found {
		t.Fatalf("retained instruction missing: %#v", context.OrderedEntries())
	}

	fixedRouter, fixedRecord := newRetentionThreadFixture(t, []session.Item{item}, nil)
	fixed := fixedRouter.retainedContextForThread(string(fixedRecord.ID))
	for _, entry := range fixed.OrderedEntries() {
		if message := entry.Entry.UserMessage; message != nil && len(message.Text) >= len(longText) {
			t.Fatalf("fixed-mode retained instruction length = %d, want a bounded projection", len(message.Text))
		}
	}
}

// Rust #62829: the sender snapshot uses the receiver's Elastic sender budget
// when configured, and the fixed three-exchange window otherwise.
func TestSenderEvidenceUsesElasticBudgetLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `[features.guardianv2]
sync_elastic_budgeting = true
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 109900
max_section_entry_tokens = 1
[features.guardianv2.sync_elastic_sections.sender_user_messages]
min_total_section_tokens = 4000
max_section_entry_tokens = 4000
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	receiver := session.ThreadID("thread-receiver-elastic")
	if err := store.Create(&session.Record{
		ID: receiver, SessionID: string(receiver), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5", Extra: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })

	fragment := state.GuardianSenderMessages{Source: "source-thread", Delivery: "delivery-1"}
	for index := 0; index < 6; index++ {
		user := "instruction-" + strings.Repeat("u", index+1)
		assistant := state.RootMessage{Kind: state.RootMessageAssistant, Text: "reply-" + strings.Repeat("a", index+1)}
		fragment.Messages = append(fragment.Messages, state.GuardianSenderExchange{User: &user, Assistant: &assistant})
	}
	elastic := router.senderEvidenceText(string(receiver), fragment)
	if !strings.Contains(elastic, "user: instruction-u\n") {
		t.Fatalf("elastic sender snapshot dropped the oldest exchange: %q", elastic)
	}
	if got := strings.Count(elastic, "user: "); got < 4 {
		t.Fatalf("elastic sender snapshot carried %d exchanges, want more than three", got)
	}

	fixedHome := t.TempDir()
	fixedStore := session.NewStore(t.TempDir())
	if err := fixedStore.Create(&session.Record{
		ID: receiver, SessionID: string(receiver), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5", Extra: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
	fixedRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(fixedStore), Config: config.NewConfigService(fixedHome)})
	t.Cleanup(func() { _ = fixedRouter.Close() })
	fixed := fixedRouter.senderEvidenceText(string(receiver), fragment)
	if strings.Contains(fixed, "user: instruction-u\n") {
		t.Fatalf("fixed sender snapshot kept the oldest exchange: %q", fixed)
	}
	if got := strings.Count(fixed, "user: "); got != 3 {
		t.Fatalf("fixed sender snapshot carried %d exchanges, want three", got)
	}
}
