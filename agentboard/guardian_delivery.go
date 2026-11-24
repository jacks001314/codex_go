package agentboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	// GuardianEvidenceContextName is the function-call-output name that carries
	// routed originals into a reviewer conversation (Rust
	// guardian-v3 sampling::context_input("guardian_original_evidence")).
	GuardianEvidenceContextName = "guardian_original_evidence"
	// GuardianEvidenceNamespace is the namespace of that evidence item.
	GuardianEvidenceNamespace = "guardian"
	// guardianEvidenceRequestIDMaxBytes bounds the generated board idempotency
	// key (Rust EvidenceId::request_id uses 512 bytes).
	guardianEvidenceRequestIDMaxBytes = 512
)

// GuardianEvidenceID identifies one routed original by its source ordinal.
// Several channels may carry the same ordinal; delivery admits it once.
type GuardianEvidenceID struct {
	Ordinal uint64 `json:"ordinal"`
}

// GuardianEvidenceRequestID renders the board idempotency key the housekeeper
// and delivery share (Rust EvidenceId::request_id): a JSON [ordinal, channel].
func GuardianEvidenceRequestID(ordinal uint64, channel string) (string, error) {
	encoded, err := json.Marshal([]any{ordinal, channel})
	if err != nil {
		return "", err
	}
	if len(encoded) > guardianEvidenceRequestIDMaxBytes {
		return "", fmt.Errorf("evidence request ID exceeds %d bytes", guardianEvidenceRequestIDMaxBytes)
	}
	return string(encoded), nil
}

// ParseGuardianEvidenceRequestID reads the source ordinal out of a board
// request ID and rejects a key minted for a different channel (Rust
// EvidenceId::from_request_id).
func ParseGuardianEvidenceRequestID(request string, channel string) (GuardianEvidenceID, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal([]byte(request), &parts); err != nil {
		return GuardianEvidenceID{}, fmt.Errorf("decode evidence request ID: %w", err)
	}
	if len(parts) != 2 {
		return GuardianEvidenceID{}, errors.New("evidence request ID must be [ordinal, channel]")
	}
	var ordinal uint64
	if err := json.Unmarshal(parts[0], &ordinal); err != nil {
		return GuardianEvidenceID{}, fmt.Errorf("decode evidence request ID ordinal: %w", err)
	}
	var destination string
	if err := json.Unmarshal(parts[1], &destination); err != nil {
		return GuardianEvidenceID{}, fmt.Errorf("decode evidence request ID channel: %w", err)
	}
	if destination != channel {
		return GuardianEvidenceID{}, errors.New("evidence request ID channel mismatch")
	}
	return GuardianEvidenceID{Ordinal: ordinal}, nil
}

// GuardianEvidenceItem is the model-visible function-call output that admits
// one batch of routed originals.
type GuardianEvidenceItem struct {
	Name      string
	Namespace string
	Text      string
}

// guardianRoutedOriginal is one stored evidence post: the source ordinal plus
// the exact original model-visible item (Rust RoutedOriginal, deny_unknown_fields).
type guardianRoutedOriginal struct {
	ID       GuardianEvidenceID `json:"id"`
	Original json.RawMessage    `json:"original"`
}

// guardianDeliveryInput is the payload of the admitted context item.
type guardianDeliveryInput struct {
	Sources []guardianRoutedOriginal `json:"sources"`
}

// EncodeGuardianRoutedOriginal renders the exact stored body of one routed
// original (Rust RoutedOriginal serialization: {"id":{"ordinal":N},"original":
// <response item>}). The original must be one JSON object so a reviewer can
// decode it without guessing at prose.
func EncodeGuardianRoutedOriginal(id GuardianEvidenceID, original json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(original)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", errors.New("routed original requires a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return "", fmt.Errorf("decode routed original item: %w", err)
	}
	if decoder.More() {
		return "", errors.New("decode routed original item: trailing JSON value")
	}
	if len(object) == 0 {
		return "", errors.New("routed original item must not be empty")
	}
	encoded, err := json.Marshal(guardianRoutedOriginal{ID: id, Original: trimmed})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// GuardianCapturedEvidence is one original captured with the position that
// admitted it (Rust EvidenceSnapshot sources).
type GuardianCapturedEvidence struct {
	ID       GuardianEvidenceID
	Original json.RawMessage
	// End is the durable position this record reached, so a cursor can
	// acknowledge a consumed prefix without skipping later appends
	// (Rust OriginalEvidence.end).
	End GuardianHistoryPosition
}

// GuardianEvidenceSnapshot is an immutable capture of the originals a review
// will admit (Rust EvidenceSnapshot). Start is the cursor position the read
// began at and End the exclusive durable boundary later retrieval binds to;
// appends that land after the capture cannot enter it.
type GuardianEvidenceSnapshot struct {
	Start   GuardianHistoryPosition
	End     GuardianHistoryPosition
	Sources []GuardianCapturedEvidence
}

// GuardianEvidenceCursor tracks how much of a root's original history a board
// has consumed (Rust EvidenceCursor). It never leaves the process: hosts create
// it at board activation and acknowledge only after the admitted records were
// written.
type GuardianEvidenceCursor struct {
	mu       sync.Mutex
	threadID string
	position GuardianHistoryPosition
}

// NewGuardianEvidenceCursor starts a cursor at the beginning of a root's
// original history (Rust EvidenceCursor::start).
func NewGuardianEvidenceCursor(threadID string) *GuardianEvidenceCursor {
	threadID = strings.TrimSpace(threadID)
	return &GuardianEvidenceCursor{
		threadID: threadID,
		position: GuardianHistoryPosition{ThreadID: threadID},
	}
}

// ThreadID is the root whose history this cursor tracks.
func (c *GuardianEvidenceCursor) ThreadID() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threadID
}

// Position returns the last acknowledged durable position.
func (c *GuardianEvidenceCursor) Position() GuardianHistoryPosition {
	if c == nil {
		return GuardianHistoryPosition{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.position
}

// Pending returns the records of snapshot the cursor has not acknowledged,
// in source order. The capture must contain the cursor: same root, and both
// its bounds must straddle the acknowledged position.
func (c *GuardianEvidenceCursor) Pending(snapshot *GuardianEvidenceSnapshot) ([]GuardianCapturedEvidence, error) {
	if c == nil {
		return nil, errors.New("Guardian evidence cursor is nil")
	}
	if snapshot == nil {
		return nil, errors.New("Guardian evidence snapshot is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	position := c.position
	if err := guardianSnapshotContainsPosition(snapshot, position); err != nil {
		return nil, err
	}
	pending := make([]GuardianCapturedEvidence, 0, len(snapshot.Sources))
	for _, record := range snapshot.Sources {
		if record.End.EndByteOffset <= position.EndByteOffset {
			continue
		}
		pending = append(pending, record)
	}
	return pending, nil
}

// Acknowledge consumes a successfully admitted prefix of the pending records.
// Acknowledging every pending record also skips trailing records that evidence
// selection excluded, so the cursor lands on the capture's exclusive end.
func (c *GuardianEvidenceCursor) Acknowledge(snapshot *GuardianEvidenceSnapshot, count int) error {
	if c == nil {
		return errors.New("Guardian evidence cursor is nil")
	}
	if count < 0 {
		return errors.New("acknowledgement count must not be negative")
	}
	pending, err := c.Pending(snapshot)
	if err != nil {
		return err
	}
	if count > len(pending) {
		return errors.New("acknowledgement exceeds pending evidence")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case count == len(pending):
		c.position = snapshot.End
	case count > 0:
		c.position = pending[count-1].End
	}
	return nil
}

// guardianSnapshotContainsPosition reports whether the capture's bounds straddle
// position (Rust EvidenceCursor::pending's containment check).
func guardianSnapshotContainsPosition(snapshot *GuardianEvidenceSnapshot, position GuardianHistoryPosition) error {
	threadID := strings.TrimSpace(position.ThreadID)
	if strings.TrimSpace(snapshot.Start.ThreadID) != threadID ||
		strings.TrimSpace(snapshot.End.ThreadID) != threadID {
		return errors.New("evidence capture does not contain the current cursor")
	}
	if snapshot.Start.EndByteOffset > position.EndByteOffset ||
		position.EndByteOffset > snapshot.End.EndByteOffset ||
		snapshot.Start.EndOrdinalExclusive > position.EndOrdinalExclusive ||
		position.EndOrdinalExclusive > snapshot.End.EndOrdinalExclusive {
		return errors.New("evidence capture does not contain the current cursor")
	}
	return nil
}

// Cutoff returns the exclusive durable source boundary of the capture.
func (s GuardianEvidenceSnapshot) Cutoff() GuardianHistoryPosition {
	return s.End
}

// EncodedSources renders every captured original in source order.
func (s GuardianEvidenceSnapshot) EncodedSources() ([]string, error) {
	encoded := make([]string, 0, len(s.Sources))
	for _, source := range s.Sources {
		text, err := EncodeGuardianRoutedOriginal(source.ID, source.Original)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, text)
	}
	return encoded, nil
}

// SourceAdvanced reports whether the capture read past the cursor position
// (Rust EvidenceSnapshot::source_advanced compares start with end).
func (s GuardianEvidenceSnapshot) SourceAdvanced() bool {
	return s.Start != s.End
}

func parseGuardianRoutedOriginal(text string) (guardianRoutedOriginal, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var source guardianRoutedOriginal
	if err := decoder.Decode(&source); err != nil {
		return guardianRoutedOriginal{}, fmt.Errorf("decode routed original: %w", err)
	}
	if decoder.More() {
		return guardianRoutedOriginal{}, errors.New("decode routed original: trailing JSON value")
	}
	if source.Original == nil {
		return guardianRoutedOriginal{}, errors.New("decode routed original: missing original item")
	}
	return source, nil
}
