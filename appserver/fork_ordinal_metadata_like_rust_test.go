package appserver

import (
	"testing"
	"time"

	"codex_go/session"
)

func TestResponsesMetadataForkOrdinalTracksLogicalParentLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID: "fork", SessionID: "fork", ForkedFromID: "parent",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		HistoryBase: &session.HistoryPosition{ThreadID: "parent", EndOrdinalExclusive: 7},
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if ordinal := forkedFromOrdinalExclusiveForRecord(record); ordinal == nil || *ordinal != 7 {
		t.Fatalf("fork ordinal = %#v, want 7", ordinal)
	}

	ambiguous := &session.Record{
		ID: "revert", SessionID: "revert", ForkedFromID: "parent",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		HistoryBase: &session.HistoryPosition{ThreadID: "different-ancestor", EndOrdinalExclusive: 9},
	}
	if ordinal := forkedFromOrdinalExclusiveForRecord(ambiguous); ordinal != nil {
		t.Fatalf("ambiguous fork ordinal = %#v, want nil", ordinal)
	}

	subagent := &session.Record{
		ID: "subagent", SessionID: "subagent", ForkedFromID: "parent", ParentThreadID: "parent",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Source: "subagent_thread_spawn_parent"},
	}
	if err := store.Create(subagent); err != nil {
		t.Fatal(err)
	}
	if lineage := router.responsesMetadataLineage("subagent"); lineage.ParentThreadID != "parent" || lineage.ForkedFromThreadID != "" || lineage.ForkedFromOrdinalExclusive != nil {
		t.Fatalf("context-inheriting subagent lineage = %#v, want parent only", lineage)
	}
}
