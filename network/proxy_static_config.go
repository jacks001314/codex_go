package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// StaticNetworkProxyConfig is a resolved standalone policy, including the
// ceilings applied by managed requirements (Rust #57180). Keep the document
// outside the workload's writable filesystem.
type StaticNetworkProxyConfig struct {
	Network     ProxySettings
	Constraints ProxyConstraints
}

type staticProxyDocument struct {
	Network     standaloneNetworkSettings   `json:"network"`
	Constraints *standaloneProxyConstraints `json:"constraints,omitempty"`
	Metadata    json.RawMessage             `json:"metadata,omitempty"`
}

type strictStaticProxyDocument struct {
	Network     standaloneNetworkSettings   `json:"network"`
	Constraints *standaloneProxyConstraints `json:"constraints"`
}

// standaloneProxyConstraints mirrors Rust NetworkProxyConstraints.
type standaloneProxyConstraints struct {
	Enabled                          *bool     `json:"enabled,omitempty"`
	Mode                             *string   `json:"mode,omitempty"`
	AllowUpstreamProxy               *bool     `json:"allow_upstream_proxy,omitempty"`
	DangerouslyAllowNonLoopbackProxy *bool     `json:"dangerously_allow_non_loopback_proxy,omitempty"`
	DangerouslyAllowAllUnixSockets   *bool     `json:"dangerously_allow_all_unix_sockets,omitempty"`
	AllowedDomains                   *[]string `json:"allowed_domains,omitempty"`
	AllowlistExpansionEnabled        *bool     `json:"allowlist_expansion_enabled,omitempty"`
	DeniedDomains                    *[]string `json:"denied_domains,omitempty"`
	DenylistExpansionEnabled         *bool     `json:"denylist_expansion_enabled,omitempty"`
	AllowUnixSockets                 *[]string `json:"allow_unix_sockets,omitempty"`
	AllowLocalBinding                *bool     `json:"allow_local_binding,omitempty"`
}

// FromStaticProxyConfigBytes parses standalone policy, allowing optional
// constraints and a top-level metadata object (Rust
// StaticNetworkProxyConfig::from_slice).
func FromStaticProxyConfigBytes(data []byte) (*StaticNetworkProxyConfig, error) {
	return parseStaticProxyConfig(data, false)
}

// FromStaticProxyConfigBytesStrict requires explicit constraints and rejects
// unknown fields at every typed level, including top-level metadata.
func FromStaticProxyConfigBytesStrict(data []byte) (*StaticNetworkProxyConfig, error) {
	return parseStaticProxyConfig(data, true)
}

func parseStaticProxyConfig(data []byte, strict bool) (*StaticNetworkProxyConfig, error) {
	if len(data) > MaxStandaloneProxyConfigBytes {
		return nil, fmt.Errorf("network proxy config exceeds %d bytes", MaxStandaloneProxyConfigBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var network standaloneNetworkSettings
	var constraints standaloneProxyConstraints
	if strict {
		var document strictStaticProxyDocument
		if err := decoder.Decode(&document); err != nil {
			return nil, err
		}
		if err := requireJSONEOF(decoder); err != nil {
			return nil, err
		}
		if document.Constraints == nil {
			return nil, errors.New("missing field `constraints`")
		}
		network = document.Network
		constraints = *document.Constraints
	} else {
		var document staticProxyDocument
		if err := decoder.Decode(&document); err != nil {
			return nil, err
		}
		if err := requireJSONEOF(decoder); err != nil {
			return nil, err
		}
		network = document.Network
		if document.Constraints != nil {
			constraints = *document.Constraints
		}
	}
	settings, err := standaloneNetworkSettingsToProxySettings(network)
	if err != nil {
		return nil, err
	}
	resolved := &StaticNetworkProxyConfig{
		Network:     settings,
		Constraints: constraints.toProxyConstraints(),
	}
	resolved.Normalize()
	return resolved, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("network proxy config has trailing characters after the network object")
	}
	return nil
}

func standaloneNetworkSettingsToProxySettings(network standaloneNetworkSettings) (ProxySettings, error) {
	table, err := standaloneSettingsToConfigValues(network)
	if err != nil {
		return ProxySettings{}, err
	}
	settings := DefaultProxySettings()
	if err := applyProxySettingsTable(&settings, table, "network"); err != nil {
		return ProxySettings{}, err
	}
	return settings, nil
}

// Normalize applies Rust's static-config normalization: limited mode and
// configured hooks both require HTTPS MITM.
func (c *StaticNetworkProxyConfig) Normalize() {
	if c == nil {
		return
	}
	c.Network.MITM = c.Network.MITM || c.Network.Mode == ProxyModeLimited || len(c.Network.MITMHooks) > 0
}

// Validate checks the policy before listeners are created or credentials and CA
// material are loaded (Rust StaticNetworkProxyConfig::build_state).
func (c *StaticNetworkProxyConfig) Validate() error {
	if c == nil {
		return errors.New("missing static network proxy config")
	}
	c.Normalize()
	if !c.Network.Enabled {
		return errors.New("standalone network proxy requires network.enabled = true")
	}
	return ValidateProxyPolicyAgainstConstraints(ProxyConfig{Network: c.Network}, c.Constraints)
}

func (c standaloneProxyConstraints) toProxyConstraints() ProxyConstraints {
	out := ProxyConstraints{
		Enabled:                          c.Enabled,
		AllowUpstreamProxy:               c.AllowUpstreamProxy,
		DangerouslyAllowNonLoopbackProxy: c.DangerouslyAllowNonLoopbackProxy,
		DangerouslyAllowAllUnixSockets:   c.DangerouslyAllowAllUnixSockets,
		AllowedDomains:                   c.AllowedDomains,
		AllowlistExpansionEnabled:        c.AllowlistExpansionEnabled,
		DeniedDomains:                    c.DeniedDomains,
		DenylistExpansionEnabled:         c.DenylistExpansionEnabled,
		AllowUnixSockets:                 c.AllowUnixSockets,
		AllowLocalBinding:                c.AllowLocalBinding,
	}
	if c.Mode != nil {
		mode := ProxyMode(*c.Mode)
		out.Mode = &mode
	}
	return out
}
