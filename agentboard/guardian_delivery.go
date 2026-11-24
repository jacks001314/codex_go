package agentboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
