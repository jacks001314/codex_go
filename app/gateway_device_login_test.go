package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
)

type fakeGatewayDeviceLoginBackend struct {
	state       *appserver.GatewayOAuthReadResponse
	readErr     error
	legacy      bool
	login       *appserver.GatewayOAuthLoginResponse
	loginErr    error
	waitErr     error
	cancelErr   error
	readCalls   int
	loginCalls  int
	waitLoginID string
	cancelCalls int
	closed      bool
}

func (b *fakeGatewayDeviceLoginBackend) readGatewayOAuth(context.Context, string) (*appserver.GatewayOAuthReadResponse, error) {
	b.readCalls++
	return b.state, b.readErr
}

func (b *fakeGatewayDeviceLoginBackend) probeLegacyGatewayOAuth(context.Context) (bool, error) {
	return b.legacy, nil
}

func (b *fakeGatewayDeviceLoginBackend) loginGatewayDevice(context.Context, string) (*appserver.GatewayOAuthLoginResponse, error) {
	b.loginCalls++
	return b.login, b.loginErr
}

func (b *fakeGatewayDeviceLoginBackend) waitGatewayOAuth(_ context.Context, loginID string) error {
	b.waitLoginID = loginID
	return b.waitErr
}

func (b *fakeGatewayDeviceLoginBackend) cancelGatewayOAuth(context.Context) error {
	b.cancelCalls++
	return b.cancelErr
}

func (b *fakeGatewayDeviceLoginBackend) close() { b.closed = true }

func gatewayStatus(status appserver.GatewayOAuthStatus) *appserver.GatewayOAuthStatus {
	return &status
}

// Mirrors Rust #60662 device_login::needs_device_login: only a required
// gateway with device support whose credential is missing or failed needs the
// startup prompt, and only when this host cannot reach a browser.
func TestNeedsGatewayDeviceLoginLikeRust(t *testing.T) {
	states := []struct {
		name         string
		state        *appserver.GatewayOAuthReadResponse
		preferDevice bool
		want         bool
	}{
		{name: "nil state", preferDevice: true},
		{name: "device prompt not preferred", state: &appserver.GatewayOAuthReadResponse{Required: true, SupportsDeviceAuth: true, Status: gatewayStatus(appserver.GatewayOAuthStatusNotReady)}},
		{name: "not required", state: &appserver.GatewayOAuthReadResponse{SupportsDeviceAuth: true, Status: gatewayStatus(appserver.GatewayOAuthStatusNotReady)}, preferDevice: true},
		{name: "no device auth", state: &appserver.GatewayOAuthReadResponse{Required: true, Status: gatewayStatus(appserver.GatewayOAuthStatusNotReady)}, preferDevice: true},
		{name: "no status", state: &appserver.GatewayOAuthReadResponse{Required: true, SupportsDeviceAuth: true}, preferDevice: true},
		{name: "started", state: &appserver.GatewayOAuthReadResponse{Required: true, SupportsDeviceAuth: true, Status: gatewayStatus(appserver.GatewayOAuthStatusStarted)}, preferDevice: true},
		{name: "succeeded", state: &appserver.GatewayOAuthReadResponse{Required: true, SupportsDeviceAuth: true, Status: gatewayStatus(appserver.GatewayOAuthStatusSucceeded)}, preferDevice: true},
		{name: "not ready", state: &appserver.GatewayOAuthReadResponse{Required: true, SupportsDeviceAuth: true, Status: gatewayStatus(appserver.GatewayOAuthStatusNotReady)}, preferDevice: true, want: true},
		{name: "failed", state: &appserver.GatewayOAuthReadResponse{Required: true, SupportsDeviceAuth: true, Status: gatewayStatus(appserver.GatewayOAuthStatusFailed)}, preferDevice: true, want: true},
	}
	for _, tc := range states {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsGatewayDeviceLogin(tc.state, tc.preferDevice); got != tc.want {
				t.Fatalf("needsGatewayDeviceLogin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunGatewayDeviceLoginPromptsAndWaitsLikeRust(t *testing.T) {
	backend := &fakeGatewayDeviceLoginBackend{
		state: &appserver.GatewayOAuthReadResponse{
			ProviderID:         "gateway",
			Required:           true,
			SupportsDeviceAuth: true,
			Status:             gatewayStatus(appserver.GatewayOAuthStatusNotReady),
		},
		login: &appserver.GatewayOAuthLoginResponse{Prompt: &appserver.GatewayOAuthPrompt{
			LoginID:         "login-1",
			VerificationURL: "https://example.test/device",
			UserCode:        "ABCD-EFGH",
		}},
	}
	var stdout bytes.Buffer
	if err := runGatewayDeviceLogin(context.Background(), backend, "", true, &stdout); err != nil {
		t.Fatalf("runGatewayDeviceLogin() error = %v", err)
	}
	if backend.loginCalls != 1 || backend.waitLoginID != "login-1" {
		t.Fatalf("login/wait calls = %d/%q", backend.loginCalls, backend.waitLoginID)
	}
	for _, want := range []string{"Sign in to your gateway", "https://example.test/device", "ABCD-EFGH"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("prompt missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunGatewayDeviceLoginSkipsWhenNotNeededLikeRust(t *testing.T) {
	backend := &fakeGatewayDeviceLoginBackend{state: &appserver.GatewayOAuthReadResponse{
		ProviderID:         "gateway",
		Required:           true,
		SupportsDeviceAuth: true,
		Status:             gatewayStatus(appserver.GatewayOAuthStatusSucceeded),
	}}
	if err := runGatewayDeviceLogin(context.Background(), backend, "other", true, nil); err != nil {
		t.Fatalf("runGatewayDeviceLogin() error = %v", err)
	}
	if backend.loginCalls != 0 {
		t.Fatalf("login calls = %d, want 0", backend.loginCalls)
	}
}

// Mirrors Rust's provider check: a session pinned to another provider never
// starts the current gateway's device prompt.
func TestRunGatewayDeviceLoginSkipsProviderMismatchLikeRust(t *testing.T) {
	backend := &fakeGatewayDeviceLoginBackend{state: &appserver.GatewayOAuthReadResponse{
		ProviderID:         "gateway",
		Required:           true,
		SupportsDeviceAuth: true,
		Status:             gatewayStatus(appserver.GatewayOAuthStatusNotReady),
	}}
	if err := runGatewayDeviceLogin(context.Background(), backend, "bedrock", true, nil); err != nil {
		t.Fatalf("runGatewayDeviceLogin() error = %v", err)
	}
	if backend.loginCalls != 0 {
		t.Fatalf("login calls = %d, want 0", backend.loginCalls)
	}
}

// An older app-server without the device RPCs skips the prompt instead of
// failing startup.
func TestRunGatewayDeviceLoginSkipsLegacyServerLikeRust(t *testing.T) {
	backend := &fakeGatewayDeviceLoginBackend{
		readErr: &gatewayDeviceLoginRPCError{code: -32601, message: "Method not found"},
	}
	if err := runGatewayDeviceLogin(context.Background(), backend, "", true, nil); err != nil {
		t.Fatalf("runGatewayDeviceLogin() error = %v", err)
	}
	legacy := &fakeGatewayDeviceLoginBackend{readErr: errors.New("unexpected"), legacy: true}
	if err := runGatewayDeviceLogin(context.Background(), legacy, "", true, nil); err != nil {
		t.Fatalf("legacy probe error = %v", err)
	}
}

func TestRunGatewayDeviceLoginCancellationLikeRust(t *testing.T) {
	backend := &fakeGatewayDeviceLoginBackend{
		state: &appserver.GatewayOAuthReadResponse{
			ProviderID:         "gateway",
			Required:           true,
			SupportsDeviceAuth: true,
			Status:             gatewayStatus(appserver.GatewayOAuthStatusNotReady),
		},
		login:   &appserver.GatewayOAuthLoginResponse{Prompt: &appserver.GatewayOAuthPrompt{LoginID: "login-1"}},
		waitErr: context.Canceled,
	}
	err := runGatewayDeviceLogin(context.Background(), backend, "", true, nil)
	if err == nil || err.Error() != gatewayDeviceLoginCancelled {
		t.Fatalf("cancellation error = %v, want %q", err, gatewayDeviceLoginCancelled)
	}
	if backend.cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", backend.cancelCalls)
	}
}
