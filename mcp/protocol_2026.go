package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultMCPProtocol               = "2025-06-18"
	modernMCPProtocol                = "2026-07-28"
	mcpProtocolVersionEnvVar         = "CODEX_MCP_PROTOCOL_VERSION"
	maxModernMCPMessageBytes         = 8 * 1024 * 1024
	mcpProtocolVersionMetadataKey    = "io.modelcontextprotocol/protocolVersion"
	mcpClientInfoMetadataKey         = "io.modelcontextprotocol/clientInfo"
	mcpClientCapabilitiesMetadataKey = "io.modelcontextprotocol/clientCapabilities"
	mcpJSONRPCInvalidRequestCode     = int64(-32600)
	mcpJSONRPCMethodNotFoundCode     = int64(-32601)
	mcpJSONRPCInvalidParamsCode      = int64(-32602)
	mcpLegacyPrevalidationErrorCode  = int64(-32000)
	maxMCPInputRequiredRounds        = 1000
)

var knownLegacyMCPProtocols = map[string]struct{}{
	"2025-11-25": {},
	"2025-06-18": {},
	"2025-03-26": {},
	"2024-11-05": {},
}

type MCPProtocolMode uint8

const (
	MCPProtocolLegacy MCPProtocolMode = iota
	MCPProtocol20260728
	// MCPProtocolDiscoverLocalStdio probes an eligible local stdio server with
	// `server/discover` even when its launch config omits a version
	// (Rust #57210). A helper that cannot answer discovery is restarted with the
	// legacy handshake.
	MCPProtocolDiscoverLocalStdio
)

var errMCPModernProtocolUnsupported = errors.New("MCP server does not support protocol version 2026-07-28")

func mcpProtocolModeFromValues(values map[string]any) MCPProtocolMode {
	if mcpFeatureEnabled(values, "mcp_2026_07_28") {
		return MCPProtocol20260728
	}
	return MCPProtocolLegacy
}

// hostOwnedAppsProtocolModeFromValues mirrors Rust
// host_owned_apps_protocol_mode (feature codex_apps_mcp_2026_07_28): the
// trusted, HTTP Codex Apps registration has an independent protocol mode.
func hostOwnedAppsProtocolModeFromValues(values map[string]any) MCPProtocolMode {
	if mcpFeatureEnabled(values, "codex_apps_mcp_2026_07_28") {
		return MCPProtocol20260728
	}
	return MCPProtocolLegacy
}

func mcpFeatureEnabled(values map[string]any, key string) bool {
	features, _ := values["features"].(map[string]any)
	switch feature := features[key].(type) {
	case bool:
		return feature
	case map[string]any:
		if enabled, _ := feature["enabled"].(bool); enabled {
			return true
		}
	}
	return false
}

// effectiveMCPProtocolMode mirrors Rust McpConnectionSet resolution (#44571): a
// registration that selects its own mode wins, otherwise the host-owned Codex
// Apps registration uses its independent mode and every other server uses the
// default. The override applies only to Streamable HTTP connections, which is
// where the HTTP client consults config.ProtocolMode.
func effectiveMCPProtocolMode(runtime *RuntimeConfig, name string, registration ServerRegistration) MCPProtocolMode {
	if registration.Config.ProtocolModeOverride != nil {
		return *registration.Config.ProtocolModeOverride
	}
	if runtime == nil {
		return MCPProtocolLegacy
	}
	if isHostOwnedCodexAppsRegistration(name, registration) {
		return runtime.HostOwnedAppsProtocolMode
	}
	return runtime.ProtocolMode
}

func isHostOwnedCodexAppsRegistration(name string, registration ServerRegistration) bool {
	// Mirrors Rust McpServerSource::is_host_owned_apps: the exact Apps server
	// name, a local-environment config, and a compatibility (controller-owned)
	// source. Extension contributions do not gain host-owned Apps authority.
	if strings.TrimSpace(name) != CodexAppsServerName || !registration.Config.IsLocalEnvironment() {
		return false
	}
	return registration.Source == string(CatalogSourceCompatibility) ||
		registration.ContributorID == legacyCodexAppsRegistrationID
}

func (m MCPProtocolMode) protocolVersion() string {
	if m.usesModernMCPLifecycle() {
		return modernMCPProtocol
	}
	return defaultMCPProtocol
}

// usesModernMCPLifecycle reports whether this mode speaks the MCP 2026-07-28
// lifecycle: discovery, protocol metadata on every request, multi-round input
// and bounded message framing. Discovery mode does so until it falls back to the
// legacy handshake.
func (m MCPProtocolMode) usesModernMCPLifecycle() bool {
	return m == MCPProtocol20260728 || m == MCPProtocolDiscoverLocalStdio
}

func mcpClientCapabilities(openAIForm bool) map[string]any {
	return mcpClientCapabilitiesWithAuthChange(openAIForm, false)
}

// mcpClientCapabilitiesWithAuthChange mirrors mcpClientCapabilities and also
// offers the experimental auth-change capability when a source is attached
// (Rust #43428).
func mcpClientCapabilitiesWithAuthChange(openAIForm bool, advertiseAuthChange bool) map[string]any {
	return mcpClientCapabilitiesWithFeatures(openAIForm, advertiseAuthChange, false)
}

// mcpClientCapabilitiesWithFeatures mirrors Rust #43524: user verification is
// advertised only to the host-owned apps MCP server.
func mcpClientCapabilitiesWithFeatures(openAIForm bool, advertiseAuthChange bool, userVerification bool) map[string]any {
	return mcpClientCapabilitiesWithAllFeatures(openAIForm, advertiseAuthChange, userVerification, false)
}

func mcpClientCapabilitiesWithAllFeatures(openAIForm bool, advertiseAuthChange bool, userVerification bool, emaAuth bool) map[string]any {
	return mcpClientCapabilitiesForFeatures(mcpClientFeatures{
		OpenAIForm:       openAIForm,
		AuthChange:       advertiseAuthChange,
		UserVerification: userVerification,
		EMAAuth:          emaAuth,
	})
}

// mcpClientCapabilitiesForFeatures builds the client capabilities one
// connection offers. Network policy is an experimental capability projected
// only to trusted ChatGPT origins (Rust #56278).
func mcpClientCapabilitiesForFeatures(features mcpClientFeatures) map[string]any {
	capabilities := map[string]any{
		"roots": map[string]any{"listChanged": false},
	}
	if features.OpenAIForm {
		elicitation := map[string]any{"form": map[string]any{}}
		if features.UserVerification {
			elicitation["userVerification"] = map[string]any{}
		}
		capabilities["extensions"] = map[string]any{
			// Rust #41447 advertises the umbrella elicitation form capability
			// alongside the legacy openai/form extension.
			"openai/form":        map[string]any{},
			"openai/elicitation": elicitation,
		}
	}
	if features.AuthChange {
		mcpAdvertiseAuthChangeCapability(capabilities)
	}
	if features.EMAAuth {
		extensions, _ := capabilities["extensions"].(map[string]any)
		if extensions == nil {
			extensions = map[string]any{}
		}
		extensions["io.modelcontextprotocol/enterprise-managed-authorization"] = map[string]any{}
		capabilities["extensions"] = extensions
	}
	if features.NetworkPolicy {
		mcpAdvertiseExperimentalCapability(capabilities, MCPNetworkPolicyMetaCapability)
	}
	return capabilities
}

func mcpParamsWithProtocolMetadata(params any, mode MCPProtocolMode, openAIForm bool) any {
	return mcpParamsWithProtocolMetadataAndAuthChange(params, mode, openAIForm, false)
}

func mcpParamsWithProtocolMetadataAndAuthChange(params any, mode MCPProtocolMode, openAIForm bool, advertiseAuthChange bool) any {
	return mcpParamsWithProtocolMetadataAndFeatures(params, mode, openAIForm, advertiseAuthChange, false)
}

func mcpParamsWithProtocolMetadataAndFeatures(params any, mode MCPProtocolMode, openAIForm bool, advertiseAuthChange bool, userVerification bool) any {
	return mcpParamsWithProtocolMetadataAndAllFeatures(params, mode, openAIForm, advertiseAuthChange, userVerification, false)
}

func mcpParamsWithProtocolMetadataAndAllFeatures(params any, mode MCPProtocolMode, openAIForm bool, advertiseAuthChange bool, userVerification bool, emaAuth bool) any {
	return mcpParamsWithProtocolMetadataForFeatures(params, mode, mcpClientFeatures{
		OpenAIForm:       openAIForm,
		AuthChange:       advertiseAuthChange,
		UserVerification: userVerification,
		EMAAuth:          emaAuth,
	})
}

// mcpParamsWithProtocolMetadataForFeatures adds the protocol metadata every
// modern request carries, including the client capabilities this connection
// advertised during initialization.
func mcpParamsWithProtocolMetadataForFeatures(params any, mode MCPProtocolMode, features mcpClientFeatures) any {
	if !mode.usesModernMCPLifecycle() {
		return params
	}
	out := map[string]any{}
	if params != nil {
		data, err := json.Marshal(params)
		if err == nil {
			_ = json.Unmarshal(data, &out)
		}
	}
	meta, _ := out["_meta"].(map[string]any)
	meta = cloneAnyMap(meta)
	if meta == nil {
		meta = map[string]any{}
	}
	meta[mcpProtocolVersionMetadataKey] = modernMCPProtocol
	meta[mcpClientInfoMetadataKey] = map[string]string{"name": "codex-go", "version": "go-port"}
	meta[mcpClientCapabilitiesMetadataKey] = mcpClientCapabilitiesForFeatures(features)
	out["_meta"] = meta
	return out
}

type mcpDiscoveryResult struct {
	ResultType        string          `json:"resultType"`
	SupportedVersions []string        `json:"supportedVersions"`
	Capabilities      json.RawMessage `json:"capabilities"`
}

func validateMCPModernDiscovery(result *json.RawMessage) error {
	if result == nil {
		return errors.New("MCP server/discover response is missing a result")
	}
	var discovery mcpDiscoveryResult
	if err := json.Unmarshal(*result, &discovery); err != nil {
		return fmt.Errorf("decode MCP server/discover response: %w", err)
	}
	for _, version := range discovery.SupportedVersions {
		if strings.TrimSpace(version) == modernMCPProtocol {
			return nil
		}
	}
	return errMCPModernProtocolUnsupported
}

func isMCPDiscoveryFallbackError(err error) bool {
	if errors.Is(err, errMCPModernProtocolUnsupported) {
		return true
	}
	// Rust #39798 (rmcp 3.1.3): any server/discover JSON-RPC error falls back
	// to legacy initialization while preserving its diagnostics; unproven
	// rejections are reclassified as header mismatches so authentication and
	// retry classifications survive the fallback.
	var remoteErr *MCPRemoteError
	return errors.As(err, &remoteErr) && remoteErr.Code != 0
}

func hasMCPLegacyFallbackEvidence(message string) bool {
	if message == "Bad Request: No valid session ID provided" {
		return true
	}
	const prefixes = "Bad Request: Unsupported protocol version: 2026-07-28 (supported versions: "
	const legacyPrefix = "Bad Request: Unsupported protocol version (supported versions: "
	supported, ok := strings.CutPrefix(message, prefixes)
	if !ok {
		supported, ok = strings.CutPrefix(message, legacyPrefix)
	}
	if !ok || !strings.HasSuffix(supported, ")") {
		return false
	}
	versions := strings.Split(strings.TrimSuffix(supported, ")"), ",")
	hasKnown := false
	for _, raw := range versions {
		version := strings.TrimSpace(raw)
		if !isLegacyMCPProtocolDate(version) {
			return false
		}
		if _, ok := knownLegacyMCPProtocols[version]; ok {
			hasKnown = true
		}
	}
	return len(versions) > 0 && hasKnown
}

func isLegacyMCPProtocolDate(version string) bool {
	if len(version) != 10 || version >= modernMCPProtocol || version[4] != '-' || version[7] != '-' {
		return false
	}
	for index := range version {
		if index == 4 || index == 7 {
			continue
		}
		if version[index] < '0' || version[index] > '9' {
			return false
		}
	}
	return true
}

// mcpInputRequestSequence numbers prompts raised by MCP input-required
// continuations. Rust #57196 requires globally unique input ids because a
// prompt outlives the transport request that raised it; the id never reaches the
// server, which correlates responses by the request's own map key.
var mcpInputRequestSequence atomic.Uint64

func nextMCPInputRequestIDValue() json.RawMessage {
	id := mcpInputRequestSequence.Add(1) - 1
	return json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("mcp-input/%d", id)))
}

type mcpInputRequiredResult struct {
	ResultType    string                             `json:"resultType"`
	InputRequests map[string]mcpInputRequiredRequest `json:"inputRequests"`
	RequestState  json.RawMessage                    `json:"requestState"`
}

type mcpInputRequiredRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// mcpContinuationState carries one tool call's input-required bookkeeping
// across rounds, mirroring Rust's request_input loop state (Rust #44346).
type mcpContinuationState struct {
	stateOnlyRounds            int
	submittedVerificationProof bool
}

func nextMCP2026RequestParams(ctx context.Context, serverName string, elicitation MCPElicitationHandler, features mcpClientFeatures, state *mcpContinuationState, baseParams any, result *json.RawMessage) (any, bool, error) {
	if result == nil {
		return nil, false, nil
	}
	var required mcpInputRequiredResult
	if err := json.Unmarshal(*result, &required); err != nil || required.ResultType != "input_required" {
		return nil, false, nil
	}
	if state == nil {
		state = &mcpContinuationState{}
	}

	params := map[string]any{}
	if baseParams != nil {
		data, err := json.Marshal(baseParams)
		if err != nil {
			return nil, false, err
		}
		if err := json.Unmarshal(data, &params); err != nil {
			return nil, false, err
		}
	}
	hasRequestState := false
	if rawState := bytes.TrimSpace(required.RequestState); len(rawState) > 0 && !bytes.Equal(rawState, []byte("null")) {
		params["requestState"] = append(json.RawMessage(nil), rawState...)
		hasRequestState = true
	}
	if len(required.InputRequests) == 0 {
		if !hasRequestState {
			// Rust treats a continuation that carries neither prompts nor the
			// opaque state as an unexpected response.
			return nil, false, fmt.Errorf("MCP input-required response carried neither input requests nor request state")
		}
		// A state-only round means the server is still working; back off
		// 50/100/200/250ms instead of spinning on the transport.
		delay := 50 << min(state.stateOnlyRounds, 3)
		if delay > 250 {
			delay = 250
		}
		state.stateOnlyRounds++
		if err := sleepMCPContinuationRound(ctx, time.Duration(delay)*time.Millisecond); err != nil {
			return nil, false, err
		}
		return params, true, nil
	}
	state.stateOnlyRounds = 0

	keys := make([]string, 0, len(required.InputRequests))
	for key := range required.InputRequests {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Parse the whole round before presenting any prompt. Only standard MCP
	// inputs and supported OpenAI elicitation modes are accepted, so an
	// unsupported or malformed request never reaches the user (Rust #44346).
	for _, key := range keys {
		request := required.InputRequests[key]
		if rpcErr := validateMCPToolInputRequest(request.Method, request.Params, features); rpcErr != nil {
			return nil, false, newMCPRemoteError(request.Method, rpcErr)
		}
	}
	responses := make(map[string]any, len(keys))
	for _, key := range keys {
		request := required.InputRequests[key]
		response, rpcErr := mcpToolInputRequestResult(ctx, serverName, elicitation, request.Method, nextMCPInputRequestIDValue(), request.Params)
		if rpcErr != nil {
			return nil, false, newMCPRemoteError(request.Method, rpcErr)
		}
		if isNativeVerificationRequest(request.Method, request.Params) && mcpElicitationResponseAccepted(response) {
			// Once a signed proof is on the wire the call must not be replayed
			// by session recovery (Rust #44346).
			state.submittedVerificationProof = true
		}
		responses[key] = response
	}
	params["inputResponses"] = responses
	return params, true, nil
}

// validateMCPToolInputRequest mirrors Rust's tool_input::parse_input plus the
// capability-gated elicitation dispatch: unsupported methods, modes, and
// unadvertised capabilities are rejected before the user is prompted.
func validateMCPToolInputRequest(method string, params json.RawMessage, features mcpClientFeatures) *stdioRPCError {
	method = strings.TrimSpace(method)
	switch method {
	case "roots/list":
		return nil
	case "elicitation/create":
		if !isMCPJSONObject(params) {
			return mcpInvalidRequest("invalid MCP tool input request")
		}
		return nil
	case "openai/elicitation/create":
		switch mcpElicitationRequestMode(params) {
		case "form":
			if rpcErr := validateMCPOpenAIElicitationForm(params); rpcErr != nil {
				return rpcErr
			}
			if !features.OpenAIForm {
				if features.UserVerification {
					return mcpInvalidParams("invalid elicitation mode")
				}
				return mcpMethodNotFound(method)
			}
			return nil
		case UserVerificationMode:
			if rpcErr := validateMCPUserVerificationRequest(params); rpcErr != nil {
				return rpcErr
			}
			if !features.UserVerification {
				return mcpMethodNotFound(method)
			}
			return nil
		default:
			return mcpInvalidRequest("unsupported OpenAI elicitation mode")
		}
	default:
		return mcpInvalidRequest("unsupported MCP tool input request")
	}
}

// validateMCPOpenAIElicitationForm requires the fields Rust's
// OpenAiElicitationRequestParams deserializer requires for the form mode.
func validateMCPOpenAIElicitationForm(params json.RawMessage) *stdioRPCError {
	var payload struct {
		Mode            string          `json:"mode"`
		Message         *string         `json:"message"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	if len(params) == 0 || json.Unmarshal(params, &payload) != nil || payload.Mode != "form" || payload.Message == nil {
		return mcpInvalidParams("invalid OpenAI elicitation request")
	}
	schema := bytes.TrimSpace(payload.RequestedSchema)
	if len(schema) == 0 || bytes.Equal(schema, []byte("null")) {
		return mcpInvalidParams("invalid OpenAI elicitation request")
	}
	return nil
}

// mcpToolInputRequestResult answers one validated continuation prompt through
// the same elicitation handler the connection uses for server-initiated
// requests.
func mcpToolInputRequestResult(ctx context.Context, serverName string, elicitation MCPElicitationHandler, method string, id json.RawMessage, params json.RawMessage) (any, *stdioRPCError) {
	switch strings.TrimSpace(method) {
	case "roots/list":
		return map[string]any{"roots": mcpRootsFromContext(ctx)}, nil
	case "elicitation/create", "openai/form", "openai/elicitation/create":
		return mcpElicitationResult(ctx, serverName, elicitation, method, id, params), nil
	default:
		return nil, mcpMethodNotFound(method)
	}
}

func isNativeVerificationRequest(method string, params json.RawMessage) bool {
	return strings.TrimSpace(method) == "openai/elicitation/create" && mcpElicitationRequestMode(params) == UserVerificationMode
}

func mcpElicitationResponseAccepted(response any) bool {
	switch value := response.(type) {
	case *MCPElicitationResponse:
		return value != nil && value.Action == MCPElicitationActionAccept
	case MCPElicitationResponse:
		return value.Action == MCPElicitationActionAccept
	case map[string]any:
		action, _ := value["action"].(string)
		return action == string(MCPElicitationActionAccept)
	default:
		return false
	}
}

func isMCPJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

func mcpInvalidRequest(message string) *stdioRPCError {
	return &stdioRPCError{Code: mcpJSONRPCInvalidRequestCode, Message: message}
}

func mcpInvalidParams(message string) *stdioRPCError {
	return &stdioRPCError{Code: mcpJSONRPCInvalidParamsCode, Message: message}
}

func mcpMethodNotFound(method string) *stdioRPCError {
	return &stdioRPCError{Code: mcpJSONRPCMethodNotFoundCode, Message: "method not found: " + method}
}

func sleepMCPContinuationRound(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// mcpStdioHandshakeMode maps a configured protocol policy plus the server's
// declared CODEX_MCP_PROTOCOL_VERSION to the handshake that will run. The
// boolean reports whether the declaration is usable at all; an unrecognized
// version is invalid, exactly like Rust's McpProtocolMode::stdio_mode.
func mcpStdioHandshakeMode(mode MCPProtocolMode, env map[string]string) (MCPProtocolMode, bool) {
	if mode != MCPProtocol20260728 && mode != MCPProtocolDiscoverLocalStdio {
		return MCPProtocolLegacy, true
	}
	requested, configured := env[mcpProtocolVersionEnvVar]
	if !configured {
		if mode == MCPProtocolDiscoverLocalStdio {
			return MCPProtocolDiscoverLocalStdio, true
		}
		return MCPProtocolLegacy, true
	}
	if requested != modernMCPProtocol {
		return MCPProtocolLegacy, false
	}
	return MCPProtocol20260728, true
}

// mcpServerCanDiscoverLocalStdio mirrors Rust McpServer::can_discover_local_stdio
// (#57210): the reserved codex_apps server keeps a per-chat catalog, servers
// with runtime or enterprise-managed credentials need that chat's
// authentication state, and only stdio launches whose declared version lets
// discovery run qualify.
func mcpServerCanDiscoverLocalStdio(name string, config *ServerConfig) bool {
	if config == nil || strings.TrimSpace(name) == CodexAppsServerName {
		return false
	}
	if !config.IsLocalEnvironment() || strings.TrimSpace(config.Command) == "" {
		return false
	}
	if config.ApplyHTTPRequest != nil || config.EffectiveAuth() == ServerAuthEMAAuth {
		return false
	}
	handshake, valid := mcpStdioHandshakeMode(MCPProtocolDiscoverLocalStdio, config.Env)
	return valid && handshake.usesModernMCPLifecycle()
}

// isMCPLegacyInitializeResult reports whether a `server/discover` reply is
// actually a legacy initialize result: some helpers assume their first request
// is initialize. Such a helper has already spent its startup budget, so the
// legacy handshake needs a fresh process (Rust #57210).
func isMCPLegacyInitializeResult(result *json.RawMessage) bool {
	if result == nil {
		return false
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(*result, &initialized); err != nil {
		return false
	}
	return isLegacyMCPProtocolDate(strings.TrimSpace(initialized.ProtocolVersion))
}

func modernMCPMessageLimit(mode MCPProtocolMode) int64 {
	if mode.usesModernMCPLifecycle() {
		return maxModernMCPMessageBytes
	}
	return 0
}
