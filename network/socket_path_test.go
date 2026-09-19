package network

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// Mirrors Rust network-proxy config.rs::resolve_runtime_validates_allow_unix_sockets_for_executor_os
// (#46302/#46334): a controller and its executor can run different operating
// systems, so allowance is decided by the executor's path grammar.
func TestSocketPathValidationUsesExecutorPlatformLikeRust(t *testing.T) {
	for _, tc := range []struct {
		path            string
		unixAbsolute    bool
		windowsAbsolute bool
	}{
		{"relative.sock", false, false},
		{"~/example.sock", false, false},
		{"/tmp/example.sock", true, true},
		{`C:\example.sock`, false, true},
		{"C:/example.sock", false, true},
		{`\\server\share\example.sock`, false, true},
		{`\\?\C:\example.sock`, false, true},
		{`\\.\pipe\example`, false, true},
		{"C:example.sock", false, false},
		{`\example.sock`, false, false},
		{`\\server`, false, false},
		{"/tmp/\x00example.sock", false, false},
		{"C:\\example\x00.sock", false, false},
	} {
		for _, pc := range []struct {
			platform utils.Platform
			want     bool
		}{
			{utils.PlatformLinux, tc.unixAbsolute},
			{utils.PlatformMacos, tc.unixAbsolute},
			{utils.PlatformWindows, tc.windowsAbsolute},
			{utils.PlatformUnknown, tc.unixAbsolute || tc.windowsAbsolute},
		} {
			settings := DefaultProxySettings()
			settings.SetAllowUnixSockets([]string{tc.path})
			_, err := ResolveProxyRuntimeForPlatform(ProxyConfig{Network: settings}, pc.platform)
			if accepted := err == nil; accepted != pc.want {
				t.Fatalf("ResolveProxyRuntimeForPlatform(%s, %q) accepted=%v, want %v (err=%v)", pc.platform, tc.path, accepted, pc.want, err)
			}
			if !pc.want && err != nil && !strings.Contains(err.Error(), "expected a NUL-free absolute path for "+pc.platform.String()) {
				t.Fatalf("ResolveProxyRuntimeForPlatform(%s, %q) error = %v", pc.platform, tc.path, err)
			}
		}
	}
}

// Mirrors Rust network-proxy state.rs::build_config_state, which validates the
// composed config's socket allowlist against the executor OS.
func TestNewProxySpecValidatesSocketAllowlistForExecutorOSLikeRust(t *testing.T) {
	for _, tc := range []struct {
		path     string
		platform utils.Platform
		want     bool
	}{
		{`C:\example.sock`, utils.PlatformWindows, true},
		{`C:\example.sock`, utils.PlatformLinux, false},
		{`C:\example.sock`, utils.PlatformUnknown, true},
		{"/var/run/example.sock", utils.PlatformLinux, true},
		{"relative.sock", utils.PlatformWindows, false},
		{"relative.sock", utils.PlatformUnknown, false},
	} {
		settings := DefaultProxySettings()
		settings.SetAllowUnixSockets([]string{tc.path})
		_, err := NewProxySpecForPlatform(ProxyConfig{Network: settings}, nil, false, tc.platform)
		if accepted := err == nil; accepted != tc.want {
			t.Fatalf("NewProxySpecForPlatform(%s, %q) accepted=%v, want %v (err=%v)", tc.platform, tc.path, accepted, tc.want, err)
		}
	}
	// Requirements-only socket entries join the same validation.
	settings := DefaultProxySettings()
	_, err := NewProxySpecForPlatform(ProxyConfig{Network: settings}, &ProxyRequirements{
		UnixSockets: &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{
			`C:\managed.sock`: ProxyUnixSocketAllow,
		}},
	}, false, utils.PlatformWindows)
	if err != nil {
		t.Fatalf("managed Windows socket entry should be accepted for a Windows executor: %v", err)
	}
}

// Mirrors Rust network-proxy config.rs::resolve_runtime_accepts_unix_style_absolute_allow_unix_sockets_entries:
// deny entries are preserved unchanged (never validated), and unix-style
// absolute allow entries are accepted on every executor platform.
func TestUnixSocketAllowlistAcceptsUnixStyleAbsoluteAndLeavesDenyUnchangedLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowUnixSockets([]string{
		"/private/tmp/example.sock",
		"/tmp/../example.sock",
		`/tmp/name\part.sock`,
	})
	if settings.UnixSockets == nil {
		t.Fatal("expected allow entries to be recorded")
	}
	for _, path := range []string{"relative.sock", "~/example.sock", `C:\example.sock`, "\x00"} {
		settings.UnixSockets.Entries[path] = ProxyUnixSocketDeny
	}
	for _, platform := range []utils.Platform{
		utils.PlatformLinux,
		utils.PlatformMacos,
		utils.PlatformWindows,
		utils.PlatformUnknown,
	} {
		if _, err := ResolveProxyRuntimeForPlatform(ProxyConfig{Network: settings}, platform); err != nil {
			t.Fatalf("unix-style absolute allow entries should be accepted for %s: %v", platform, err)
		}
	}
	if len(settings.AllowUnixSockets()) != 3 {
		t.Fatalf("deny entries must not join the allowlist: %#v", settings.AllowUnixSockets())
	}
}

// Mirrors Rust socket_path.rs: Unix-style absolute paths stay valid on Windows
// for portability, while the Windows grammar is only consulted when the
// executor platform can use it (Windows or unknown metadata).
func TestSocketPathIsAbsoluteLikeRust(t *testing.T) {
	for _, tc := range []struct {
		path     string
		platform utils.Platform
		want     bool
	}{
		{"/tmp/example.sock", utils.PlatformLinux, true},
		{"/tmp/example.sock", utils.PlatformWindows, true},
		{`C:\example.sock`, utils.PlatformLinux, false},
		{`C:\example.sock`, utils.PlatformWindows, true},
		{`C:\example.sock`, utils.PlatformUnknown, true},
		{`relative.sock`, utils.PlatformUnknown, false},
		{`\\server\share\s.sock`, utils.PlatformUnknown, true},
		{`\\server`, utils.PlatformUnknown, false},
		{`\\server\`, utils.PlatformUnknown, false},
	} {
		if got := SocketPathIsAbsolute(tc.platform, tc.path); got != tc.want {
			t.Fatalf("SocketPathIsAbsolute(%s, %q) = %v, want %v", tc.platform, tc.path, got, tc.want)
		}
	}
}
