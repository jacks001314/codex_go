package windowsandboxservice

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

func requestFrame(t *testing.T, frame *provisionservice.Frame) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := provisionservice.WriteFrame(&buffer, frame); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	return buffer.Bytes()
}

func provisioningFrame(home string, registeredCore bool, refreshOnly bool, settings provisionservice.Settings, listeners provisionservice.Listeners) *provisionservice.Frame {
	return &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{
			Kind: provisionservice.KindProvisionSandboxRequest,
			Request: &provisionservice.ProvisionRequest{
				CodexHome:      home,
				RegisteredCore: registeredCore,
				RefreshOnly:    refreshOnly,
				Settings:       settings,
				Listeners:      listeners,
			},
		},
	}
}

// TestValidateRequestRefusesMalformedRequestsLikeRust pins Rust
// ipc::request::validate_request: one bounded frame at the shared version with
// a normalized provisioning payload.
func TestValidateRequestRefusesMalformedRequestsLikeRust(t *testing.T) {
	register := requestFrame(t, &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{Kind: provisionservice.KindRegisterInstallationRequest, CodexHome: `C:\Users\owner\.codex`},
	})
	response := requestFrame(t, &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{
			Kind:     provisionservice.KindProvisionSandboxResponse,
			Response: &provisionservice.Response{Kind: provisionservice.ResponseOK},
		},
	})
	badVersion := requestFrame(t, &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion + 1,
		Message: provisionservice.Message{Kind: provisionservice.KindRegisterInstallationRequest, CodexHome: `C:\home`},
	})
	emptyHome := requestFrame(t, &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{Kind: provisionservice.KindRegisterInstallationRequest, CodexHome: ""},
	})
	controlHome := requestFrame(t, &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{Kind: provisionservice.KindRegisterInstallationRequest, CodexHome: "C:\\home\n"},
	})
	refreshWithoutCore := requestFrame(t, provisioningFrame(`C:\home`, false, true, provisionservice.Settings{}, provisionservice.Listeners{}))
	zeroPort := requestFrame(t, provisioningFrame(`C:\home`, false, false,
		provisionservice.Settings{ProxyPorts: []uint16{0}}, provisionservice.Listeners{}))
	absentListener := requestFrame(t, provisioningFrame(`C:\home`, false, false,
		provisionservice.Settings{ProxyPorts: []uint16{8080}},
		provisionservice.Listeners{HTTPPorts: []uint16{8080}, SOCKSPorts: []uint16{1081}}))
	cases := []struct {
		name    string
		request []byte
		want    string
	}{
		{"oversized", make([]byte, MaxRequestBytes+1), "exceeds size limit"},
		{"empty", nil, "empty request"},
		{"two frames", append(append([]byte{}, register...), register...), "exactly one IPC frame"},
		{"unsupported version", badVersion, "unsupported provisioning request version"},
		{"empty home", emptyHome, "invalid control character"},
		{"control character home", controlHome, "invalid control character"},
		{"response message", response, "expected a service request"},
		{"refresh without registered Core", refreshWithoutCore, "registration refresh requires registered Core"},
		{"zero port", zeroPort, "invalid proxy port"},
		{"listener absent from settings", absentListener, "listener is absent from the proxy settings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateRequest(tc.request); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateRequest() error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestValidateRequestNormalizesProvisioningPayloadsLikeRust pins the sorted,
// deduplicated port lists the service provisions from.
func TestValidateRequestNormalizesProvisioningPayloadsLikeRust(t *testing.T) {
	frame := provisioningFrame(`C:\home`, true, true,
		provisionservice.Settings{ProxyPorts: []uint16{8080, 1081, 8080}, AllowLocalBinding: true},
		provisionservice.Listeners{HTTPPorts: []uint16{8080, 8080}, SOCKSPorts: []uint16{1081}})
	request, err := ValidateRequest(requestFrame(t, frame))
	if err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
	if request.Provision == nil || request.Register != nil {
		t.Fatalf("request = %#v", request)
	}
	if request.Provision.CodexHome != `C:\home` || !request.Provision.RegisteredCore || !request.Provision.RefreshOnly {
		t.Fatalf("provisioning request = %#v", request.Provision)
	}
	if got, want := request.Provision.Settings.ProxyPorts, []uint16{1081, 8080}; !reflect.DeepEqual(got, want) {
		t.Fatalf("proxy ports = %v, want %v", got, want)
	}
	if !request.Provision.Settings.AllowLocalBinding {
		t.Fatal("local binding permission was dropped")
	}
	if got, want := request.Provision.Listeners.HTTPPorts, []uint16{8080}; !reflect.DeepEqual(got, want) {
		t.Fatalf("http ports = %v, want %v", got, want)
	}
	if got, want := request.Provision.Listeners.SOCKSPorts, []uint16{1081}; !reflect.DeepEqual(got, want) {
		t.Fatalf("socks ports = %v, want %v", got, want)
	}
}

// TestValidateRequestAcceptsRegisterInstallationLikeRust pins the uninstall
// ownership request, which never provisions a sandbox.
func TestValidateRequestAcceptsRegisterInstallationLikeRust(t *testing.T) {
	home := `C:\Users\owner\.codex`
	request, err := ValidateRequest(requestFrame(t, &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{Kind: provisionservice.KindRegisterInstallationRequest, CodexHome: home},
	}))
	if err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
	if request.Register == nil || request.Register.CodexHome != home || request.Provision != nil {
		t.Fatalf("request = %#v", request)
	}
}
