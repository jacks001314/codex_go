package codexapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	InstallationIDKey      = "installation_id"
	SessionIDKey           = "session_id"
	ThreadIDKey            = "thread_id"
	TurnIDKey              = "turn_id"
	WindowIDKey            = "window_id"
	RequestKindKey         = "request_kind"
	CompactionKey          = "compaction"
	CodeModeToolNamesKey   = "code_mode_tool_names"
	TurnStartedAtUnixMSKey = "turn_started_at_unix_ms"
	ForkedFromThreadIDKey  = "forked_from_thread_id"
	ParentThreadIDKey      = "parent_thread_id"
	ParentTurnIDKey        = "parent_turn_id"
	SubagentKindKey        = "subagent_kind"
	ThreadSourceKey        = "thread_source"
	SandboxKey             = "sandbox"
	SandboxModeKey         = "sandbox_mode"
	WorkspacesKey          = "workspaces"
	InstallationIDHeader   = "x-codex-installation-id"
	WindowIDHeader         = "x-codex-window-id"
	TurnMetadataHeader     = "x-codex-turn-metadata"
	ParentThreadIDHeader   = "x-codex-parent-thread-id"
	OpenAISubagentHeader   = "x-openai-subagent"
	RequestKindTurn        = "turn"
	RequestKindPrewarm     = "prewarm"
	RequestKindCompaction  = "compaction"
	RequestKindMemory      = "memory"
)

var reservedMetadataKeys = map[string]bool{
	InstallationIDKey:      true,
	InstallationIDHeader:   true,
	SessionIDKey:           true,
	ThreadIDKey:            true,
	TurnIDKey:              true,
	WindowIDKey:            true,
	WindowIDHeader:         true,
	TurnMetadataHeader:     true,
	ParentThreadIDHeader:   true,
	OpenAISubagentHeader:   true,
	RequestKindKey:         true,
	CompactionKey:          true,
	CodeModeToolNamesKey:   true,
	TurnStartedAtUnixMSKey: true,
	ForkedFromThreadIDKey:  true,
	ParentThreadIDKey:      true,
	ParentTurnIDKey:        true,
	SubagentKindKey:        true,
	ThreadSourceKey:        true,
	SandboxKey:             true,
	SandboxModeKey:         true,
	WorkspacesKey:          true,
}

const (
	maxExtraMetadataEntries    = 16
	maxExtraMetadataKeyBytes   = 64
	maxExtraMetadataValueBytes = 128
)

type ResponsesRequestKind struct {
	Kind       string                       `json:"kind"`
	Compaction *ResponsesCompactionMetadata `json:"compaction,omitempty"`
}

type ResponsesCompactionMetadata struct {
	Trigger        string `json:"trigger"`
	Reason         string `json:"reason"`
	Implementation string `json:"implementation"`
	Phase          string `json:"phase"`
	Strategy       string `json:"strategy"`
}

func NewResponsesCompactionMetadata(trigger string, reason string, implementation string, phase string) *ResponsesCompactionMetadata {
	return &ResponsesCompactionMetadata{Trigger: trigger, Reason: reason, Implementation: implementation, Phase: phase, Strategy: "memento"}
}

type ResponsesWorkspace struct {
	AssociatedRemoteURLs map[string]string `json:"associated_remote_urls,omitempty"`
	LatestGitCommitHash  string            `json:"latest_git_commit_hash,omitempty"`
	HasChanges           *bool             `json:"has_changes,omitempty"`
}

type ResponsesMetadata struct {
	InstallationID      string
	SessionID           string
	ThreadID            string
	TurnID              string
	WindowID            string
	RequestKind         *ResponsesRequestKind
	ForkedFromThreadID  string
	ParentThreadID      string
	ParentTurnID        string
	SubagentHeader      string
	SubagentKind        string
	ThreadSource        string
	Sandbox             string
	SandboxMode         string
	Workspaces          map[string]ResponsesWorkspace
	TurnStartedAtUnixMS *int64
	Extra               map[string]string
}

func NewResponsesMetadata(installationID string, sessionID string, threadID string, windowID string) *ResponsesMetadata {
	return &ResponsesMetadata{
		InstallationID: installationID,
		SessionID:      sessionID,
		ThreadID:       threadID,
		WindowID:       windowID,
		Workspaces:     map[string]ResponsesWorkspace{},
		Extra:          map[string]string{},
	}
}

func (m *ResponsesMetadata) HasTurnMetadata() bool {
	return m != nil && m.RequestKind != nil
}

func (m *ResponsesMetadata) TurnMetadataValue() map[string]any {
	if m == nil {
		return nil
	}
	payload := map[string]any{}
	hasTurnIdentity := m.RequestKind == nil || m.RequestKind.Kind != RequestKindMemory
	hasRequestIdentity := m.RequestKind != nil && m.RequestKind.Kind != RequestKindMemory
	if hasRequestIdentity {
		putStringAny(payload, InstallationIDKey, m.InstallationID)
		putStringAny(payload, WindowIDKey, m.WindowID)
	}
	if hasTurnIdentity {
		putStringAny(payload, SessionIDKey, m.SessionID)
		putStringAny(payload, ThreadIDKey, m.ThreadID)
		putStringAny(payload, TurnIDKey, m.TurnID)
	}
	if m.RequestKind != nil {
		putStringAny(payload, RequestKindKey, m.RequestKind.Kind)
		if m.RequestKind.Compaction != nil {
			payload[CompactionKey] = m.RequestKind.Compaction
		}
	}
	putStringAny(payload, ForkedFromThreadIDKey, m.ForkedFromThreadID)
	putStringAny(payload, ParentThreadIDKey, m.ParentThreadID)
	putStringAny(payload, ParentTurnIDKey, m.ParentTurnID)
	putStringAny(payload, SubagentKindKey, m.SubagentKind)
	putStringAny(payload, ThreadSourceKey, m.ThreadSource)
	putStringAny(payload, SandboxKey, m.Sandbox)
	putStringAny(payload, SandboxModeKey, m.SandboxMode)
	if len(m.Workspaces) > 0 {
		payload[WorkspacesKey] = m.Workspaces
	}
	if m.TurnStartedAtUnixMS != nil {
		payload[TurnStartedAtUnixMSKey] = *m.TurnStartedAtUnixMS
	}
	for _, key := range sortedKeys(m.Extra) {
		if !reservedMetadataKeys[key] {
			payload[key] = m.Extra[key]
		}
	}
	return payload
}

func (m *ResponsesMetadata) TurnMetadataJSON() (string, bool) {
	if m == nil {
		return "", false
	}
	data, err := json.Marshal(m.TurnMetadataValue())
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (m *ResponsesMetadata) ClientMetadata() map[string]string {
	if m == nil {
		return nil
	}
	out := map[string]string{
		InstallationIDHeader: m.InstallationID,
		SessionIDKey:         m.SessionID,
		ThreadIDKey:          m.ThreadID,
		WindowIDHeader:       m.WindowID,
	}
	putStringString(out, TurnIDKey, m.TurnID)
	putStringString(out, OpenAISubagentHeader, m.SubagentHeader)
	putStringString(out, ParentThreadIDHeader, m.ParentThreadID)
	putStringString(out, ParentTurnIDKey, m.ParentTurnID)
	if m.HasTurnMetadata() {
		if encoded, ok := m.TurnMetadataJSON(); ok {
			out[TurnMetadataHeader] = encoded
		}
	}
	return out
}

func (m *ResponsesMetadata) CompatibilityHeaders() map[string]string {
	if m == nil {
		return nil
	}
	out := map[string]string{WindowIDHeader: m.WindowID}
	if m.HasTurnMetadata() {
		value := m.TurnMetadataValue()
		delete(value, CodeModeToolNamesKey)
		if data, err := json.Marshal(value); err == nil {
			encoded := string(data)
			out[TurnMetadataHeader] = encoded
		}
	}
	putStringString(out, ParentThreadIDHeader, m.ParentThreadID)
	putStringString(out, OpenAISubagentHeader, m.SubagentHeader)
	return out
}

func FilterExtraMetadata(extra map[string]string) map[string]string {
	out := make(map[string]string, len(extra))
	for key, value := range extra {
		if reservedMetadataKeys[key] {
			continue
		}
		out[key] = value
	}
	return out
}

// ValidateResponsesAPIMetadata mirrors Rust validate_extra_metadata
// (9e301c8c9a): bounded product-owned metadata for `responses_api_metadata`
// with ASCII identifier keys, no reserved Codex keys, and bounded values.
func ValidateResponsesAPIMetadata(metadata map[string]string) error {
	count := 0
	for key, value := range metadata {
		count++
		if count > maxExtraMetadataEntries {
			return fmt.Errorf("responses_api_metadata may contain at most %d entries", maxExtraMetadataEntries)
		}
		if len(key) > maxExtraMetadataKeyBytes || !validExtraMetadataKey(key) {
			return fmt.Errorf("responses_api_metadata keys must be short ASCII identifiers")
		}
		if reservedMetadataKeys[key] {
			return fmt.Errorf("responses_api_metadata contains a reserved key")
		}
		if len(value) > maxExtraMetadataValueBytes {
			return fmt.Errorf("responses_api_metadata values may contain at most %d bytes", maxExtraMetadataValueBytes)
		}
	}
	return nil
}

func validExtraMetadataKey(key string) bool {
	if key == "" {
		return false
	}
	for index, character := range []byte(key) {
		if index == 0 {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') {
				return false
			}
			continue
		}
		isAlphanumeric := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')
		if !isAlphanumeric && character != '_' && character != '.' && character != '-' {
			return false
		}
	}
	return true
}

func SubagentHeaderValue(sessionSource string) string {
	switch strings.TrimSpace(sessionSource) {
	case "review":
		return "review"
	case "compact":
		return "compact"
	case "memory_consolidation":
		return "memory_consolidation"
	case "thread_spawn":
		return "collab_spawn"
	default:
		if strings.HasPrefix(sessionSource, "subagent:") {
			return strings.TrimPrefix(sessionSource, "subagent:")
		}
		return ""
	}
}

func putStringString(target map[string]string, key string, value string) {
	if strings.TrimSpace(value) != "" {
		target[key] = value
	}
}

func putStringAny(target map[string]any, key string, value string) {
	if strings.TrimSpace(value) != "" {
		target[key] = value
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
