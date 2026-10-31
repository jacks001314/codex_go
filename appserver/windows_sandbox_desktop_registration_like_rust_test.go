package appserver

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"codex_go/config"
)

// TestInitializeRegistersDesktopUninstallOwnershipLikeRust pins Rust
// initialize_processor: only the local Codex Desktop stdio host records desktop
// uninstall ownership during initialize, and a failed registration must not
// fail the request.
func TestInitializeRegistersDesktopUninstallOwnershipLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop uninstall ownership is Windows-only")
	}
	previous := registerDesktopWindowsUninstallOwnership
	t.Cleanup(func() { registerDesktopWindowsUninstallOwnership = previous })

	cases := []struct {
		name      string
		transport string
		client    string
		wantHome  bool
	}{
		{"desktop stdio host", "stdio", "Codex Desktop", true},
		{"other client on stdio", "stdio", "codex-tui", false},
		{"desktop on another transport", "remote_control", "Codex Desktop", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			var registeredHome string
			registerDesktopWindowsUninstallOwnership = func(codexHome string) error {
				registeredHome = codexHome
				return nil
			}
			router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
			router.SetRequestTransport(tc.transport)
			response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
				ClientInfo: ClientInfo{Name: tc.client, Version: "1.0.0"},
			}))
			if response.Error != nil {
				t.Fatalf("initialize = %+v", response)
			}
			if tc.wantHome && registeredHome != home {
				t.Fatalf("registered home = %q, want %q", registeredHome, home)
			}
			if !tc.wantHome && registeredHome != "" {
				t.Fatalf("unexpected registration for %q on %q", tc.client, tc.transport)
			}
		})
	}
}

// TestInitializeKeepsTheResponseWhenDesktopRegistrationFailsLikeRust pins the
// Rust warning-only failure path: registration is bounded and its error never
// reaches the initialize response.
func TestInitializeKeepsTheResponseWhenDesktopRegistrationFailsLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop uninstall ownership is Windows-only")
	}
	previous := registerDesktopWindowsUninstallOwnership
	t.Cleanup(func() { registerDesktopWindowsUninstallOwnership = previous })
	attempts := 0
	registerDesktopWindowsUninstallOwnership = func(string) error {
		attempts++
		return errors.New("service unavailable")
	}
	home := t.TempDir()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("create home: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	router.SetRequestTransport("stdio")
	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "Codex Desktop", Version: "1.0.0"},
	}))
	if response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}
	if attempts != 1 {
		t.Fatalf("registration attempts = %d, want 1", attempts)
	}
}
