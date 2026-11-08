package appserver

import (
	"fmt"
	"testing"

	"codex_go/session"
)

// The default router installs the platform seam at construction, so the
// initialize gate no longer depends on the nil-provider compatibility fallback.
func TestDefaultRuntimeRouterInstallsUserVerificationPlatformProvider(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	injected := router.services.UserVerificationProvider
	if injected == nil {
		t.Fatal("NewDefaultRuntimeRouter left UserVerificationProvider nil")
	}
	// Pin the concrete platform provider: the darwin/cgo build injects the
	// Secure Enclave provider, every other build injects the unsupported one.
	if got, want := fmt.Sprintf("%T", injected), fmt.Sprintf("%T", newUserVerificationPlatformProvider()); got != want {
		t.Fatalf("injected provider = %s, want %s", got, want)
	}
}

// Once a provider is injected it answers the device probe itself; the platform
// fallback must only serve hand-built RuntimeServices.
func TestUserVerificationDeviceSupportPrefersInjectedProvider(t *testing.T) {
	previous := userVerificationPlatformDeviceProbe
	userVerificationPlatformDeviceProbe = func() bool { return true }
	t.Cleanup(func() { userVerificationPlatformDeviceProbe = previous })

	if userVerificationDeviceSupported(noDeviceUserVerificationProvider{}) {
		t.Fatal("an injected provider without device support must win over the platform probe")
	}
	if !userVerificationDeviceSupported(deviceUserVerificationProvider{}) {
		t.Fatal("an injected provider advertising device support must be honoured")
	}
	if !userVerificationDeviceSupported(nil) {
		t.Fatal("a nil provider must still fall back to the platform probe")
	}
}

type noDeviceUserVerificationProvider struct {
	UnsupportedUserVerificationProvider
}

func (noDeviceUserVerificationProvider) DeviceSupported() bool { return false }

type deviceUserVerificationProvider struct {
	UnsupportedUserVerificationProvider
}

func (deviceUserVerificationProvider) DeviceSupported() bool { return true }
