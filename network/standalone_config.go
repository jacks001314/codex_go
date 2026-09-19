package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxStandaloneProxyConfigBytes bounds the standalone network-proxy config file
// before it is decoded (Rust MAX_CONFIG_BYTES).
const MaxStandaloneProxyConfigBytes = 1024 * 1024

// standaloneProxyConfig is the top-level `--config` document: a single
// `network` object (Rust StandaloneConfig).
type standaloneProxyConfig struct {
	Network standaloneNetworkSettings `json:"network"`
}

// standaloneNetworkSettings mirrors the Rust NetworkProxyConfig JSON shape.
// Pointer fields keep the "absent" state so the shared settings table applies
// the same defaults as the TOML path.
type standaloneNetworkSettings struct {
	Enabled                                      *bool                      `json:"enabled,omitempty"`
	ProxyURL                                     *string                    `json:"proxy_url,omitempty"`
	EnableSocks5                                 *bool                      `json:"enable_socks5,omitempty"`
	SocksURL                                     *string                    `json:"socks_url,omitempty"`
	EnableSocks5UDP                              *bool                      `json:"enable_socks5_udp,omitempty"`
	AllowUpstreamProxy                           *bool                      `json:"allow_upstream_proxy,omitempty"`
	DangerouslyAllowNonLoopbackProxy             *bool                      `json:"dangerously_allow_non_loopback_proxy,omitempty"`
	DangerouslyAllowAllUnixSockets               *bool                      `json:"dangerously_allow_all_unix_sockets,omitempty"`
	Mode                                         *string                    `json:"mode,omitempty"`
	Domains                                      map[string]string          `json:"domains,omitempty"`
	UnixSockets                                  map[string]string          `json:"unix_sockets,omitempty"`
	AllowLocalBinding                            *bool                      `json:"allow_local_binding,omitempty"`
	MITM                                         *bool                      `json:"mitm,omitempty"`
	CredentialBroker                             *bool                      `json:"credential_broker,omitempty"`
	CredentialProviders                          map[string]json.RawMessage `json:"credential_providers,omitempty"`
	DangerouslyAllowPlaintextCredentialInjection *bool                      `json:"dangerously_allow_plaintext_credential_injection,omitempty"`
	MITMHooks                                    []standaloneMITMHook       `json:"mitm_hooks,omitempty"`
	AllowedDomains                               []string                   `json:"allowed_domains,omitempty"`
	DeniedDomains                                []string                   `json:"denied_domains,omitempty"`
	AllowUnixSockets                             []string                   `json:"allow_unix_sockets,omitempty"`
}

// standaloneMITMHook mirrors Rust MitmHookConfig. The nested structs make
// encoding/json reject unknown fields at every hook level.
type standaloneMITMHook struct {
	Host    string                     `json:"host"`
	Match   *standaloneMITMHookMatch   `json:"match,omitempty"`
	Actions *standaloneMITMHookActions `json:"actions,omitempty"`
}

type standaloneMITMHookMatch struct {
	Methods      []string            `json:"methods,omitempty"`
	PathPrefixes []string            `json:"path_prefixes,omitempty"`
	Query        map[string][]string `json:"query,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         json.RawMessage     `json:"body,omitempty"`
}

type standaloneMITMHookActions struct {
	StripRequestHeaders  []string                   `json:"strip_request_headers,omitempty"`
	InjectRequestHeaders []standaloneInjectedHeader `json:"inject_request_headers,omitempty"`
}

type standaloneInjectedHeader struct {
	Name         string  `json:"name"`
	SecretEnvVar *string `json:"secret_env_var,omitempty"`
	SecretFile   *string `json:"secret_file,omitempty"`
	Prefix       *string `json:"prefix,omitempty"`
}

// ParseStandaloneProxyConfig parses the standalone `codex-network-proxy`
// configuration (Rust #46573): a single `network` object, at most 1 MiB, with
// unknown fields rejected at every level (including nested MITM hooks) and no
// trailing JSON values. HTTPS MITM is enabled automatically for limited mode or
// configured hooks, and `network.enabled` must be true.
func ParseStandaloneProxyConfig(data []byte) (*ProxyConfig, error) {
	if len(data) > MaxStandaloneProxyConfigBytes {
		return nil, fmt.Errorf("network proxy config exceeds %d bytes", MaxStandaloneProxyConfigBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config standaloneProxyConfig
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("network proxy config has trailing characters after the network object")
	}

	table, err := standaloneSettingsToConfigValues(config.Network)
	if err != nil {
		return nil, err
	}
	settings := DefaultProxySettings()
	if err := applyProxySettingsTable(&settings, table, "network"); err != nil {
		return nil, err
	}
	// Rust NetworkProxyConfig::from_config: limited mode and configured hooks
	// both require HTTPS MITM.
	settings.MITM = settings.MITM || settings.Mode == ProxyModeLimited || len(settings.MITMHooks) > 0
	if !settings.Enabled {
		return nil, errors.New("standalone network proxy requires network.enabled = true")
	}
	// Policy validation runs when the proxy starts (Rust's build_config_state in
	// main), matching the parse step that this function mirrors.
	return &ProxyConfig{Network: settings}, nil
}

// standaloneSettingsToConfigValues re-encodes the strict settings struct into
// the generic map the shared config-table parser consumes.
func standaloneSettingsToConfigValues(settings standaloneNetworkSettings) (map[string]any, error) {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	table := map[string]any{}
	if err := json.Unmarshal(encoded, &table); err != nil {
		return nil, err
	}
	return table, nil
}
