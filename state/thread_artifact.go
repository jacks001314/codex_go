package state

import "encoding/json"

// ThreadArtifact is a bounded artifact durably associated with one thread
// (Rust #40509). Identity is unique per (thread, artifact type, identity key),
// while ID is the server-assigned artifact identity.
type ThreadArtifact struct {
	ID           string
	ThreadID     string
	ArtifactType string
	IdentityKey  string
	Payload      json.RawMessage
	CreatedAt    int64
}

// ThreadArtifactAttachmentOutcome reports the result of attaching one uniquely
// identified artifact. Created is false when the identity already existed; the
// existing payload and creation time are unchanged in that case.
type ThreadArtifactAttachmentOutcome struct {
	Artifact ThreadArtifact
	Created  bool
}

// ThreadArtifactRemovalOutcome reports whether an attached artifact was
// removed. When Removed is false the zero artifact is returned.
type ThreadArtifactRemovalOutcome struct {
	Artifact ThreadArtifact
	Removed  bool
}

// ThreadArtifactPage is one deterministically ordered page of artifacts across
// selected threads. NextCursor is nil when the selection is exhausted.
type ThreadArtifactPage struct {
	Artifacts  []ThreadArtifact
	NextCursor *string
}
