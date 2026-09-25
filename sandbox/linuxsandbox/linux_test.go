//go:build linux

package linuxsandbox

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseLinuxPermissionProfileRustManagedShape(t *testing.T) {
	raw := `{
		"type": "managed",
		"file_system": {
			"type": "restricted",
			"entries": [
				{"path": {"type": "special", "value": {"kind": "root"}}, "access": "read"},
				{"path": {"type": "path", "path": "/work"}, "access": "write"}
			]
		},
		"network": "restricted"
	}`
	profile, err := parseLinuxPermissionProfile(raw)
	if err != nil {
		t.Fatalf("parseLinuxPermissionProfile() error = %v", err)
	}
	if profile.NetworkEnabled {
		t.Fatalf("NetworkEnabled = true, want false")
	}
	if profile.Filesystem.hasFullDiskWriteAccess() {
		t.Fatalf("filesystem unexpectedly has full disk write")
	}
	roots := strings.Join(profile.Filesystem.writableRoots("/repo"), ";")
	if roots != "/work" {
		t.Fatalf("writable roots = %q", roots)
	}
}

func TestParseLinuxPermissionProfileGoShape(t *testing.T) {
	raw := `{"Disabled":false,"SandboxPolicy":{"type":"workspaceWrite","writableRoots":["/extra"],"networkAccess":false,"excludeTmpdirEnvVar":true,"excludeSlashTmp":true},"NetworkEnabled":false}`
	profile, err := parseLinuxPermissionProfile(raw)
	if err != nil {
		t.Fatalf("parseLinuxPermissionProfile() error = %v", err)
	}
	roots := strings.Join(profile.Filesystem.writableRoots("/repo"), ";")
	if !strings.Contains(roots, "/repo") || !strings.Contains(roots, "/extra") {
		t.Fatalf("writable roots = %q", roots)
	}
}

func TestLinuxNetworkSeccompModeFor(t *testing.T) {
	if got := linuxNetworkSeccompModeFor(true, false, false); got != linuxNetworkSeccompNone {
		t.Fatalf("full network mode = %v", got)
	}
	if got := linuxNetworkSeccompModeFor(false, false, false); got != linuxNetworkSeccompRestricted {
		t.Fatalf("restricted network mode = %v", got)
	}
	if got := linuxNetworkSeccompModeFor(true, true, true); got != linuxNetworkSeccompProxyRouted {
		t.Fatalf("proxy-routed network mode = %v", got)
	}
}

func TestRewriteProxyEnvValue(t *testing.T) {
	got, err := rewriteProxyEnvValue("localhost:8888", 4321)
	if err != nil {
		t.Fatalf("rewriteProxyEnvValue() error = %v", err)
	}
	if got != "127.0.0.1:4321" {
		t.Fatalf("rewrite without scheme = %q", got)
	}
	got, err = rewriteProxyEnvValue("http://localhost:8888/path", 4321)
	if err != nil {
		t.Fatalf("rewriteProxyEnvValue() error = %v", err)
	}
	if got != "http://127.0.0.1:4321/path" {
		t.Fatalf("rewrite with scheme = %q", got)
	}
}

func TestLinuxDenyReadGlobExpansion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.env"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write a.env: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b.env"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write b.env: %v", err)
	}
	depth := 1
	policy := &linuxFilesystemPolicy{
		Kind:             "restricted",
		GlobScanMaxDepth: &depth,
		Entries: []linuxFilesystemEntry{{
			Access:  "deny",
			Pattern: filepath.Join(root, "**", "*.env"),
		}},
	}
	paths, err := policy.unreadableRoots(root)
	if err != nil {
		t.Fatalf("unreadableRoots() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(root, "a.env") {
		t.Fatalf("unreadable roots = %#v", paths)
	}
}

func TestAppendUnreadableRootBwrapArgs(t *testing.T) {
	dir := t.TempDir()
	var args []string
	fd, err := appendUnreadableRootBwrapArgs(&args, dir)
	if err != nil {
		t.Fatalf("append dir unreadable args error = %v", err)
	}
	if fd != -1 || !containsArgWindow(args, []string{"--perms", "000", "--tmpfs", dir}) {
		t.Fatalf("dir args = %#v fd=%d", args, fd)
	}
	args = nil
	missing := filepath.Join(dir, "missing-secret")
	fd, err = appendUnreadableRootBwrapArgs(&args, missing)
	if err != nil {
		t.Fatalf("append missing unreadable args error = %v", err)
	}
	if fd < 0 {
		t.Fatalf("missing path fd = %d", fd)
	}
	if !containsArgWindow(args, []string{"--ro-bind-data", strconv.Itoa(fd), missing}) {
		t.Fatalf("missing args = %#v fd=%d", args, fd)
	}
}

func containsArgWindow(args []string, window []string) bool {
	for i := 0; i+len(window) <= len(args); i++ {
		matched := true
		for j := range window {
			if args[i+j] != window[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func TestManagedProxyRoutesWebsocketEnvAndSocketDirsAreSearchable(t *testing.T) {
	oldWS, hadWS := os.LookupEnv("WSS_PROXY")
	defer func() {
		if hadWS {
			_ = os.Setenv("WSS_PROXY", oldWS)
		} else {
			_ = os.Unsetenv("WSS_PROXY")
		}
	}()
	_ = os.Setenv("WSS_PROXY", "http://127.0.0.1:8765")
	routes, configured := planProxyRoutesFromEnv()
	if !configured {
		t.Fatal("proxy not configured")
	}
	found := false
	for _, route := range routes {
		if strings.EqualFold(route.EnvKey, "WSS_PROXY") {
			found = true
		}
	}
	if !found {
		t.Fatalf("routes=%#v", routes)
	}
	dir, err := createProxySocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

// TestProtectedReadOnlySubpathsIncludeAWSMetadata mirrors Rust #48176: an
// existing top-level `.aws` directory under a writable root stays read-only, so
// Linux sandbox writes to protected configuration files fail while siblings
// remain writable.
func TestProtectedReadOnlySubpathsIncludeAWSMetadata(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{".git", ".agents", ".gcode", ".aws"} {
		if err := os.MkdirAll(filepath.Join(cwd, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "write", Special: linuxSpecialPath{Kind: "project_roots"}},
	}}
	protected := policy.protectedReadOnlySubpaths(cwd)
	for _, name := range []string{".git", ".agents", ".gcode", ".aws"} {
		want := filepath.Join(cwd, name)
		found := false
		for _, path := range protected {
			if path == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("protected subpaths = %#v, want %s", protected, want)
		}
	}
	// A missing metadata directory is not materialized as a carveout.
	if len(protected) != 4 {
		t.Fatalf("protected subpaths = %#v, want exactly the four existing metadata dirs", protected)
	}
}
