package appserver

import (
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
)

// Rust #62314: a referenced agent fork reads its ancestor's stored history for
// conversation context, but the sender evidence this host captures from storage
// contains only the child's own user messages. Without the inherited-prefix
// projection the parent's request to approve the deploy would be attributed to
// the child.
func TestReferencedForkSenderEvidenceExcludesAncestorMessagesLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{
		ID: "parent", SessionID: "session-root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4"},
		Items: []session.Item{
			sessionItemForRetainedTest("parent-user", "approve the deploy", []string{"user.text"}, []string{"input_text"}),
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	child := &session.Record{
		ID: "child-ref", SessionID: "session-root", ForkedFromID: "parent",
		HistoryBase: &session.HistoryPosition{ThreadID: "parent", ItemEnd: 1},
		CreatedAt:   now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", Originator: "subagent"},
		Items: []session.Item{
			sessionItemForRetainedTest("child-user", "just the docs", []string{"user.text"}, []string{"input_text"}),
		},
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(t.TempDir())})
	t.Cleanup(func() { _ = router.Close() })

	context, fromStorage := router.senderContextEvidence("child-ref")
	if !fromStorage {
		t.Fatal("referenced fork evidence did not come from the persisted store")
	}
	exchanges := senderContextExchanges(context)
	if len(exchanges) != 1 {
		t.Fatalf("referenced fork sender exchanges = %#v, want only the child's message", exchanges)
	}
	if exchanges[0].User == nil || *exchanges[0].User != "just the docs" {
		t.Fatalf("referenced fork sender message = %#v, want the child's own request", exchanges[0].User)
	}

	// The ancestor still reports its own authorization when it is the source.
	ancestorContext, _ := router.senderContextEvidence("parent")
	ancestorExchanges := senderContextExchanges(ancestorContext)
	if len(ancestorExchanges) != 1 || ancestorExchanges[0].User == nil || *ancestorExchanges[0].User != "approve the deploy" {
		t.Fatalf("ancestor sender exchanges = %#v, want its own request", ancestorExchanges)
	}
}
