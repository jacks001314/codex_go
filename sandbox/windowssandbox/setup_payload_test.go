package windowssandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestLoopbackProxyPortFromURL(t *testing.T) {
	cases := []struct {
		url  string
		port uint16
		ok   bool
	}{
		{"http://localhost:3128", 3128, true},
		{"https://127.0.0.1:8080", 8080, true},
		{"socks5h://user:pass@[::1]:1080", 1080, true},
		{"http://example.com:3128", 0, false},
		{"http://127.0.0.1:0", 0, false},
		{"localhost:8080", 0, false},
	}
	for _, tc := range cases {
		port, ok := LoopbackProxyPortFromURL(tc.url)
		if port != tc.port || ok != tc.ok {
			t.Fatalf("LoopbackProxyPortFromURL(%q) = (%d, %v), want (%d, %v)", tc.url, port, ok, tc.port, tc.ok)
		}
	}
}

func TestProxyPortsFromEnvDedupesAndSorts(t *testing.T) {
	env := map[string]string{
		"HTTP_PROXY":  "http://127.0.0.1:8080",
		"HTTPS_PROXY": "http://localhost:8080",
		"ALL_PROXY":   "socks5h://[::1]:1081",
		"NO_PROXY":    "http://127.0.0.1:9000",
	}
	got := ProxyPortsFromEnv(env)
	want := []uint16{1081, 8080}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProxyPortsFromEnv() = %#v, want %#v", got, want)
	}
}

func TestOfflineProxySettingsFromEnv(t *testing.T) {
	env := map[string]string{
		"HTTP_PROXY":                        "http://127.0.0.1:8080",
		"CODEX_NETWORK_ALLOW_LOCAL_BINDING": "1",
	}
	offline := OfflineProxySettingsFromEnv(env, SandboxNetworkIdentityOffline)
	if !reflect.DeepEqual(offline.ProxyPorts, []uint16{8080}) || !offline.AllowLocalBinding {
		t.Fatalf("offline settings = %#v", offline)
	}
	online := OfflineProxySettingsFromEnv(env, SandboxNetworkIdentityOnline)
	if len(online.ProxyPorts) != 0 || online.AllowLocalBinding {
		t.Fatalf("online settings = %#v", online)
	}
}

func TestSetupMarkerRequestMismatchReason(t *testing.T) {
	marker := &SetupMarker{Version: SetupVersion, ProxyPorts: []uint16{8080}, AllowLocalBinding: false}
	if reason := marker.RequestMismatchReason(SandboxNetworkIdentityOnline, OfflineProxySettings{ProxyPorts: []uint16{9090}}); reason != "" {
		t.Fatalf("online reason = %q, want empty", reason)
	}
	if reason := marker.RequestMismatchReason(SandboxNetworkIdentityOffline, OfflineProxySettings{ProxyPorts: []uint16{8080}}); reason != "" {
		t.Fatalf("matching reason = %q, want empty", reason)
	}
	if reason := marker.RequestMismatchReason(SandboxNetworkIdentityOffline, OfflineProxySettings{ProxyPorts: []uint16{9090}}); reason == "" {
		t.Fatalf("mismatch reason is empty")
	}
}

func TestProfileReadRootsExcludesConfiguredTopLevelEntries(t *testing.T) {
	userProfile := t.TempDir()
	for _, name := range []string{"Documents", ".ssh", ".AWS", "Desktop"} {
		if err := os.MkdirAll(filepath.Join(userProfile, name), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
	}
	roots := ProfileReadRoots(userProfile)
	got := map[string]bool{}
	for _, root := range roots {
		got[filepath.Base(root)] = true
	}
	if !got["Documents"] || !got["Desktop"] || got[".ssh"] || got[".AWS"] {
		t.Fatalf("profile roots = %#v", roots)
	}
}

func TestBuildPayloadRootsPreservesHelperRootsWhenReadOverrideIsProvided(t *testing.T) {
	tmp := t.TempDir()
	codexHome := filepath.Join(tmp, "codex-home")
	commandCWD := filepath.Join(tmp, "workspace")
	readableRoot := filepath.Join(tmp, "docs")
	for _, path := range []string{commandCWD, readableRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
	}
	profile := coresandbox.ReadOnlyPermissionProfile()
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	readRoots, writeRoots := BuildPayloadRoots(&SandboxSetupRequest{
		Permissions: permissions,
		CommandCWD:  commandCWD,
		CodexHome:   codexHome,
	}, SetupRootOverrides{ReadRoots: []string{readableRoot}, ReadRootsSet: true})
	if len(writeRoots) != 0 {
		t.Fatalf("writeRoots = %#v", writeRoots)
	}
	if !containsCanonical(readRoots, HelperBinDir(codexHome)) || !containsCanonical(readRoots, readableRoot) {
		t.Fatalf("readRoots = %#v, want helper and override", readRoots)
	}
	if containsCanonical(readRoots, commandCWD) {
		t.Fatalf("readRoots unexpectedly include cwd: %#v", readRoots)
	}
}

func TestEffectiveWriteRootsFiltersSensitiveCodexHome(t *testing.T) {
	tmp := t.TempDir()
	codexHome := filepath.Join(tmp, "codex-home")
	workspace := filepath.Join(tmp, "workspace")
	extra := filepath.Join(tmp, "extra")
	for _, path := range []string{codexHome, SandboxDir(codexHome), SandboxBinDir(codexHome), SandboxSecretsDir(codexHome), workspace, extra} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
	}
	got := EffectiveWriteRootsForSetup(nil, workspace, nil, codexHome, []string{workspace, extra, codexHome, SandboxDir(codexHome)}, true)
	if !containsCanonical(got, workspace) || !containsCanonical(got, extra) {
		t.Fatalf("write roots = %#v, want workspace and extra", got)
	}
	if containsCanonical(got, codexHome) || containsCanonical(got, SandboxDir(codexHome)) {
		t.Fatalf("write roots include protected paths: %#v", got)
	}
}

func TestBuildPayloadDenyWritePathsMergesProtectedChildren(t *testing.T) {
	tmp := t.TempDir()
	commandCWD := filepath.Join(tmp, "workspace")
	extra := filepath.Join(tmp, "extra")
	explicitDeny := filepath.Join(tmp, "explicit-deny")
	for _, path := range []string{filepath.Join(commandCWD, ".git"), filepath.Join(extra, ".codex")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = []string{extra}
	profile.SandboxPolicy.ExcludeTmpdirEnvVar = true
	profile.SandboxPolicy.ExcludeSlashTmp = true
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	got := BuildPayloadDenyWritePaths(&SandboxSetupRequest{Permissions: permissions, CommandCWD: commandCWD}, []string{explicitDeny})
	if !containsCanonical(got, filepath.Join(commandCWD, ".git")) || !containsCanonical(got, filepath.Join(extra, ".codex")) || !containsCanonical(got, explicitDeny) {
		t.Fatalf("deny write paths = %#v", got)
	}
}

func containsCanonical(paths []string, path string) bool {
	want := CanonicalPathKey(path)
	for _, candidate := range paths {
		if CanonicalPathKey(candidate) == want {
			return true
		}
	}
	return false
}
