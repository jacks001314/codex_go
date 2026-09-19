package appserver

import (
	"encoding/json"
	"fmt"
	"testing"

	"codex_go/session"
)

func listThreadAttachmentsForTest(t *testing.T, router *RuntimeRouter, threadID string, requestID int64) []ThreadAttachment {
	t.Helper()
	limit := uint32(maxThreadAttachmentListPageSize)
	response := router.Handle(requestWithParams(t, IntID(requestID), MethodThreadAttachmentList, ThreadAttachmentListParams{
		ThreadID: threadID,
		Limit:    &limit,
	}))
	if response.Error != nil {
		t.Fatalf("thread/attachment/list(%s) error: %+v", threadID, response.Error)
	}
	return response.Result.(*ThreadAttachmentListResponse).Data
}

// TestThreadForkCopiesAttachmentsLikeRust mirrors Rust #45579: a non-ephemeral
// fork owns a copy of the source thread's current attachments with fresh ids and
// creation timestamps, its membership diverges from the source's, and an
// ephemeral fork inherits none.
func TestThreadForkCopiesAttachmentsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo"}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, threadID)

	for index, identity := range []string{"identity-1", "identity-2"} {
		add := router.Handle(requestWithParams(t, IntID(int64(2+index)), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
			ThreadID:       threadID,
			AttachmentType: "note",
			IdentityKey:    identity,
			Payload:        json.RawMessage(fmt.Sprintf(`{"text":%q}`, identity)),
		}))
		if add.Error != nil {
			t.Fatalf("thread/attachment/add(%s) error: %+v", identity, add.Error)
		}
	}
	sourceAttachments := listThreadAttachmentsForTest(t, router, threadID, 10)
	if len(sourceAttachments) != 2 {
		t.Fatalf("source attachments = %#v, want two", sourceAttachments)
	}

	fork := router.Handle(requestWithParams(t, IntID(11), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error: %+v", fork.Error)
	}
	forkID := fork.Result.(*ThreadForkResponse).Thread.ID
	forkAttachments := listThreadAttachmentsForTest(t, router, forkID, 12)
	if len(forkAttachments) != len(sourceAttachments) {
		t.Fatalf("fork attachments = %#v, want a copy of %#v", forkAttachments, sourceAttachments)
	}
	for index := range sourceAttachments {
		source, copied := sourceAttachments[index], forkAttachments[index]
		if copied.AttachmentType != source.AttachmentType || copied.IdentityKey != source.IdentityKey || string(copied.Payload) != string(source.Payload) {
			t.Fatalf("fork attachment %d = %#v, want the source identity and payload %#v", index, copied, source)
		}
		if copied.ID == source.ID {
			t.Fatalf("fork attachment kept the source id %q; copies must receive new ids", copied.ID)
		}
	}

	// Membership can change independently on either thread.
	remove := router.Handle(requestWithParams(t, IntID(13), MethodThreadAttachmentRemove, ThreadAttachmentRemoveParams{
		ThreadID:       forkID,
		AttachmentType: "note",
		IdentityKey:    "identity-1",
	}))
	if remove.Error != nil {
		t.Fatalf("thread/attachment/remove error: %+v", remove.Error)
	}
	if got := len(listThreadAttachmentsForTest(t, router, forkID, 14)); got != 1 {
		t.Fatalf("fork attachments after removal = %d, want 1", got)
	}
	if got := len(listThreadAttachmentsForTest(t, router, threadID, 15)); got != 2 {
		t.Fatalf("source attachments after the fork's removal = %d, want 2", got)
	}

	// An ephemeral fork inherits nothing.
	source := ThreadSourceUser
	ephemeral := router.Handle(requestWithParams(t, IntID(16), MethodThreadFork, ThreadForkParams{
		ThreadID:     threadID,
		ThreadSource: &source,
		Ephemeral:    true,
	}))
	if ephemeral.Error != nil {
		t.Fatalf("ephemeral thread/fork error: %+v", ephemeral.Error)
	}
	ephemeralID := ephemeral.Result.(*ThreadForkResponse).Thread.ID
	record, ok := router.threads.EphemeralRecord(session.ThreadID(ephemeralID), true)
	if !ok || record == nil {
		t.Fatal("ephemeral fork record was not retained")
	}
	if attachments := threadAttachmentsFromExtra(record.Metadata.Extra); len(attachments) != 0 {
		t.Fatalf("ephemeral fork inherited attachments: %#v", attachments)
	}
}
