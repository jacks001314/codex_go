package retainedctx

import (
	"encoding/json"
)

// Rust parity: codex-rs/history/src/legacy_mcp_attribution.rs (Rust #62589).
// Older rollouts carry the removed cumulative `mcp_attribution` checkpoint;
// only whether Search MCP may have been used survives, and new rollouts record
// `model_search_state` instead.

// legacySearchConnectorID is Rust's SEARCH_CONNECTOR_ID.
const legacySearchConnectorID = "connector_openai_search_service"

// UnmarshalJSON decodes the harness metadata and translates an old
// `mcp_attribution` member into LegacySearchMCPUsed. Every other member is
// decoded as the plain struct would; the legacy member is never serialized.
func (m *HarnessMetadata) UnmarshalJSON(data []byte) error {
	wire := struct {
		*plainHarnessMetadata
		RawMcpAttribution json.RawMessage `json:"mcp_attribution"`
	}{plainHarnessMetadata: (*plainHarnessMetadata)(m)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.RawMcpAttribution != nil {
		used := legacyMcpAttributionIndicatesSearchMCP(wire.RawMcpAttribution)
		m.LegacySearchMCPUsed = &used
	}
	return nil
}

// plainHarnessMetadata strips HarnessMetadata's methods for a nested decode.
type plainHarnessMetadata HarnessMetadata

// legacyMcpAttributionIndicatesSearchMCP mirrors Rust's
// legacy_mcp_attribution::deserialize_search_mcp_use: a value this build cannot
// decode counts as possible Search MCP use so native search fallback stays
// disabled. An `attribution_error` status and any reported source on the Search
// connector also count as use.
func legacyMcpAttributionIndicatesSearchMCP(raw json.RawMessage) bool {
	var attribution struct {
		Status  string `json:"status"`
		Sources []struct {
			ConnectorID *string `json:"connector_id"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(raw, &attribution); err != nil {
		return true
	}
	switch attribution.Status {
	case "none", "complete":
	default:
		// Rust's LegacyStatus maps every other status, including a missing or
		// unrecognized one, to the error arm.
		return true
	}
	for _, source := range attribution.Sources {
		if source.ConnectorID != nil && *source.ConnectorID == legacySearchConnectorID {
			return true
		}
	}
	return false
}
