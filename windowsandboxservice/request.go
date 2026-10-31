// Package windowsandboxservice carries the machine-wide Windows sandbox
// provisioning service (Rust crate windows-sandbox-service): the validated
// request surface, the authenticated named-pipe listener, provisioning under an
// impersonated owner and the package-lifecycle cleanup that runs it.
package windowsandboxservice

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

const (
	// MaxRequestBytes bounds one provisioning request read from the pipe.
	MaxRequestBytes = 4096
)

// RegisterInstallationRequest records desktop uninstall ownership without
// provisioning sandbox resources.
type RegisterInstallationRequest struct {
	CodexHome string
}

// ProvisioningRequest is a validated sandbox provisioning request. Listeners
// and settings arrive normalized: sorted, deduplicated and free of port 0.
type ProvisioningRequest struct {
	CodexHome      string
	RegisteredCore bool
	RefreshOnly    bool
	Settings       provisionservice.Settings
	Listeners      provisionservice.Listeners
}

// ServiceRequest is exactly one validated request frame.
type ServiceRequest struct {
	Register  *RegisterInstallationRequest
	Provision *ProvisioningRequest
}

// ValidateRequest ports Rust ipc::request::validate_request: one framed request
// within MaxRequestBytes, at the shared protocol version, with a normalized
// provisioning payload.
func ValidateRequest(request []byte) (*ServiceRequest, error) {
	if len(request) > MaxRequestBytes {
		return nil, errors.New("provisioning request exceeds size limit")
	}
	reader := bytes.NewReader(request)
	frame, err := provisionservice.ReadFrame(reader)
	if err != nil {
		return nil, fmt.Errorf("invalid framed provisioning request: %w", err)
	}
	if frame == nil {
		return nil, errors.New("provisioning client sent an empty request")
	}
	if reader.Len() != 0 {
		return nil, errors.New("provisioning requests must contain exactly one IPC frame")
	}
	if frame.Version != provisionservice.ProtocolVersion {
		return nil, fmt.Errorf("unsupported provisioning request version: %d", frame.Version)
	}
	switch frame.Message.Kind {
	case provisionservice.KindRegisterInstallationRequest:
		if err := validateHome(frame.Message.CodexHome); err != nil {
			return nil, err
		}
		return &ServiceRequest{Register: &RegisterInstallationRequest{CodexHome: frame.Message.CodexHome}}, nil
	case provisionservice.KindProvisionSandboxRequest:
		if frame.Message.Request == nil {
			return nil, errors.New("provision_sandbox_request requires a payload")
		}
	case provisionservice.KindProvisionSandboxResponse:
		return nil, errors.New("expected a service request")
	default:
		return nil, fmt.Errorf("unknown provisioning message %q", frame.Message.Kind)
	}
	payload := *frame.Message.Request
	if err := validateHome(payload.CodexHome); err != nil {
		return nil, err
	}
	if payload.RefreshOnly && !payload.RegisteredCore {
		return nil, errors.New("registration refresh requires registered Core")
	}
	settings, listeners := payload.Settings, payload.Listeners
	for _, ports := range [][]uint16{settings.ProxyPorts, listeners.HTTPPorts, listeners.SOCKSPorts} {
		if containsPortValue(ports, 0) {
			return nil, errors.New("provisioning request includes an invalid proxy port")
		}
	}
	settings.ProxyPorts = sortedPorts(settings.ProxyPorts)
	listeners.HTTPPorts = sortedPorts(listeners.HTTPPorts)
	listeners.SOCKSPorts = sortedPorts(listeners.SOCKSPorts)
	for _, port := range append(append([]uint16{}, listeners.HTTPPorts...), listeners.SOCKSPorts...) {
		if !containsPortValue(settings.ProxyPorts, port) {
			return nil, errors.New("provisioning listener is absent from the proxy settings")
		}
	}
	return &ServiceRequest{Provision: &ProvisioningRequest{
		CodexHome:      payload.CodexHome,
		RegisteredCore: payload.RegisteredCore,
		RefreshOnly:    payload.RefreshOnly,
		Settings:       settings,
		Listeners:      listeners,
	}}, nil
}

// validateHome mirrors Rust validate_home; the service later resolves the path
// under the authenticated owner's token.
func validateHome(home string) error {
	if home == "" || strings.ContainsAny(home, "\x00\r\n") {
		return errors.New("Codex home is empty or contains an invalid control character")
	}
	return nil
}

func containsPortValue(ports []uint16, port uint16) bool {
	for _, candidate := range ports {
		if candidate == port {
			return true
		}
	}
	return false
}

// sortedPorts sorts and deduplicates a port list like the Rust request
// normalization.
func sortedPorts(ports []uint16) []uint16 {
	out := append([]uint16(nil), ports...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	deduped := out[:0]
	for i, port := range out {
		if i == 0 || port != out[i-1] {
			deduped = append(deduped, port)
		}
	}
	if deduped == nil {
		return []uint16{}
	}
	return deduped
}
