package state

import (
	"encoding/json"
	"testing"
)

// TestThreadArtifactModelsMatchRustShape pins the exported model fields from
// Rust #40509 without adding storage operations that the upstream commit does
// not define.
func TestThreadArtifactModelsMatchRustShape(t *testing.T) {
	payload := json.RawMessage(`{"title":"release notes","size":12}`)
	artifact := ThreadArtifact{
		ID:           "0198f0f0-0000-7000-8000-000000000001",
		ThreadID:     "thread-1",
		ArtifactType: "document",
		IdentityKey:  "release-notes",
		Payload:      payload,
		CreatedAt:    1_700_000_000,
	}
	created := ThreadArtifactAttachmentOutcome{Artifact: artifact, Created: true}
	if !created.Created || created.Artifact.IdentityKey != "release-notes" {
		t.Fatalf("created outcome = %#v", created)
	}
	if string(created.Artifact.Payload) != string(payload) {
		t.Fatalf("payload = %s, want %s", created.Artifact.Payload, payload)
	}
	existing := ThreadArtifactAttachmentOutcome{Artifact: artifact}
	if existing.Created {
		t.Fatalf("existing outcome = %#v", existing)
	}
	removed := ThreadArtifactRemovalOutcome{Artifact: artifact, Removed: true}
	if !removed.Removed || removed.Artifact.ThreadID != "thread-1" {
		t.Fatalf("removed outcome = %#v", removed)
	}
	notFound := ThreadArtifactRemovalOutcome{}
	if notFound.Removed {
		t.Fatalf("not-found outcome = %#v", notFound)
	}
	cursor := "cursor-2"
	page := ThreadArtifactPage{Artifacts: []ThreadArtifact{artifact}, NextCursor: &cursor}
	if len(page.Artifacts) != 1 || page.NextCursor == nil || *page.NextCursor != cursor {
		t.Fatalf("page = %#v", page)
	}
	last := ThreadArtifactPage{Artifacts: []ThreadArtifact{}}
	if last.NextCursor != nil {
		t.Fatalf("exhausted page cursor = %#v", last.NextCursor)
	}
}
