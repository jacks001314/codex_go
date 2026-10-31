package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"codex_go/session"
)

// Rust parity: codex-rs/thread-store/src/local/thread_attachments.rs at #44350.
// Attachment add/remove reserve the thread lifecycle writer that archive,
// delete and fork hold, so a mutation cannot land after its thread was removed
// and cannot update metadata while another lifecycle operation owns the thread.
func TestThreadAttachmentMutationsReserveLifecycleWriterLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7() error = %v", err)
	}
	threadID := session.ThreadID(id.String())
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRouter(store)
	params := &ThreadAttachmentAddParams{
		ThreadID:       string(threadID),
		AttachmentType: "note",
		IdentityKey:    "identity-1",
		Payload:        json.RawMessage(`{"text":"hello"}`),
	}

	held, err := store.AcquireWriter(threadID)
	if err != nil {
		t.Fatalf("AcquireWriter() error = %v", err)
	}
	if _, err := router.addThreadAttachment(params); err == nil {
		t.Fatal("attachment add succeeded while the lifecycle writer was held")
	}
	record, err := store.Read(threadID, true, false)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if attachments := threadAttachmentsFromExtra(record.Metadata.Extra); len(attachments) != 0 {
		t.Fatalf("blocked add persisted attachments: %#v", attachments)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("held.Close() error = %v", err)
	}

	added, err := router.addThreadAttachment(params)
	if err != nil {
		t.Fatalf("addThreadAttachment() after release error = %v", err)
	}
	if added.Outcome != ThreadAttachmentAddOutcomeCreated {
		t.Fatalf("add outcome = %q, want created", added.Outcome)
	}

	held, err = store.AcquireWriter(threadID)
	if err != nil {
		t.Fatalf("AcquireWriter() before remove error = %v", err)
	}
	if _, _, err := router.removeThreadAttachment(&ThreadAttachmentRemoveParams{
		ThreadID:       string(threadID),
		AttachmentType: "note",
		IdentityKey:    "identity-1",
	}); err == nil {
		t.Fatal("attachment remove succeeded while the lifecycle writer was held")
	}
	record, err = store.Read(threadID, true, false)
	if err != nil {
		t.Fatalf("Read() after blocked remove error = %v", err)
	}
	if attachments := threadAttachmentsFromExtra(record.Metadata.Extra); len(attachments) != 1 {
		t.Fatalf("blocked remove changed attachments: %#v", attachments)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("held.Close() error = %v", err)
	}
	if _, removed, err := router.removeThreadAttachment(&ThreadAttachmentRemoveParams{
		ThreadID:       string(threadID),
		AttachmentType: "note",
		IdentityKey:    "identity-1",
	}); err != nil || removed == nil {
		t.Fatalf("removeThreadAttachment() after release removed = %#v, error = %v", removed, err)
	}
}
