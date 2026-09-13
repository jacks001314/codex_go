package telemetry

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Rust parity: codex-otel's SessionTelemetry::conversation_starts and the
// codex-login auth env telemetry that supplies its `auth.env_*` fields
// (codex-rs/login/src/auth_env_telemetry.rs).

// Auth-related environment variable names mirror codex-login's constants.
const (
	OpenAIAPIKeyEnvVar            = "OPENAI_API_KEY"
	CodexAPIKeyEnvVar             = "CODEX_API_KEY"
	RefreshTokenURLOverrideEnvVar = "CODEX_REFRESH_TOKEN_URL_OVERRIDE"
)

// AuthEnvTelemetryMetadata mirrors codex-otel's AuthEnvTelemetryMetadata: which
// auth-related environment variables this process sees, never their values.
type AuthEnvTelemetryMetadata struct {
	OpenAIAPIKeyEnvPresent         bool
	CodexAPIKeyEnvPresent          bool
	CodexAPIKeyEnvEnabled          bool
	ProviderEnvKeyName             string
	ProviderEnvKeyPresent          *bool
	RefreshTokenURLOverridePresent bool
}

// CollectAuthEnvTelemetry mirrors codex-login's collect_auth_env_telemetry: the
// provider's env key is reported as the literal "configured" together with
// whether that variable is set, so the key name itself never leaves the process.
func CollectAuthEnvTelemetry(providerEnvKey string, codexAPIKeyEnvEnabled bool) AuthEnvTelemetryMetadata {
	metadata := AuthEnvTelemetryMetadata{
		OpenAIAPIKeyEnvPresent:         envVarPresent(OpenAIAPIKeyEnvVar),
		CodexAPIKeyEnvPresent:          envVarPresent(CodexAPIKeyEnvVar),
		CodexAPIKeyEnvEnabled:          codexAPIKeyEnvEnabled,
		RefreshTokenURLOverridePresent: envVarPresent(RefreshTokenURLOverrideEnvVar),
	}
	if providerEnvKey = strings.TrimSpace(providerEnvKey); providerEnvKey != "" {
		metadata.ProviderEnvKeyName = "configured"
		present := envVarPresent(providerEnvKey)
		metadata.ProviderEnvKeyPresent = &present
	}
	return metadata
}

// ConversationStartsEvent mirrors the values
// SessionTelemetry::conversation_starts reports when a session starts.
type ConversationStartsEvent struct {
	ProviderName string
	// ReasoningEffort is absent when the session has no explicit effort.
	ReasoningEffort  string
	ReasoningSummary string
	// ContextWindow and AutoCompactTokenLimit are absent when unset.
	ContextWindow         *int64
	AutoCompactTokenLimit *int64
	ApprovalPolicy        string
	SandboxPolicy         string
	// MCPServers are the enabled MCP server names.
	MCPServers []string
}

// EmitConversationStarts mirrors SessionTelemetry::conversation_starts: the
// session identity plus the model/permission settings and the auth environment
// on both records, the MCP server names on the log record, and only their count
// on the trace-safe record.
func EmitConversationStarts(ctx context.Context, telemetry *SessionTelemetry, event ConversationStartsEvent) {
	if telemetry == nil {
		return
	}
	authEnv := telemetry.Metadata.AuthEnv
	common := map[string]string{
		"provider_name":                               event.ProviderName,
		"auth.env_openai_api_key_present":             strconv.FormatBool(authEnv.OpenAIAPIKeyEnvPresent),
		"auth.env_codex_api_key_present":              strconv.FormatBool(authEnv.CodexAPIKeyEnvPresent),
		"auth.env_codex_api_key_enabled":              strconv.FormatBool(authEnv.CodexAPIKeyEnvEnabled),
		"auth.env_refresh_token_url_override_present": strconv.FormatBool(authEnv.RefreshTokenURLOverridePresent),
		"reasoning_summary":                           event.ReasoningSummary,
		"approval_policy":                             event.ApprovalPolicy,
		"sandbox_policy":                              event.SandboxPolicy,
	}
	if authEnv.ProviderEnvKeyName != "" {
		common["auth.env_provider_key_name"] = authEnv.ProviderEnvKeyName
	}
	if authEnv.ProviderEnvKeyPresent != nil {
		common["auth.env_provider_key_present"] = strconv.FormatBool(*authEnv.ProviderEnvKeyPresent)
	}
	if event.ReasoningEffort != "" {
		common["reasoning_effort"] = event.ReasoningEffort
	}
	if event.ContextWindow != nil {
		common["context_window"] = strconv.FormatInt(*event.ContextWindow, 10)
	}
	if event.AutoCompactTokenLimit != nil {
		common["auto_compact_token_limit"] = strconv.FormatInt(*event.AutoCompactTokenLimit, 10)
	}
	servers := append([]string(nil), event.MCPServers...)
	sort.Strings(servers)
	telemetry.LogAndTraceEvent(ctx, "codex.conversation_starts", common,
		map[string]string{"mcp_servers": strings.Join(servers, ", ")},
		map[string]string{"mcp_server_count": strconv.Itoa(len(servers))})
}

// envVarPresent mirrors codex-login's env_var_present: an unset or blank
// variable is absent.
func envVarPresent(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	return strings.TrimSpace(os.Getenv(name)) != ""
}
