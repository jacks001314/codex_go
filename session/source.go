package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GuardianReviewSource is the canonical source recorded for Guardian approval
// review threads. Rust serializes that source as
// SessionSource::SubAgent(SubAgentSource::Other("guardian")), whose Display
// form is "subagent_guardian".
const GuardianReviewSource = "subagent_guardian"

// IsGuardianReviewSource mirrors codex_state::is_guardian_review_source
// (codex-rs/state/src/extract.rs): only the legacy subagent "other" source
// named "guardian" identifies a Guardian review thread.
func IsGuardianReviewSource(source string) bool {
	return normalizedSourceKind(source) == "subagentguardian"
}

// ParseSessionSourceJSON decodes the Rust SessionSource wire form. Rollout
// session_meta payloads and app-server thread snapshots serialize the enum
// externally tagged, so unit variants are plain strings ("cli") and payload
// variants are single-key objects ({"subagent":{"other":"guardian"}}). The
// returned value is the Display form Go stores in session.Metadata.Source.
func ParseSessionSourceJSON(raw []byte) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var literal string
	if err := json.Unmarshal(trimmed, &literal); err == nil {
		return literal, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return "", fmt.Errorf("session source: expected string or object: %w", err)
	}
	if len(object) != 1 {
		return "", fmt.Errorf("session source: expected one variant, got %d", len(object))
	}
	for key, value := range object {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "subagent":
			return parseSubAgentSourceJSON(value)
		case "internal":
			var name string
			if err := json.Unmarshal(value, &name); err != nil {
				return "", fmt.Errorf("session source internal: %w", err)
			}
			return "internal_" + strings.TrimSpace(name), nil
		case "custom":
			var name string
			if err := json.Unmarshal(value, &name); err != nil {
				return "", fmt.Errorf("session source custom: %w", err)
			}
			return strings.TrimSpace(name), nil
		default:
			variant := strings.ToLower(strings.TrimSpace(key))
			var name string
			if err := json.Unmarshal(value, &name); err == nil && strings.TrimSpace(name) != "" {
				return variant + "_" + strings.TrimSpace(name), nil
			}
			return variant, nil
		}
	}
	return "", errors.New("session source: empty variant object")
}

func parseSubAgentSourceJSON(raw json.RawMessage) (string, error) {
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		return canonicalSubAgentKind(literal), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("session source subagent: expected string or object: %w", err)
	}
	if len(object) != 1 {
		return "", fmt.Errorf("session source subagent: expected one variant, got %d", len(object))
	}
	for key, value := range object {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "other":
			var name string
			if err := json.Unmarshal(value, &name); err != nil {
				return "", fmt.Errorf("session source subagent other: %w", err)
			}
			return canonicalSubAgentKind(name), nil
		case "thread_spawn", "threadspawn":
			return "subagent_thread_spawn", nil
		default:
			return canonicalSubAgentKind(strings.ToLower(strings.TrimSpace(key))), nil
		}
	}
	return "", errors.New("session source subagent: empty variant object")
}

func canonicalSubAgentKind(kind string) string {
	return "subagent_" + strings.TrimSpace(kind)
}

// SubAgentSourceWire returns the Serialize form of a canonical subagent
// source's payload (the value carried under the "subagent"/"subAgent" key).
// It reports false for sources whose payload cannot be reconstructed without
// losing detail (thread spawn carries parent/depth/agent metadata), leaving
// those to keep their plain display string.
func SubAgentSourceWire(source string) (any, bool) {
	switch normalizedSourceKind(source) {
	case "subagentreview":
		return "review", true
	case "subagentcompact":
		return "compact", true
	case "subagentmemoryconsolidation":
		return "memory_consolidation", true
	case "subagentguardian":
		return map[string]any{"other": "guardian"}, true
	default:
		return nil, false
	}
}

// InternalSessionSourceWire returns the Serialize form of a canonical internal
// session source's payload (the value carried under the "internal" key). Rust
// serializes SessionSource::Internal(InternalSessionSource::Guardian) as
// {"internal":"guardian"}; Go stores the same source in Display form as
// "internal_guardian".
func InternalSessionSourceWire(source string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(source))
	normalized = strings.NewReplacer("-", "_", ":", "_", "/", "_").Replace(normalized)
	const prefix = "internal_"
	if !strings.HasPrefix(normalized, prefix) {
		return "", false
	}
	switch name := strings.TrimPrefix(normalized, prefix); name {
	case "memory_consolidation", "guardian":
		return name, true
	default:
		return "", false
	}
}
