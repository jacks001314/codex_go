package retainedctx

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Rust parity: codex-rs/protocol/src/mcp.rs's MCP tools/call attribution
// (`McpAttribution`), reached through codex-history's `CodexHarnessMetadata`
// sidecar. The checkpoint is cumulative, model-invisible provenance data that
// survives history compaction; it is not a policy decision or attestation.

// McpAttributionStatus is the producer-reported completeness of a thread's MCP
// tools/call attribution (Rust `McpAttributionStatus`).
type McpAttributionStatus string

const (
	McpAttributionStatusNone             McpAttributionStatus = "none"
	McpAttributionStatusComplete         McpAttributionStatus = "complete"
	McpAttributionStatusAttributionError McpAttributionStatus = "attribution_error"
)

// McpAttributionErrorReason identifies the first observed attribution error
// (Rust `McpAttributionErrorReason`). Recorder reasons survive checkpoints;
// payload reasons describe a request-serialization fallback.
type McpAttributionErrorReason string

const (
	McpAttributionErrorHistoryMissingCheckpoint McpAttributionErrorReason = "history_missing_checkpoint"
	McpAttributionErrorCheckpointInvalid        McpAttributionErrorReason = "checkpoint_invalid"
	McpAttributionErrorCheckpointSourceConflict McpAttributionErrorReason = "checkpoint_source_conflict"
	McpAttributionErrorSourceInvalid            McpAttributionErrorReason = "source_invalid"
	McpAttributionErrorRecorderPoisoned         McpAttributionErrorReason = "recorder_poisoned"
	McpAttributionErrorRestoredErrorUnknown     McpAttributionErrorReason = "restored_error_unknown"
	McpAttributionErrorPayloadTooLarge          McpAttributionErrorReason = "payload_too_large"
	McpAttributionErrorSerializationFailed      McpAttributionErrorReason = "serialization_failed"
	// McpAttributionErrorUnknown is Rust's `#[serde(other)]` arm: a reason this
	// build does not recognize still decodes as `unknown`.
	McpAttributionErrorUnknown McpAttributionErrorReason = "unknown"
)

// McpAttributionSource is one recorded source and the first runtime turn in
// which Codex observed it (Rust `McpAttributionSource`). Server and tool names
// are retained only when no stable identifier is available, and are not
// guaranteed to be globally unique or independently authenticated.
type McpAttributionSource struct {
	ConnectorID *string `json:"connector_id,omitempty"`
	PluginID    *string `json:"plugin_id,omitempty"`
	ServerName  string  `json:"server_name"`
	ToolName    string  `json:"tool_name"`
	FirstTurnID string  `json:"first_turn_id"`
}

// McpAttribution is the cumulative, model-invisible attribution reported on
// Responses API requests (Rust `McpAttribution`).
type McpAttribution struct {
	Status McpAttributionStatus `json:"status"`
	// ErrorReason is the first observed attribution error, absent for
	// non-error states.
	ErrorReason *McpAttributionErrorReason `json:"error_reason,omitempty"`
	Sources     []McpAttributionSource     `json:"sources,omitempty"`
}

// mcpAttributionError is Rust's `McpAttribution { status: AttributionError,
// error_reason: None, sources: Vec::new() }` fallback used whenever a
// checkpoint cannot be decoded.
func mcpAttributionError() McpAttribution {
	return McpAttribution{Status: McpAttributionStatusAttributionError}
}

// UnmarshalJSON mirrors Rust's `deny_unknown_fields` McpAttribution: the
// `status` field is required and must name a known variant, every source is
// validated, and the `error_reason` field is lenient - an unrecognized string
// becomes `unknown` and a non-string value is dropped, because Rust's own
// `deserialize_mcp_attribution_error_reason` swallows the error.
func (a *McpAttribution) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "status", "error_reason", "sources":
		default:
			return fmt.Errorf("mcp attribution: unknown field %q", key)
		}
	}
	statusRaw, ok := fields["status"]
	if !ok {
		return errors.New("mcp attribution: missing field `status`")
	}
	var status string
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		return err
	}
	switch McpAttributionStatus(status) {
	case McpAttributionStatusNone, McpAttributionStatusComplete, McpAttributionStatusAttributionError:
		a.Status = McpAttributionStatus(status)
	default:
		return fmt.Errorf("mcp attribution: unknown status %q", status)
	}
	if raw, ok := fields["error_reason"]; ok {
		if reason, ok := decodeMcpAttributionErrorReason(raw); ok {
			a.ErrorReason = &reason
		}
	}
	if raw, ok := fields["sources"]; ok {
		var encoded []json.RawMessage
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return err
		}
		sources := make([]McpAttributionSource, 0, len(encoded))
		for _, entry := range encoded {
			source, err := decodeMcpAttributionSource(entry)
			if err != nil {
				return err
			}
			sources = append(sources, source)
		}
		a.Sources = sources
	}
	return nil
}

// decodeMcpAttributionErrorReason mirrors Rust's
// `deserialize_mcp_attribution_error_reason`: a recognized string decodes to
// itself, any other string decodes to `unknown` (`#[serde(other)]`), and a
// non-string is dropped rather than failing the checkpoint.
func decodeMcpAttributionErrorReason(raw json.RawMessage) (McpAttributionErrorReason, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	switch McpAttributionErrorReason(text) {
	case McpAttributionErrorHistoryMissingCheckpoint,
		McpAttributionErrorCheckpointInvalid,
		McpAttributionErrorCheckpointSourceConflict,
		McpAttributionErrorSourceInvalid,
		McpAttributionErrorRecorderPoisoned,
		McpAttributionErrorRestoredErrorUnknown,
		McpAttributionErrorPayloadTooLarge,
		McpAttributionErrorSerializationFailed:
		return McpAttributionErrorReason(text), true
	default:
		return McpAttributionErrorUnknown, true
	}
}

// decodeMcpAttributionSource mirrors Rust's `deny_unknown_fields` source: the
// three non-optional strings must be present, and unknown keys are rejected.
func decodeMcpAttributionSource(data []byte) (McpAttributionSource, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return McpAttributionSource{}, err
	}
	for key := range fields {
		switch key {
		case "connector_id", "plugin_id", "server_name", "tool_name", "first_turn_id":
		default:
			return McpAttributionSource{}, fmt.Errorf("mcp attribution source: unknown field %q", key)
		}
	}
	var source McpAttributionSource
	optional := map[string]**string{
		"connector_id": &source.ConnectorID,
		"plugin_id":    &source.PluginID,
	}
	for key, target := range optional {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return McpAttributionSource{}, err
		}
		encoded := value
		*target = &encoded
	}
	required := map[string]*string{
		"server_name":   &source.ServerName,
		"tool_name":     &source.ToolName,
		"first_turn_id": &source.FirstTurnID,
	}
	for key, target := range required {
		raw, ok := fields[key]
		if !ok {
			return McpAttributionSource{}, fmt.Errorf("mcp attribution source: missing field `%s`", key)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return McpAttributionSource{}, err
		}
		*target = value
	}
	return source, nil
}

// UnmarshalJSON implements Rust's `deny_unknown_fields` source directly so a
// nested decode (through `[]McpAttributionSource`) keeps the same rules.
func (s *McpAttributionSource) UnmarshalJSON(data []byte) error {
	decoded, err := decodeMcpAttributionSource(data)
	if err != nil {
		return err
	}
	*s = decoded
	return nil
}

// UnmarshalJSON mirrors Rust's `deserialize_mcp_attribution_checkpoint`: the
// `mcp_attribution` member is decoded leniently, so a checkpoint that this
// build cannot read becomes an attribution error instead of dropping the whole
// harness metadata (or the field). Every other member is decoded as the plain
// struct would.
func (m *HarnessMetadata) UnmarshalJSON(data []byte) error {
	wire := struct {
		*plainHarnessMetadata
		RawMcpAttribution json.RawMessage `json:"mcp_attribution"`
	}{plainHarnessMetadata: (*plainHarnessMetadata)(m)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.RawMcpAttribution != nil {
		attribution := mcpAttributionError()
		if err := json.Unmarshal(wire.RawMcpAttribution, &attribution); err != nil {
			attribution = mcpAttributionError()
		}
		m.McpAttribution = &attribution
	}
	return nil
}

// plainHarnessMetadata strips HarnessMetadata's methods for a nested decode.
type plainHarnessMetadata HarnessMetadata
