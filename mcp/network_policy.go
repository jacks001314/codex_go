package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"codex_go/sandbox"
)

// MCPNetworkMode is the Rust codex-network-proxy NetworkMode wire value.
type MCPNetworkMode string

const (
	MCPNetworkModeLimited MCPNetworkMode = "limited"
	MCPNetworkModeFull    MCPNetworkMode = "full"
)

// NetworkPolicyMetadata is the effective network restriction delivered with an
// MCP call. It never carries proxy endpoints or credentials (Rust #56278).
type NetworkPolicyMetadata struct {
	Enabled                 bool           `json:"enabled"`
	Mode                    MCPNetworkMode `json:"mode"`
	AllowedDomains          []string       `json:"allowedDomains"`
	DeniedDomains           []string       `json:"deniedDomains"`
	HardDenyAllowlistMisses bool           `json:"hardDenyAllowlistMisses"`
}

// NetworkPolicyMetadataProvider produces trusted network policy metadata from
// the MCP publication's frozen policy sources (Rust
// NetworkPolicyMetadataProvider). Implementations must derive the result from
// the supplied call authority and server environment and must never accept
// caller-supplied metadata.
type NetworkPolicyMetadataProvider interface {
	NetworkPolicyMetadataForCall(profile *sandbox.PermissionProfile, environmentID string) (*NetworkPolicyMetadata, error)
}

// networkPolicyAdvertisedForServer reports whether a connection to this server
// may offer the network policy capability: the runtime must carry a policy
// source and the server must be a trusted ChatGPT origin (Rust #56278).
func (s *MCPService) networkPolicyAdvertisedForServer(config *ServerConfig) bool {
	if s == nil || s.networkPolicyMetadata == nil {
		return false
	}
	return mcpIsTrustedChatGPTMCPServer(config, s.chatGPTBaseURL)
}

// mcpNetworkPolicyMetaForToolCall ports Rust
// McpConnectionSet::network_policy_meta_for_call. It always strips
// caller-supplied policy metadata and keeps one resolved policy snapshot for
// the call, attaching it only when the client advertised the capability and the
// session that sends the attempt also advertises it.
func (s *MCPService) mcpNetworkPolicyMetaForToolCall(server string, config *ServerConfig, environmentID string, meta any) func(json.RawMessage) (any, error) {
	resolver := &mcpNetworkPolicyMetaResolver{
		base:          mcpMetaWithoutNetworkPolicy(meta),
		advertised:    s.networkPolicyAdvertisedForServer(config),
		server:        server,
		environmentID: environmentID,
	}
	if resolver.advertised {
		resolver.provider = s.networkPolicyMetadata
		resolver.profile = s.permissionProfileForServer(server)
	}
	return resolver.metaFor
}

type mcpNetworkPolicyMetaResolver struct {
	base          any
	advertised    bool
	provider      NetworkPolicyMetadataProvider
	profile       *sandbox.PermissionProfile
	server        string
	environmentID string

	once   sync.Once
	policy any
	err    error
}

// metaFor returns the request metadata for the session that sends one attempt.
// A recovery retry may negotiate policy support for the first time, so the
// snapshot is resolved lazily and reused across attempts.
func (r *mcpNetworkPolicyMetaResolver) metaFor(capabilities json.RawMessage) (any, error) {
	if !r.advertised || !mcpServerExperimentalCapability(capabilities, MCPNetworkPolicyMetaCapability) {
		return r.base, nil
	}
	policy, err := r.snapshot()
	if err != nil {
		return nil, err
	}
	if r.base == nil {
		return map[string]any{MCPNetworkPolicyMetaCapability: policy}, nil
	}
	base, ok := r.base.(map[string]any)
	if !ok {
		return nil, errors.New("MCP tool request _meta must be a JSON object")
	}
	out := cloneAnyMap(base)
	if out == nil {
		out = map[string]any{}
	}
	out[MCPNetworkPolicyMetaCapability] = policy
	return out, nil
}

// snapshot resolves the call-scoped policy once, so a recovery retry reuses the
// grants captured for the original call (Rust OnceLock semantics).
func (r *mcpNetworkPolicyMetaResolver) snapshot() (any, error) {
	r.once.Do(func() {
		if r.provider == nil {
			r.err = errors.New("MCP network policy metadata source is unavailable")
			return
		}
		if r.profile == nil {
			r.err = fmt.Errorf("MCP server %q has no captured permission authority", r.server)
			return
		}
		metadata, err := r.provider.NetworkPolicyMetadataForCall(r.profile, r.environmentID)
		if err != nil {
			r.err = err
			return
		}
		if metadata == nil {
			r.err = errors.New("MCP network policy metadata source returned no metadata")
			return
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			r.err = err
			return
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			r.err = err
			return
		}
		r.policy = decoded
	})
	return r.policy, r.err
}

// mcpParamsWithReplacedMeta rebuilds tool-call style params with the given
// request _meta, dropping the key when the resolved value is nil.
func mcpParamsWithReplacedMeta(params any, meta any) any {
	paramMap, ok := params.(map[string]any)
	if !ok {
		return params
	}
	out := cloneAnyMap(paramMap)
	if out == nil {
		out = map[string]any{}
	}
	if meta == nil {
		delete(out, "_meta")
		return out
	}
	out["_meta"] = meta
	return out
}

// mcpMetaWithoutNetworkPolicy removes caller-supplied policy metadata. Server
// names and caller-supplied capabilities never grant the privilege, so a forged
// value is dropped even when no policy source is configured.
func mcpMetaWithoutNetworkPolicy(meta any) any {
	if meta == nil {
		return nil
	}
	metaMap, ok := meta.(map[string]any)
	if !ok {
		return meta
	}
	if _, present := metaMap[MCPNetworkPolicyMetaCapability]; !present {
		return metaMap
	}
	out := cloneAnyMap(metaMap)
	delete(out, MCPNetworkPolicyMetaCapability)
	if out == nil {
		out = map[string]any{}
	}
	return out
}
