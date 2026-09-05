package codexapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	ClientOpenAIBetaHeader                       = "OpenAI-Beta"
	ClientCodexInstallationIDHeader              = "x-codex-installation-id"
	ClientCodexRoutingHintHeader                 = "x-codex-routing-hint"
	ClientCodexTurnStateHeader                   = "x-codex-turn-state"
	ClientCodexTurnMetadataHeader                = "x-codex-turn-metadata"
	ClientCodexParentThreadIDHeader              = "x-codex-parent-thread-id"
	ClientCodexWindowIDHeader                    = "x-codex-window-id"
	ClientCodexBetaFeaturesHeader                = "x-codex-beta-features"
	ClientOpenAIMemgenRequestHeader              = "x-openai-memgen-request"
	ClientOpenAISubagentHeader                   = "x-openai-subagent"
	ClientResponsesAPIIncludeTimingMetricsHeader = "x-responsesapi-include-timing-metrics"
)

type ClientContentKind string

const (
	ClientContentText  ClientContentKind = "text"
	ClientContentImage ClientContentKind = "image"
)

type ClientContentItem struct {
	Kind     ClientContentKind
	Text     string
	ImageURL string
	Detail   string
}

type ClientResponseItem struct {
	Type    string
	Role    string
	Content []ClientContentItem
}

type ClientPrompt struct {
	Input              []ClientResponseItem
	Tools              []string
	ParallelToolCalls  bool
	BaseInstructions   string
	OutputSchema       map[string]any
	OutputSchemaStrict bool
}

func NewClientPrompt() *ClientPrompt {
	return &ClientPrompt{OutputSchemaStrict: true}
}

func (p *ClientPrompt) FormattedInput(useResponsesLite bool) []ClientResponseItem {
	if p == nil {
		return nil
	}
	input := cloneClientResponseItems(p.Input)
	if useResponsesLite {
		stripClientImageDetails(input)
	}
	return input
}

type ClientRequestKind string

const (
	ClientRequestTurn       ClientRequestKind = "turn"
	ClientRequestPrewarm    ClientRequestKind = "prewarm"
	ClientRequestCompaction ClientRequestKind = "compaction"
	ClientRequestMemory     ClientRequestKind = "memory"
)

type ClientCompactionMetadata struct {
	Trigger        string `json:"trigger"`
	Reason         string `json:"reason"`
	Implementation string `json:"implementation"`
	Phase          string `json:"phase"`
	Strategy       string `json:"strategy"`
}

type ClientWorkspaceMetadata struct {
	AssociatedRemoteURLs map[string]string `json:"associated_remote_urls,omitempty"`
	LatestGitCommitHash  string            `json:"latest_git_commit_hash,omitempty"`
	HasChanges           *bool             `json:"has_changes,omitempty"`
}

type ClientMetadata struct {
	InstallationID  string
	SessionID       string
	ThreadID        string
	TurnID          string
	WindowID        string
	ContextWindowID string
	// WindowNumber is the zero-based number of the current context window,
	// included in Responses turn metadata (Rust #40987).
	WindowNumber *uint64
	RequestKind  ClientRequestKind
	Compaction   *ClientCompactionMetadata
	// AgentName is the canonical agent path for sub-agent turns (Rust #38483).
	// It is emitted in turn metadata only when set; callers fall back to
	// "/root" like Rust.
	AgentName                  string
	ForkedFromThreadID         string
	ParentThreadID             string
	ParentTurnID               string
	RootTurnID                 string
	SubagentHeader             string
	SubagentKind               string
	ThreadSource               string
	TurnTrigger                string
	CodexVersion               string
	Sandbox                    string
	SandboxMode                string
	AutoReviewEnabled          *bool
	NodeReplAutoReviewRequired *bool
	NodeReplDisabled           *bool
	Workspaces                 map[string]ClientWorkspaceMetadata
	TurnStartedAtUnixMS        int64
	Extra                      map[string]string
	// ResponsesAPIMetadata carries bounded, product-owned metadata from the
	// `responses_api_metadata` config (Rust 9e301c8c9a). Product metadata takes
	// precedence over client-provided Extra values and is kept out of metadata
	// sent to external MCP servers.
	ResponsesAPIMetadata map[string]string
}

func NewClientMetadata(installationID string, sessionID string, threadID string, windowID string) *ClientMetadata {
	return &ClientMetadata{
		InstallationID: installationID,
		SessionID:      sessionID,
		ThreadID:       threadID,
		WindowID:       windowID,
		Workspaces:     map[string]ClientWorkspaceMetadata{},
		Extra:          map[string]string{},
	}
}

func (m *ClientMetadata) HasTurnMetadata() bool {
	return m != nil && m.RequestKind != ""
}

func (m *ClientMetadata) TurnMetadataValue() map[string]any {
	if m == nil {
		return nil
	}
	value := map[string]any{}
	hasTurnIdentity := m.RequestKind != ClientRequestMemory
	hasRequestIdentity := m.RequestKind != "" && m.RequestKind != ClientRequestMemory
	if hasRequestIdentity {
		value["installation_id"] = m.InstallationID
		value["window_id"] = m.WindowID
		if m.ContextWindowID != "" {
			value["context_window_id"] = m.ContextWindowID
		}
		if m.WindowNumber != nil {
			value["window_number"] = *m.WindowNumber
		}
	}
	if hasTurnIdentity {
		value["session_id"] = m.SessionID
		value["thread_id"] = m.ThreadID
		if m.AgentName != "" {
			value[AgentNameKey] = m.AgentName
		}
		if m.TurnID != "" {
			value["turn_id"] = m.TurnID
		}
	}
	if m.RequestKind != "" {
		value["request_kind"] = string(m.RequestKind)
	}
	if m.Compaction != nil {
		value["compaction"] = m.Compaction
	}
	if m.ForkedFromThreadID != "" {
		value["forked_from_thread_id"] = m.ForkedFromThreadID
	}
	if m.ParentThreadID != "" {
		value["parent_thread_id"] = m.ParentThreadID
	}
	if m.ParentTurnID != "" {
		value["parent_turn_id"] = m.ParentTurnID
	}
	if m.RootTurnID != "" {
		value[RootTurnIDKey] = m.RootTurnID
	}
	if m.SubagentKind != "" {
		value["subagent_kind"] = m.SubagentKind
	}
	if m.ThreadSource != "" {
		value["thread_source"] = m.ThreadSource
	}
	if m.TurnTrigger != "" {
		value["turn_trigger"] = m.TurnTrigger
	}
	if m.CodexVersion != "" {
		value["codex_version"] = m.CodexVersion
	}
	if m.Sandbox != "" {
		value["sandbox"] = m.Sandbox
	}
	if m.SandboxMode != "" {
		value["sandbox_mode"] = m.SandboxMode
	}
	if m.AutoReviewEnabled != nil {
		value[AutoReviewEnabledKey] = *m.AutoReviewEnabled
	}
	if m.NodeReplAutoReviewRequired != nil {
		value[NodeReplAutoReviewRequiredKey] = *m.NodeReplAutoReviewRequired
	}
	if m.NodeReplDisabled != nil {
		value[NodeReplDisabledKey] = *m.NodeReplDisabled
	}
	if len(m.Workspaces) > 0 {
		value["workspaces"] = m.Workspaces
	}
	if m.TurnStartedAtUnixMS != 0 {
		value["turn_started_at_unix_ms"] = m.TurnStartedAtUnixMS
	}
	for key, extra := range m.Extra {
		if ClientReservedMetadataKeys()[key] {
			continue
		}
		value[key] = extra
	}
	for key, productValue := range m.ResponsesAPIMetadata {
		if ClientReservedMetadataKeys()[key] {
			continue
		}
		value[key] = productValue
	}
	return value
}

func (m *ClientMetadata) TurnMetadataJSON() (string, bool) {
	value := m.TurnMetadataValue()
	if value == nil {
		return "", false
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(bytes), true
}

func (m *ClientMetadata) ClientMetadata() map[string]string {
	if m == nil {
		return nil
	}
	out := map[string]string{
		ClientCodexInstallationIDHeader: m.InstallationID,
		"session_id":                    m.SessionID,
		"thread_id":                     m.ThreadID,
		ClientCodexWindowIDHeader:       m.WindowID,
	}
	if m.TurnID != "" {
		out["turn_id"] = m.TurnID
	}
	if m.SubagentHeader != "" {
		out[ClientOpenAISubagentHeader] = m.SubagentHeader
	}
	if m.ParentThreadID != "" {
		out[ClientCodexParentThreadIDHeader] = m.ParentThreadID
	}
	if m.ParentTurnID != "" {
		out["parent_turn_id"] = m.ParentTurnID
	}
	if m.HasTurnMetadata() {
		if jsonText, ok := m.TurnMetadataJSON(); ok {
			out[ClientCodexTurnMetadataHeader] = jsonText
		}
	}
	return out
}

func (m *ClientMetadata) CompatibilityHeaders() map[string]string {
	if m == nil {
		return nil
	}
	headers := map[string]string{ClientCodexWindowIDHeader: m.WindowID}
	if m.HasTurnMetadata() {
		if jsonText, ok := clientCompatibilityTurnMetadataJSON(m.TurnMetadataValue()); ok {
			headers[ClientCodexTurnMetadataHeader] = jsonText
		}
	}
	if m.ParentThreadID != "" {
		headers[ClientCodexParentThreadIDHeader] = m.ParentThreadID
	}
	if m.SubagentHeader != "" {
		headers[ClientOpenAISubagentHeader] = m.SubagentHeader
	}
	return headers
}

func clientCompatibilityTurnMetadataJSON(value map[string]any) (string, bool) {
	if value == nil {
		return "", false
	}
	delete(value, CodeModeToolNamesKey)
	bytes, err := json.Marshal(value)
	return string(bytes), err == nil
}

func ClientFilterExtraMetadata(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	reserved := ClientReservedMetadataKeys()
	for key, value := range values {
		if reserved[strings.ToLower(key)] || key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func ClientReservedMetadataKeys() map[string]bool {
	keys := []string{
		"installation_id", strings.ToLower(ClientCodexInstallationIDHeader),
		"session_id", "thread_id", "turn_id", AgentNameKey, "window_id", strings.ToLower(ClientCodexWindowIDHeader),
		strings.ToLower(ClientCodexTurnMetadataHeader), strings.ToLower(ClientCodexParentThreadIDHeader),
		strings.ToLower(ClientOpenAISubagentHeader), "request_kind", "compaction",
		"turn_started_at_unix_ms", "forked_from_thread_id", "parent_thread_id", "parent_turn_id", RootTurnIDKey,
		"subagent_kind", "thread_source", "sandbox", "sandbox_mode", "workspaces",
		"turn_trigger", "codex_version",
		AutoReviewEnabledKey, NodeReplAutoReviewRequiredKey, NodeReplDisabledKey, CodeModeToolNamesKey,
	}
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

type ClientRetryRequest string

const (
	ClientRetrySampling           ClientRetryRequest = "sampling"
	ClientRetryRemoteCompactionV2 ClientRetryRequest = "remote_compaction_v2"
)

type ClientRetryState struct {
	Retries      uint64
	MaxRetries   uint64
	UsedFallback bool
}

type ClientRetryDecision struct {
	Retry      bool
	Fallback   bool
	Delay      time.Duration
	NotifyUser bool
	Error      error
}

type ClientRetryableError struct {
	Message        string
	RequestedDelay time.Duration
}

func (e *ClientRetryableError) Error() string {
	return e.Message
}

func (e *ClientRetryableError) RequestedRetryDelay() (time.Duration, bool) {
	if e == nil {
		return 0, false
	}
	return e.RequestedDelay, e.RequestedDelay > 0
}

func (s *ClientRetryState) Handle(err error, request ClientRetryRequest, canFallback bool, websocketEnabled bool, debug bool) ClientRetryDecision {
	if s.MaxRetries == 0 {
		s.MaxRetries = 3
	}
	if s.Retries >= s.MaxRetries && canFallback && !s.UsedFallback {
		s.Retries = 0
		s.UsedFallback = true
		return ClientRetryDecision{Retry: true, Fallback: true, Delay: 0}
	}
	if s.Retries < s.MaxRetries {
		s.Retries++
		delay := ClientBackoff(s.Retries)
		if requested, ok := RetryDelayInfo(err); ok {
			delay = requested
		}
		notify := s.Retries > 1 || debug || !websocketEnabled || request == ClientRetryRemoteCompactionV2
		return ClientRetryDecision{Retry: true, Delay: delay, NotifyUser: notify}
	}
	return ClientRetryDecision{Error: err}
}

func ClientBackoff(retry uint64) time.Duration {
	if retry == 0 {
		return 0
	}
	delay := time.Duration(1<<minClientUint64(retry-1, 5)) * 100 * time.Millisecond
	return delay
}

func cloneClientResponseItems(items []ClientResponseItem) []ClientResponseItem {
	out := make([]ClientResponseItem, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].Content = append([]ClientContentItem(nil), items[i].Content...)
	}
	return out
}

func stripClientImageDetails(items []ClientResponseItem) {
	for i := range items {
		for j := range items[i].Content {
			if items[i].Content[j].Kind == ClientContentImage {
				items[i].Content[j].Detail = ""
			}
		}
	}
}

func minClientUint64(a uint64, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func ClientSubagentHeaderValue(sessionSource string) string {
	header, _ := ClientSubagentMetadataFromSource(sessionSource)
	return header
}

func ClientSubagentMetadataKind(sessionSource string) string {
	_, kind := ClientSubagentMetadataFromSource(sessionSource)
	return kind
}

func ClientSubagentMetadataFromSource(sessionSource string) (string, string) {
	raw := strings.TrimSpace(sessionSource)
	if raw == "" {
		return "", ""
	}
	label, isSubagent := clientSubagentSourceLabel(raw)
	normalizedLabel := clientNormalizeSubagentSource(label)
	if isSubagent {
		switch normalizedLabel {
		case "review":
			return "review", "review"
		case "compact":
			return "compact", "compact"
		case "memoryconsolidation":
			return "memory_consolidation", "memory_consolidation"
		}
		if strings.HasPrefix(normalizedLabel, "threadspawn") {
			return "collab_spawn", "thread_spawn"
		}
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			return trimmed, trimmed
		}
		return "", ""
	}
	switch clientNormalizeSubagentSource(raw) {
	case "internalmemoryconsolidation":
		return "memory_consolidation", ""
	case "review":
		return "review", "review"
	case "compact":
		return "compact", "compact"
	case "memoryconsolidation":
		return "memory_consolidation", "memory_consolidation"
	case "threadspawn":
		return "collab_spawn", "thread_spawn"
	default:
		return "", ""
	}
}

func clientSubagentSourceLabel(source string) (string, bool) {
	lower := strings.ToLower(source)
	for _, prefix := range []string{"subagent:", "subagent_", "subagent-"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(source[len(prefix):]), true
		}
	}
	return "", false
}

func clientNormalizeSubagentSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer("_", "", "-", "", ":", "", " ", "").Replace(value)
}

func (c *ClientCompactionMetadata) EnsureDefaults() {
	if c == nil {
		return
	}
	if c.Strategy == "" {
		c.Strategy = "memento"
	}
}

func (m *ClientMetadata) String() string {
	text, ok := m.TurnMetadataJSON()
	if !ok {
		return "{}"
	}
	return fmt.Sprintf("%s", text)
}
