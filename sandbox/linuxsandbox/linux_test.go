//go:build linux

package linuxsandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
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

// Mirrors Rust #51407's native-fallback case: deny-read glob expansion runs
// before sandbox confinement, so it must never execute an external binary
// (which could resolve a writable workspace `rg`). Go always walks with the
// in-process `doublestar` matcher, which is Rust's protected-lookup fallback,
// and that walker must still mask dotfiles such as `.env.local`.
func TestLinuxDenyReadGlobExpansionMatchesDotfilesWithoutExternalBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", ".env.local"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write nested/.env.local: %v", err)
	}
	matches, err := expandLinuxDenyGlob(filepath.Join(root, "**", ".env.local"), root, nil)
	if err != nil {
		t.Fatalf("expandLinuxDenyGlob() error = %v", err)
	}
	got := map[string]bool{}
	for _, match := range matches {
		got[match] = true
	}
	for _, want := range []string{filepath.Join(root, ".env.local"), filepath.Join(root, "nested", ".env.local")} {
		if !got[want] {
			t.Fatalf("deny glob matches = %#v, want %s", matches, want)
		}
	}
}

// Mirrors Rust #51527: user ripgrep configuration (for example `--quiet` in
// `RIPGREP_CONFIG_PATH`) must not be able to suppress the file list used to
// build Linux deny masks. Go never invokes ripgrep for this walk, so the
// expansion is unaffected by any ripgrep configuration; this test freezes that
// property by running the expansion with a suppressing config in the
// environment and requiring every denied file to still be returned.
func TestLinuxDenyReadGlobExpansionIgnoresRipgrepConfiguration(t *testing.T) {
	root := t.TempDir()
	denied := []string{
		filepath.Join(root, "secret.key"),
		filepath.Join(root, "nested", "secret.key"),
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	for _, path := range denied {
		if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	allowed := filepath.Join(root, "allowed.pem")
	if err := os.WriteFile(allowed, []byte("allowed"), 0o600); err != nil {
		t.Fatalf("write allowed file: %v", err)
	}
	rgConfig := filepath.Join(root, "ripgrep.conf")
	if err := os.WriteFile(rgConfig, []byte("--quiet\n"), 0o600); err != nil {
		t.Fatalf("write ripgrep config: %v", err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", rgConfig)

	matches, err := expandLinuxDenyGlob(filepath.Join(root, "**", "*.key"), root, nil)
	if err != nil {
		t.Fatalf("expandLinuxDenyGlob() error = %v", err)
	}
	got := map[string]bool{}
	for _, match := range matches {
		got[match] = true
	}
	for _, want := range denied {
		if !got[want] {
			t.Fatalf("deny glob matches = %#v, want %s despite ripgrep config", matches, want)
		}
	}
	if got[allowed] {
		t.Fatalf("deny glob matches = %#v, allowed file %s must not be masked", matches, allowed)
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

// Mirrors Rust #50059: every empty-file mask needs its own preserved descriptor
// so multiple denied files cannot prevent bubblewrap from starting. Go opens a
// per-path memfd instead of Rust's per-mask /dev/null file, but the distinct-fd
// invariant is identical.
func TestAppendUnreadableRootBwrapArgsUsesDistinctDescriptors(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.key")
	second := filepath.Join(dir, "nested", "second.key")
	// `nested/` does not exist, so the mask targets the first missing path
	// component, exactly like Rust `find_first_non_existent_component`
	// (bwrap.rs) — masking the deepest missing path would leave that
	// component unresolved.
	maskedSecond := filepath.Join(dir, "nested")
	var args []string
	firstFD, err := appendUnreadableRootBwrapArgs(&args, first)
	if err != nil {
		t.Fatalf("append first unreadable args error = %v", err)
	}
	secondFD, err := appendUnreadableRootBwrapArgs(&args, second)
	if err != nil {
		t.Fatalf("append second unreadable args error = %v", err)
	}
	if firstFD < 0 || secondFD < 0 || firstFD == secondFD {
		t.Fatalf("descriptors = %d, %d, want distinct non-negative", firstFD, secondFD)
	}
	if !containsArgWindow(args, []string{"--ro-bind-data", strconv.Itoa(firstFD), first}) ||
		!containsArgWindow(args, []string{"--ro-bind-data", strconv.Itoa(secondFD), maskedSecond}) {
		t.Fatalf("args = %#v", args)
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

// TestLinuxNetworkSeccompModeForProfile covers Rust parity for
// `seccomp.rs::apply_permission_profile_to_current_thread`: the network policy
// picks the mode, and a filesystem-restricted policy with unrestricted
// networking still installs the VM-socket filter.
func TestLinuxNetworkSeccompModeForProfile(t *testing.T) {
	restrictedFS := linuxReadOnlyFilesystemPolicy()
	if restrictedFS.hasFullDiskWriteAccess() {
		t.Fatalf("read-only filesystem policy should not have full disk write access")
	}
	unrestrictedFS := linuxUnrestrictedFilesystemPolicy()
	cases := []struct {
		name        string
		profile     *linuxPermissionProfile
		allowProxy  bool
		proxyRouted bool
		want        linuxNetworkSeccompMode
	}{
		{
			name:    "filesystem restricted network enabled installs vm socket filter",
			profile: &linuxPermissionProfile{Filesystem: restrictedFS, NetworkEnabled: true},
			want:    linuxNetworkSeccompVmSocketRestricted,
		},
		{
			name:    "filesystem unrestricted network enabled installs nothing",
			profile: &linuxPermissionProfile{Filesystem: unrestrictedFS, NetworkEnabled: true},
			want:    linuxNetworkSeccompNone,
		},
		{
			name:    "restricted network keeps the restricted filter",
			profile: &linuxPermissionProfile{Filesystem: restrictedFS, NetworkEnabled: false},
			want:    linuxNetworkSeccompRestricted,
		},
		{
			name:        "managed proxy routing keeps the proxy-routed filter",
			profile:     &linuxPermissionProfile{Filesystem: restrictedFS, NetworkEnabled: true},
			allowProxy:  true,
			proxyRouted: true,
			want:        linuxNetworkSeccompProxyRouted,
		},
		{
			name:       "managed proxy without routing keeps the restricted filter",
			profile:    &linuxPermissionProfile{Filesystem: restrictedFS, NetworkEnabled: true},
			allowProxy: true,
			want:       linuxNetworkSeccompRestricted,
		},
		{
			name:        "filesystem restricted falls back to vm sockets without proxy opt-in",
			profile:     &linuxPermissionProfile{Filesystem: restrictedFS, NetworkEnabled: true},
			proxyRouted: true,
			want:        linuxNetworkSeccompVmSocketRestricted,
		},
		{
			name:    "missing profile installs nothing",
			profile: nil,
			want:    linuxNetworkSeccompNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := linuxNetworkSeccompModeForProfile(tc.profile, tc.allowProxy, tc.proxyRouted)
			if got != tc.want {
				t.Fatalf("linuxNetworkSeccompModeForProfile() = %v, want %v", got, tc.want)
			}
		})
	}
}

const (
	linuxSeccompProbeEnv    = "CODEX_GO_LINUX_SECCOMP_PROBE"
	linuxSeccompProbePrefix = "codex-seccomp-probe "
)

var linuxSeccompProbeNames = []string{
	"vsock_socket",
	"vsock_socketpair",
	"unix_socket",
	"unix_socketpair",
	"inet_socket",
	"io_uring_setup",
}

// linuxSeccompProbeNotEPerm asserts only that the probe did not return EPERM.
const linuxSeccompProbeNotEPerm = syscall.Errno(0xffff)

// TestLinuxSeccompFilterMatchesUpstreamRustSemantics re-executes this test
// binary so every probe installs its seccomp filter in a fresh process, then
// asserts the observed errno of real syscalls. A seccomp filter cannot be
// removed, so probes cannot share the test process.
func TestLinuxSeccompFilterMatchesUpstreamRustSemantics(t *testing.T) {
	expectations := []struct {
		mode string
		want map[string]syscall.Errno
	}{
		{
			// Baseline: without a filter, VM sockets, Unix sockets and IP
			// sockets all reach the kernel.
			mode: "control",
			want: map[string]syscall.Errno{
				"vsock_socket":   0,
				"unix_socket":    0,
				"inet_socket":    0,
				"io_uring_setup": linuxSeccompProbeNotEPerm,
			},
		},
		{
			// Filesystem-restricted policy with unrestricted networking.
			mode: "vm",
			want: map[string]syscall.Errno{
				"vsock_socket":     syscall.EPERM,
				"vsock_socketpair": syscall.EPERM,
				"unix_socket":      0,
				"unix_socketpair":  0,
				"inet_socket":      0,
				"io_uring_setup":   syscall.EPERM,
			},
		},
		{
			mode: "restricted",
			want: map[string]syscall.Errno{
				"vsock_socket":   syscall.EPERM,
				"inet_socket":    syscall.EPERM,
				"unix_socket":    0,
				"io_uring_setup": syscall.EPERM,
			},
		},
		{
			mode: "proxy",
			want: map[string]syscall.Errno{
				"vsock_socket":    syscall.EPERM,
				"unix_socket":     syscall.EPERM,
				"unix_socketpair": 0,
				"inet_socket":     0,
				"io_uring_setup":  syscall.EPERM,
			},
		},
	}
	for _, tc := range expectations {
		t.Run(tc.mode, func(t *testing.T) {
			got := runLinuxSeccompProbe(t, tc.mode)
			for name, want := range tc.want {
				have := got[name]
				if want == linuxSeccompProbeNotEPerm {
					if have == syscall.EPERM {
						t.Fatalf("%s = EPERM, want anything else", name)
					}
					continue
				}
				if have != want {
					t.Fatalf("%s = %v, want %v", name, have, want)
				}
			}
		})
	}
}

func runLinuxSeccompProbe(t *testing.T, mode string) map[string]syscall.Errno {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxSeccompProbeChildProcess$")
	cmd.Env = append(os.Environ(), linuxSeccompProbeEnv+"="+mode)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("seccomp probe %q failed: %v\n%s", mode, err, out)
	}
	got := map[string]syscall.Errno{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, linuxSeccompProbePrefix) {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, linuxSeccompProbePrefix), "=", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed probe line %q", line)
		}
		value, convErr := strconv.Atoi(parts[1])
		if convErr != nil {
			t.Fatalf("malformed probe value %q: %v", line, convErr)
		}
		got[parts[0]] = syscall.Errno(value)
	}
	for _, name := range linuxSeccompProbeNames {
		if _, ok := got[name]; !ok {
			t.Fatalf("probe %q produced no result for %s:\n%s", mode, name, out)
		}
	}
	return got
}

// TestLinuxSeccompProbeChildProcess is the child half of
// TestLinuxSeccompFilterMatchesUpstreamRustSemantics. It does nothing during a
// normal package run, which keeps the package free of skipped tests.
func TestLinuxSeccompProbeChildProcess(t *testing.T) {
	mode := os.Getenv(linuxSeccompProbeEnv)
	if mode == "" {
		return
	}
	runtime.LockOSThread()
	switch mode {
	case "control":
	case "vm":
		// Production entry point: a filesystem-restricted profile with
		// unrestricted networking.
		profile := &linuxPermissionProfile{
			Filesystem:     linuxReadOnlyFilesystemPolicy(),
			NetworkEnabled: true,
		}
		if err := applyLinuxPermissionProfile(profile, os.TempDir(), false, false, false); err != nil {
			t.Fatalf("apply filesystem-restricted unrestricted-network profile: %v", err)
		}
	case "restricted":
		if err := installLinuxNetworkSeccompFilter(linuxNetworkSeccompRestricted); err != nil {
			t.Fatalf("install restricted seccomp filter: %v", err)
		}
	case "proxy":
		if err := installLinuxNetworkSeccompFilter(linuxNetworkSeccompProxyRouted); err != nil {
			t.Fatalf("install proxy-routed seccomp filter: %v", err)
		}
	default:
		t.Fatalf("unknown probe mode %q", mode)
	}
	probes := map[string]func() error{
		"vsock_socket": func() error {
			fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
			if err == nil {
				_ = unix.Close(fd)
			}
			return err
		},
		"vsock_socketpair": func() error {
			fds, err := unix.Socketpair(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
			if err == nil {
				_ = unix.Close(fds[0])
				_ = unix.Close(fds[1])
			}
			return err
		},
		"unix_socket": func() error {
			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err == nil {
				_ = unix.Close(fd)
			}
			return err
		},
		"unix_socketpair": func() error {
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err == nil {
				_ = unix.Close(fds[0])
				_ = unix.Close(fds[1])
			}
			return err
		},
		"inet_socket": func() error {
			fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
			if err == nil {
				_ = unix.Close(fd)
			}
			return err
		},
		"io_uring_setup": func() error {
			_, _, errno := unix.Syscall(unix.SYS_IO_URING_SETUP, 0, 0, 0)
			if errno != 0 {
				return errno
			}
			return nil
		},
	}
	for _, name := range linuxSeccompProbeNames {
		probe, ok := probes[name]
		if !ok {
			t.Fatalf("missing probe implementation for %s", name)
		}
		fmt.Printf("%s%s=%d\n", linuxSeccompProbePrefix, name, int(errnoOfProbeError(probe())))
	}
}

func errnoOfProbeError(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return syscall.Errno(0xfffe)
}
