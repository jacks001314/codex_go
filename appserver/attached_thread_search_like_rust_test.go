package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestAttachedThreadSearchOptInLikeRust mirrors Rust #60574: ordinary searches
// keep hiding persisted attached threads, while includeAttachedThreads opts
// them into title and content searches and exposes their owning parent.
func TestAttachedThreadSearchOptInLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	router := NewRouter(store)
	now := time.Now().UTC()
	parent := &session.Record{
		ID: "0198f004-0000-7000-8000-000000000001", SessionID: "0198f004-0000-7000-8000-000000000001",
		CreatedAt: now, UpdatedAt: now,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", Source: string(SessionSourceVsCode)},
		Items:    []session.Item{{Role: "user", Text: "ordinary parent"}},
	}
	attached := &session.Record{
		ID: "0198f004-0000-7000-8000-000000000002", SessionID: "0198f004-0000-7000-8000-000000000002",
		ForkedFromID: parent.ID, AttachedThreadParentID: parent.ID,
		Title: "matching title", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", Source: string(SessionSourceVsCode)},
		Items:    []session.Item{{Role: "user", Text: "searchable attached content"}},
	}
	for _, record := range []*session.Record{parent, attached} {
		if err := store.Create(record); err != nil {
			t.Fatalf("store.Create(%s) error = %v", record.ID, err)
		}
	}

	withoutAttached := router.Handle(requestWithParams(t, IntID(1), MethodThreadSearch, ThreadSearchParams{SearchTerm: "matching title"}))
	if withoutAttached.Error != nil {
		t.Fatalf("thread/search default error = %+v", withoutAttached.Error)
	}
	if data := withoutAttached.Result.(*ThreadSearchResponse).Data; len(data) != 0 {
		t.Fatalf("default search exposed attached thread: %+v", data)
	}

	withAttached := router.Handle(requestWithParams(t, IntID(2), MethodThreadSearch, ThreadSearchParams{
		SearchTerm: "matching title", IncludeAttachedThreads: true,
	}))
	if withAttached.Error != nil {
		t.Fatalf("thread/search include attached error = %+v", withAttached.Error)
	}
	results := withAttached.Result.(*ThreadSearchResponse).Data
	if len(results) != 1 || results[0].Thread.ID != string(attached.ID) {
		t.Fatalf("inclusive title search = %+v", results)
	}
	if results[0].Thread.AttachedToThreadID == nil || *results[0].Thread.AttachedToThreadID != string(parent.ID) {
		t.Fatalf("inclusive search attachedToThreadId = %#v", results[0].Thread.AttachedToThreadID)
	}

	content := router.Handle(requestWithParams(t, IntID(3), MethodThreadSearch, ThreadSearchParams{
		SearchTerm: "searchable attached content", IncludeAttachedThreads: true,
	}))
	if content.Error != nil {
		t.Fatalf("thread/search attached content error = %+v", content.Error)
	}
	if data := content.Result.(*ThreadSearchResponse).Data; len(data) != 1 || data[0].Thread.ID != string(attached.ID) {
		t.Fatalf("inclusive content search = %+v", data)
	}

	listed := router.Handle(requestWithParams(t, IntID(4), MethodThreadList, ThreadListParams{
		SearchTerm: stringPointer("matching title"), IncludeAttachedThreads: true,
	}))
	if listed.Error != nil {
		t.Fatalf("thread/list include attached error = %+v", listed.Error)
	}
	if data := listed.Result.(*ThreadListResponse).Data; len(data) != 1 || data[0].ID != string(attached.ID) {
		t.Fatalf("inclusive thread/list = %+v", data)
	}

	rejected := router.Handle(requestWithParams(t, IntID(5), MethodThreadList, ThreadListParams{IncludeAttachedThreads: true}))
	if rejected.Error == nil || !strings.Contains(rejected.Error.Message, "non-empty searchTerm") {
		t.Fatalf("thread/list include attached without search error = %+v", rejected.Error)
	}
}
