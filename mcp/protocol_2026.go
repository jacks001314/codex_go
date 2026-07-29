package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	modernMCPProtocol                = "2026-07-28"
	mcpProtocolVersionEnvVar         = "CODEX_MCP_PROTOCOL_VERSION"
	maxModernMCPMessageBytes         = 8 * 1024 * 1024
	mcpProtocolVersionMetadataKey    = "io.modelcontextprotocol/protocolVersion"
	mcpClientInfoMetadataKey         = "io.modelcontextprotocol/clientInfo"
	mcpClientCapabilitiesMetadataKey = "io.modelcontextprotocol/clientCapabilities"
	mcpJSONRPCMethodNotFoundCode     = int64(-32601)
	maxMCPInputRequiredRounds        = 1000
)

type MCPProtocolMode uint8

const (
	MCPProtocolLegacy MCPProtocolMode = iota
	MCPProtocol20260728
)

var errMCPModernProtocolUnsupported = errors.New("MCP server does not support protocol version 2026-07-28")

func mcpProtocolModeFromValues(values map[string]any) MCPProtocolMode {
	features, _ := values["features"].(map[string]any)
	switch feature := features["mcp_2026_07_28"].(type) {
	case bool:
		if feature {
			return MCPProtocol20260728
		}
	case map[string]any:
		if enabled, _ := feature["enabled"].(bool); enabled {
			return MCPProtocol20260728
		}
	}
	return MCPProtocolLegacy
}

func (m MCPProtocolMode) protocolVersion() string {
	if m == MCPProtocol20260728 {
		return modernMCPProtocol
	}
	return defaultMCPProtocol
}

func mcpClientCapabilities(openAIForm bool) map[string]any {
	capabilities := map[string]any{
		"roots": map[string]any{"listChanged": false},
	}
	if openAIForm {
		capabilities["extensions"] = map[string]any{
			"openai/form": map[string]any{},
		}
	}
	return capabilities
}

func mcpParamsWithProtocolMetadata(params any, mode MCPProtocolMode, openAIForm bool) any {
	if mode != MCPProtocol20260728 {
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
	meta[mcpClientCapabilitiesMetadataKey] = mcpClientCapabilities(openAIForm)
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
	var remoteErr *MCPRemoteError
	return errors.As(err, &remoteErr) && remoteErr.Code == mcpJSONRPCMethodNotFoundCode
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

func nextMCP2026RequestParams(ctx context.Context, serverName string, elicitation MCPElicitationHandler, baseParams any, result *json.RawMessage) (any, bool, error) {
	if result == nil {
		return nil, false, nil
	}
	var required mcpInputRequiredResult
	if err := json.Unmarshal(*result, &required); err != nil || required.ResultType != "input_required" {
		return nil, false, nil
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
	if state := bytes.TrimSpace(required.RequestState); len(state) > 0 && !bytes.Equal(state, []byte("null")) {
		params["requestState"] = append(json.RawMessage(nil), state...)
	}

	if len(required.InputRequests) > 0 {
		keys := make([]string, 0, len(required.InputRequests))
		for key := range required.InputRequests {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		responses := make(map[string]any, len(keys))
		for _, key := range keys {
			request := required.InputRequests[key]
			id, _ := json.Marshal(key)
			response, rpcErr := mcpClientRequestResult(ctx, serverName, elicitation, request.Method, id, request.Params)
			if rpcErr != nil {
				return nil, false, newMCPRemoteError(request.Method, rpcErr)
			}
			responses[key] = response
		}
		params["inputResponses"] = responses
	}
	return params, true, nil
}

func modernMCPMessageLimit(mode MCPProtocolMode) int64 {
	if mode == MCPProtocol20260728 {
		return maxModernMCPMessageBytes
	}
	return 0
}
