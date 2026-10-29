// Package mxc implements the portable policy and launcher transport used by
// Codex's native Windows MXC sandbox.
package mxc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// LauncherArg1 is dispatched by the Codex executable before ordinary CLI
// parsing (Rust CODEX_WINDOWS_MXC_ARG1).
const LauncherArg1 = "--__codex-windows-mxc"

// ClientOnlyLoopbackUnsupported mirrors the Rust diagnostic for a managed
// network policy MXC cannot represent.
const ClientOnlyLoopbackUnsupported = "MXC cannot enforce managed networking with allow_local_binding=false: native host-loopback access is bidirectional"

// ManagedNetworkContext is the portable managed-network launch context carried
// by the MXC request.
type ManagedNetworkContext struct {
	LoopbackPorts     []uint16 `json:"loopbackPorts"`
	AllowLocalBinding bool     `json:"allowLocalBinding"`
}

// Request is the typed launcher payload. Permissions remains raw so the helper
// receives the canonical Rust-compatible permission profile rather than Go's
// internal field names.
type Request struct {
	Permissions      json.RawMessage        `json:"permissions"`
	SandboxPolicyCWD string                 `json:"sandbox_policy_cwd"`
	ManagedNetwork   *ManagedNetworkContext `json:"managed_network,omitempty"`
	Command          []string               `json:"command"`
	DefaultChildEnv  bool                   `json:"default_child_env,omitempty"`
}

// ValidateManagedNetwork mirrors Rust's launcher and policy boundary checks.
func ValidateManagedNetwork(network *ManagedNetworkContext) error {
	if network == nil {
		return nil
	}
	if len(network.LoopbackPorts) == 0 {
		return errors.New("MXC managed networking requires dedicated proxy ports")
	}
	for _, port := range network.LoopbackPorts {
		if port == 0 {
			return errors.New("MXC proxy ports must be nonzero")
		}
	}
	if !network.AllowLocalBinding {
		return errors.New(ClientOnlyLoopbackUnsupported)
	}
	return nil
}

// CreateCommandArgs wraps an exact argv and adds bounded launcher-only
// environment variables. The helper removes them before starting the workload.
func CreateCommandArgs(
	command []string,
	profile sandbox.PermissionProfile,
	sandboxPolicyCWD string,
	managedNetwork *ManagedNetworkContext,
	env map[string]string,
) ([]string, error) {
	if len(command) == 0 {
		return nil, errors.New("MXC command must not be empty")
	}
	if !filepath.IsAbs(sandboxPolicyCWD) {
		return nil, errors.New("MXC requires an absolute policy working directory")
	}
	if !utf8.ValidString(sandboxPolicyCWD) {
		return nil, errors.New("MXC requires a Unicode policy working directory")
	}
	if err := ValidateManagedNetwork(managedNetwork); err != nil {
		return nil, err
	}
	if env == nil {
		return nil, errors.New("MXC launch environment is required")
	}
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		return nil, fmt.Errorf("encode MXC permission profile: %w", err)
	}
	defaultChildEnv := true
	for key := range env {
		if !windowssandbox.IsSandboxLaunchEnvKey(key) {
			defaultChildEnv = false
			break
		}
	}
	request := Request{
		Permissions:      json.RawMessage(profileJSON),
		SandboxPolicyCWD: sandboxPolicyCWD,
		ManagedNetwork:   managedNetwork,
		Command:          append([]string(nil), command...),
		DefaultChildEnv:  defaultChildEnv,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	// SDK profile defaults depend on the launcher's SystemRoot. These values
	// are host-owned and cannot be overridden by the caller.
	reserved := []string{"SystemDrive", "LOCALAPPDATA"}
	if defaultChildEnv {
		reserved = append(reserved, "SystemRoot")
	}
	for _, key := range reserved {
		value, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("MXC requires nonempty Unicode host %s", key)
		}
		for existing := range env {
			if strings.EqualFold(existing, key) {
				delete(env, existing)
			}
		}
		env[key] = value
	}
	if err := windowssandbox.EncodeSandboxLaunchPayload(string(payload), env); err != nil {
		return nil, err
	}
	return []string{LauncherArg1}, nil
}

// DecodeCommandArgs reconstructs and scrubs a launcher request.
func DecodeCommandArgs(env map[string]string) (*Request, error) {
	payload, err := windowssandbox.DecodeAndScrubSandboxLaunchPayload(env)
	if err != nil {
		// Transport and serde errors can quote arbitrary payload values; the
		// helper reports a single stable diagnostic instead (Rust #44903).
		return nil, errors.New("invalid MXC launcher request")
	}
	var request Request
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return nil, errors.New("invalid MXC launcher request")
	}
	if request.DefaultChildEnv {
		for key := range env {
			delete(env, key)
		}
	}
	return &request, nil
}
