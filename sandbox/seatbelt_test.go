package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSeatbeltPolicyWorkspaceWriteAndNetwork(t *testing.T) {
	cwd := "/workspace"
	profile := WorkspaceWritePermissionProfile()
	policy, parameters, err := buildSeatbeltPolicy(cwd, &profile, []string{"/tmp/agent.sock", "relative.sock"}, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	for _, want := range []string{"(deny file-write*)", "(deny network*)", "PROTECTED_WRITE_", "SCRATCH_"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
	if !seatbeltHasParameterPrefix(parameters, "PROTECTED_WRITE_") || !seatbeltHasParameterPrefix(parameters, "SCRATCH_") || !seatbeltParametersContain(parameters, "/tmp/agent.sock") || !seatbeltParametersContain(parameters, "/private/tmp") {
		t.Fatalf("parameters = %#v", parameters)
	}
	if seatbeltParametersContain(parameters, "relative.sock") {
		t.Fatalf("relative socket accepted: %#v", parameters)
	}
}

// Mirrors Rust #45548: the prepared managed-network context controls the
// Unix-socket policy, and explicit extra grants are still preserved. Relative
// entries are ignored rather than resolved against the host CWD.
func TestBuildSeatbeltPolicyUsesPreparedManagedNetworkUnixSocketsLikeRust(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	explicitSocket := filepath.Join(root, "explicit.sock")
	preparedSocket := filepath.Join(root, "prepared.sock")
	profile := WorkspaceWritePermissionProfile()
	managed := &seatbeltManagedNetworkContext{
		AllowUnixSockets: []string{preparedSocket, "relative.sock"},
	}
	policy, parameters, err := buildSeatbeltPolicyWithManagedNetwork(root, &profile, []string{explicitSocket}, managed, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	if !strings.Contains(policy, "(allow system-socket (socket-domain AF_UNIX))") {
		t.Fatalf("policy missing Unix-socket domain grant:\n%s", policy)
	}
	if strings.Contains(policy, "(allow network-bind (local unix-socket))") ||
		strings.Contains(policy, "(allow network-outbound (remote unix-socket))\n") {
		t.Fatalf("restricted policy unexpectedly grants every Unix socket:\n%s", policy)
	}
	for index, wantPath := range []string{explicitSocket, preparedSocket} {
		wantName := fmt.Sprintf("UNIX_SOCKET_PATH_%d", index)
		wantParameter := seatbeltParameter{Name: wantName, Value: wantPath}
		if !strings.Contains(policy, fmt.Sprintf(`(allow network-bind (local unix-socket (subpath (param "%s"))))`, wantName)) ||
			!strings.Contains(policy, fmt.Sprintf(`(allow network-outbound (remote unix-socket (subpath (param "%s"))))`, wantName)) {
			t.Fatalf("policy missing socket rules for %s:\n%s", wantName, policy)
		}
		found := false
		for _, parameter := range parameters {
			if parameter == wantParameter {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("parameters = %#v, want %#v", parameters, wantParameter)
		}
	}
	if seatbeltParametersContain(parameters, "relative.sock") {
		t.Fatalf("relative socket accepted: %#v", parameters)
	}
}

// Mirrors Rust #45548's empty-context case: an empty prepared allowlist keeps
// AF_UNIX denied instead of inheriting a broader live-proxy grant.
func TestBuildSeatbeltPolicyPreparedEmptyUnixSocketsStayDeniedLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	policy, parameters, err := buildSeatbeltPolicyWithManagedNetwork(t.TempDir(), &profile, nil, &seatbeltManagedNetworkContext{}, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	if strings.Contains(policy, "(allow system-socket (socket-domain AF_UNIX))") ||
		strings.Contains(policy, "(allow network-bind (local unix-socket") ||
		strings.Contains(policy, "(allow network-outbound (remote unix-socket") {
		t.Fatalf("empty prepared policy granted Unix sockets:\n%s", policy)
	}
	if seatbeltHasParameterPrefix(parameters, "UNIX_SOCKET_PATH_") {
		t.Fatalf("empty prepared policy emitted socket parameters: %#v", parameters)
	}
}

// Mirrors Rust #45548's explicit allow-all behavior: every Unix socket is
// available, but unrestricted IP networking is still denied by the profile.
func TestBuildSeatbeltPolicyPreparedAllowAllUnixSocketsLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	managed := &seatbeltManagedNetworkContext{
		AllowUnixSockets:               []string{"/tmp/ignored.sock"},
		DangerouslyAllowAllUnixSockets: true,
	}
	policy, parameters, err := buildSeatbeltPolicyWithManagedNetwork(t.TempDir(), &profile, nil, managed, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	for _, want := range []string{
		"(allow system-socket (socket-domain AF_UNIX))",
		"(allow network-bind (local unix-socket))",
		"(allow network-outbound (remote unix-socket))",
		"(deny network*)",
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
	if strings.Contains(policy, "(allow network-outbound)\n") {
		t.Fatalf("allow-all Unix sockets widened IP networking:\n%s", policy)
	}
	if seatbeltHasParameterPrefix(parameters, "UNIX_SOCKET_PATH_") {
		t.Fatalf("allow-all policy emitted path parameters: %#v", parameters)
	}
}

func TestParseSeatbeltManagedNetworkContextRejectsInvalidJSON(t *testing.T) {
	if _, err := parseSeatbeltManagedNetworkContext("{"); err == nil {
		t.Fatal("invalid managed network context was accepted")
	}
}

func TestBuildSeatbeltPolicyDeniedReadAndCommand(t *testing.T) {
	denied := "/private/secret"
	profile := ReadOnlyPermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{{Path: FileSystemPath{Type: "path", Path: denied}, Access: FileSystemAccessDeny}}
	args, err := createSeatbeltCommandArgs([]string{"echo", "ok"}, "", &profile, nil, "")
	if err != nil {
		t.Fatalf("createSeatbeltCommandArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if args[0] != macOSSeatbeltExecutable || !strings.Contains(joined, "CODEX_SANDBOX=seatbelt") || !strings.Contains(joined, "DENIED_READ_") || !strings.Contains(joined, denied) {
		t.Fatalf("args = %#v", args)
	}
}

// TestBuildSeatbeltPolicyDeniesXPCLookupsLikeRust covers #46583: restricted
// profiles deny Mach XPC service lookups.
func TestBuildSeatbeltPolicyDeniesXPCLookupsLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if !strings.Contains(policy, `(deny mach-lookup (xpc-service-name-prefix ""))`) {
		t.Fatalf("policy missing XPC deny rule:\n%s", policy)
	}
}

// TestBuildSeatbeltPolicyAllowsTLSTrustServiceLikeRust covers #48565: network
// profiles must let system libcurl reach the trust-evaluation service, and the
// grant has to follow the XPC deny so it wins. Profiles without networking keep
// the service denied.
func TestBuildSeatbeltPolicyAllowsTLSTrustServiceLikeRust(t *testing.T) {
	networked := WorkspaceWritePermissionProfile()
	networked.NetworkEnabled = true
	policy, _, err := buildSeatbeltPolicy("/workspace", &networked, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	denyIndex := strings.Index(policy, `(deny mach-lookup (xpc-service-name-prefix ""))`)
	allowIndex := strings.Index(policy, seatbeltTLSTrustService)
	if denyIndex < 0 || allowIndex < 0 {
		t.Fatalf("policy missing XPC deny or trust-service grant:\n%s", policy)
	}
	if allowIndex < denyIndex {
		t.Fatalf("trust-service grant precedes the XPC deny and would be overridden:\n%s", policy)
	}
	if strings.Contains(policy, "(deny network*)") {
		t.Fatalf("network profile unexpectedly denies all networking:\n%s", policy)
	}

	offline := WorkspaceWritePermissionProfile()
	offlinePolicy, _, err := buildSeatbeltPolicy("/workspace", &offline, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if strings.Contains(offlinePolicy, seatbeltTLSTrustService) {
		t.Fatalf("network-disabled policy granted TLS trust access:\n%s", offlinePolicy)
	}
}

// TestBuildSeatbeltPolicyDeniesMutatingFcntlsLikeRust covers #46500:
// F_MAKECOMPRESSED (80) and F_TRANSFEREXTENTS (110) mutate files through
// read-only descriptors, so every policy without full disk write access denies
// them explicitly and the denial stays last so no broader allowance reopens it.
func TestBuildSeatbeltPolicyDeniesMutatingFcntlsLikeRust(t *testing.T) {
	const denyFcntls = `(deny system-fcntl (fcntl-command 80 110))`
	for name, profile := range map[string]PermissionProfile{
		"read only":       ReadOnlyPermissionProfile(),
		"workspace write": WorkspaceWritePermissionProfile(),
	} {
		policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
		if err != nil {
			t.Fatalf("%s: buildSeatbeltPolicy: %v", name, err)
		}
		if got := strings.Count(policy, denyFcntls); got != 1 {
			t.Fatalf("%s: mutating fcntl deny appears %d times, want 1:\n%s", name, got, policy)
		}
		lines := strings.Split(strings.TrimSpace(policy), "\n")
		if last := lines[len(lines)-1]; last != denyFcntls {
			t.Fatalf("%s: last policy line = %q, want %q", name, last, denyFcntls)
		}
	}
	fullAccess := FullAccessPermissionProfile()
	policy, _, err := buildSeatbeltPolicy("/workspace", &fullAccess, nil, "")
	if err != nil {
		t.Fatalf("full access: buildSeatbeltPolicy: %v", err)
	}
	if strings.Contains(policy, denyFcntls) {
		t.Fatalf("full access policy denies mutating fcntls:\n%s", policy)
	}
}

// Mirrors Rust #39599: a writable root directory is an authority boundary and
// cannot be unlinked or renamed by the sandboxed process.
func TestBuildSeatbeltPolicyProtectsWritableRootAnchorLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if !strings.Contains(policy, `(deny file-write-unlink (require-all (literal (param "WRITABLE_ROOT_`) ||
		!strings.Contains(policy, `(vnode-type DIRECTORY)))`) {
		t.Fatalf("policy missing writable-root anchor deny:\n%s", policy)
	}
}

// Mirrors Rust #42716: writable roots with nested symlinks are rejected by
// default, the rejection explains the trusted-home opt-out, and a resolved
// CODEX_HOME alias covers only roots beneath that home.
func TestBuildSeatbeltPolicyAllowsTrustedCodexHomeSymlinksLikeRust(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "visualizations")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	outside := t.TempDir()
	profile := WorkspaceWritePermissionProfile()
	legacy := profile.LegacySandboxPolicy()
	legacy.ExcludeSlashTmp = true
	legacy.ExcludeTmpdirEnvVar = true
	legacy.WritableRoots = nil
	profile.SandboxPolicy = legacy

	_, _, err := buildSeatbeltPolicy(link, &profile, nil, "")
	if err == nil || !strings.Contains(err.Error(), "symlinked writable roots are not supported") {
		t.Fatalf("disabled opt-out error = %v, want nested symlink rejection", err)
	}
	for _, want := range []string{"allow_symlinked_codex_home = true", "targets outside CODEX_HOME", "changed between commands"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection error missing %q: %v", want, err)
		}
	}

	if _, _, err := buildSeatbeltPolicy(link, &profile, nil, outside); err == nil || !strings.Contains(err.Error(), "symlinked writable roots are not supported") {
		t.Fatalf("out-of-scope home error = %v, want rejection", err)
	}

	_, parameters, err := buildSeatbeltPolicy(link, &profile, nil, home)
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy(allowed home) error = %v", err)
	}
	if !seatbeltParametersContain(parameters, target) {
		t.Fatalf("writable root parameter = %#v, want resolved target %q", parameters, target)
	}
	if seatbeltParametersContain(parameters, link) {
		t.Fatalf("policy retained the unresolved symlink root %q: %#v", link, parameters)
	}

	alias := filepath.Join(outside, "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := buildSeatbeltPolicy(link, &profile, nil, alias); err != nil {
		t.Fatalf("buildSeatbeltPolicy(resolved alias) error = %v", err)
	}
}

// Mirrors Rust #46571: the implicit scratch grants respect the profile's
// unreadable paths, and the exclusion lands after the grant so Seatbelt's
// last-match-wins semantics deny it.
func TestBuildSeatbeltPolicyConstrainsScratchWithExclusionsLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "path", Path: "/tmp/secret"},
		Access: FileSystemAccessDeny,
	}}
	policy, parameters, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if !seatbeltParametersContain(parameters, "/tmp/secret") {
		t.Fatalf("scratch exclusion parameter missing: %#v", parameters)
	}
	grant := strings.Index(policy, `(allow file-read* file-test-existence file-write* (subpath (param "SCRATCH_0"))`)
	excluded := strings.Index(policy, `(deny file-write* (subpath (param "SCRATCH_EXCLUDED_`)
	if grant < 0 {
		t.Fatalf("scratch grant missing:\n%s", policy)
	}
	if excluded < 0 || excluded < grant {
		t.Fatalf("scratch exclusion order = grant %d excluded %d:\n%s", grant, excluded, policy)
	}
}

// Mirrors Rust #46571: an ancestor of a protected path is never unlinkable, so
// renaming it cannot relocate the protected descendants past their carveouts.
// The deny stays last so no broader allowance reopens the rename operation.
func TestBuildSeatbeltPolicyProtectsWritableRootAncestorsLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	policy, parameters, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if !seatbeltHasParameterPrefix(parameters, "PROTECTED_ANCESTOR_") {
		t.Fatalf("protected ancestor parameters missing: %#v", parameters)
	}
	unlink := strings.Index(policy, `(deny file-write-unlink (require-all (vnode-type DIRECTORY) (literal (param "PROTECTED_ANCESTOR_0"))))`)
	if unlink < 0 {
		t.Fatalf("protected ancestor deny missing:\n%s", policy)
	}
	if last := strings.LastIndex(policy, "(allow "); last > unlink {
		t.Fatalf("unlink denies must follow every allowance:\n%s", policy)
	}
}

// Mirrors Rust #46571: a protected path that is a symlink protects the resolved
// target's parents as well, so moving either the logical entry or the target
// cannot relocate the protected path past its carveout.
func TestBuildSeatbeltPolicyProtectsResolvedSymlinkAncestorsLikeRust(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real", "nested")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(root, ".git")
	if err := os.Symlink(target, logical); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	profile := WorkspaceWritePermissionProfile()
	policy := profile.LegacySandboxPolicy()
	policy.WritableRoots = []string{root}
	profile.SandboxPolicy = policy

	ancestors := seatbeltProtectedAncestors(policy, root)
	contains := func(want string) bool {
		want = cleanSeatbeltPath(want)
		for _, ancestor := range ancestors {
			if ancestor == want {
				return true
			}
		}
		return false
	}
	if !contains(root) {
		t.Fatalf("logical path ancestors = %#v, want %q", ancestors, root)
	}
	// Only the symlink resolution contributes this intermediate directory.
	if !contains(filepath.Join(root, "real")) {
		t.Fatalf("resolved target ancestors = %#v, want %q", ancestors, filepath.Join(root, "real"))
	}
	resolved, err := filepath.EvalSymlinks(logical)
	if err != nil || cleanSeatbeltPath(resolved) != cleanSeatbeltPath(target) {
		t.Fatalf("symlink resolution = %q, %v", resolved, err)
	}
	if _, _, err := buildSeatbeltPolicy(root, &profile, nil, ""); err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
}

// The resolver indirection covers hosts where symlink creation is unavailable:
// a resolved target one level deeper than the logical entry contributes its own
// ancestors (Rust #46571).
func TestSeatbeltProtectedAncestorsFollowResolvedTargetsLikeRust(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, ".git")
	resolved := filepath.Join(root, "real", "nested", ".git")
	previous := seatbeltSymlinkResolver
	seatbeltSymlinkResolver = func(path string) string {
		if cleanSeatbeltPath(path) == cleanSeatbeltPath(logical) {
			return cleanSeatbeltPath(resolved)
		}
		return ""
	}
	t.Cleanup(func() { seatbeltSymlinkResolver = previous })

	profile := WorkspaceWritePermissionProfile()
	policy := profile.LegacySandboxPolicy()
	policy.WritableRoots = []string{root}
	profile.SandboxPolicy = policy

	ancestors := seatbeltProtectedAncestors(policy, root)
	want := cleanSeatbeltPath(filepath.Join(root, "real", "nested"))
	found := false
	for _, ancestor := range ancestors {
		if ancestor == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("resolved ancestors = %#v, want %q", ancestors, want)
	}
}

func seatbeltParametersContain(parameters []seatbeltParameter, value string) bool {
	value = cleanSeatbeltPath(value)
	for _, parameter := range parameters {
		if cleanSeatbeltPath(parameter.Value) == value {
			return true
		}
	}
	return false
}

func seatbeltHasParameterPrefix(parameters []seatbeltParameter, prefix string) bool {
	for _, parameter := range parameters {
		if strings.HasPrefix(parameter.Name, prefix) {
			return true
		}
	}
	return false
}

// Mirrors Rust #18096: Seatbelt cannot evaluate the filesystem policy's glob
// syntax itself, so each unreadable glob becomes anchored read/write denies plus
// unlink protection for the directories that could contain matches.
func TestBuildSeatbeltPolicyDeniesUnreadableGlobsLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "glob_pattern", Pattern: "/workspace/**/*.env"},
		Access: FileSystemAccessDeny,
	}}
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	for _, want := range []string{
		`(deny file-read* (regex #"^/workspace/(.*/)?[^/]*\.env$"))`,
		`(deny file-write* (regex #"^/workspace/(.*/)?[^/]*\.env$"))`,
		`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (regex #"^/workspace/.*$")))`,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
	if lastAllow := strings.LastIndex(policy, "(allow "); lastAllow > strings.Index(policy, `(deny file-read* (regex`) {
		t.Fatalf("glob deny must follow every allowance:\n%s", policy)
	}
}

// Mirrors Rust's `seatbelt_regex_for_glob` unit tests: the supported git-style
// glob subset translates to the same anchored Seatbelt regex.
func TestSeatbeltRegexForGlobMatchesRust(t *testing.T) {
	cases := []struct {
		pattern string
		exact   bool
		want    string
	}{
		{"/tmp/repo/**/*.env", false, `^/tmp/repo/(.*/)?[^/]*\.env$`},
		{"/tmp/repo/*/file[0-9]?.txt", false, `^/tmp/repo/[^/]*/file[0-9][^/]\.txt$`},
		{"/tmp/repo/{.env,secrets.yml}", false, `^/tmp/repo/(\.env|secrets\.yml)$`},
		{"/tmp/repo/{private", true, `^/tmp/repo/(private)$`},
		{`/tmp/repo/config\?.env`, false, `^/tmp/repo/config\?\.env(/.*)?$`},
		{"/tmp/repo/[*.env", false, `^/tmp/repo/\[[^/]*\.env$`},
		{"/tmp/repo/[!a]x", false, `^/tmp/repo/[^a]x$`},
		{"/tmp/literal", false, `^/tmp/literal(/.*)?$`},
		{"/tmp/literal", true, `^/tmp/literal$`},
	}
	for _, testCase := range cases {
		got, ok := seatbeltRegexForGlob(testCase.pattern, testCase.exact)
		if !ok || got != testCase.want {
			t.Fatalf("seatbeltRegexForGlob(%q, exact=%v) = %q, %v; want %q", testCase.pattern, testCase.exact, got, ok, testCase.want)
		}
	}
	if _, ok := seatbeltRegexForGlob("", false); ok {
		t.Fatal("empty pattern accepted")
	}
}

// Mirrors Rust #47920: a root-anchored recursive literal basename stays denied
// after ancestor moves, so it skips the ancestor unlink denies and extends its
// deny to descendants of a matching directory. Scoped and nonliteral patterns
// keep the ancestor protection.
func TestBuildSeatbeltPolicyGlobalLiteralBasenameSkipsAncestorUnlinkLikeRust(t *testing.T) {
	literal := WorkspaceWritePermissionProfile()
	literal.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "glob_pattern", Pattern: "/**/.env"},
		Access: FileSystemAccessDeny,
	}}
	policy, _, err := buildSeatbeltPolicy("/workspace", &literal, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	for _, want := range []string{
		`(deny file-read* (regex #"^/(.*/)?\.env(/.*)?$"))`,
		`(deny file-write* (regex #"^/(.*/)?\.env(/.*)?$"))`,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
	if strings.Contains(policy, "(vnode-type DIRECTORY) (regex #") {
		t.Fatalf("global literal basename should not deny ancestor unlinks:\n%s", policy)
	}

	nonliteral := WorkspaceWritePermissionProfile()
	nonliteral.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "glob_pattern", Pattern: "/**/*.env"},
		Access: FileSystemAccessDeny,
	}}
	nonliteralPolicy, _, err := buildSeatbeltPolicy("/workspace", &nonliteral, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	for _, want := range []string{
		`(deny file-read* (regex #"^/(.*/)?[^/]*\.env$"))`,
		`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (regex #"^/.*$")))`,
		`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (regex #"^/$")))`,
	} {
		if !strings.Contains(nonliteralPolicy, want) {
			t.Fatalf("nonliteral policy missing %q:\n%s", want, nonliteralPolicy)
		}
	}
}

// Mirrors Rust's canonicalized static-prefix coverage: the deny must also cover
// the symlink target of the glob's static directory prefix.
func TestSeatbeltUnreadableGlobPolicyCanonicalizesStaticPrefixLikeRust(t *testing.T) {
	previous := seatbeltSymlinkResolver
	seatbeltSymlinkResolver = func(path string) string {
		if cleanSeatbeltPath(path) == "/linkroot" {
			return "/realroot"
		}
		return ""
	}
	t.Cleanup(func() { seatbeltSymlinkResolver = previous })

	profile := ReadOnlyPermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "glob_pattern", Pattern: "/linkroot/**/*.env"},
		Access: FileSystemAccessDeny,
	}}
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	for _, want := range []string{
		`(deny file-read* (regex #"^/linkroot/(.*/)?[^/]*\.env$"))`,
		`(deny file-read* (regex #"^/realroot/(.*/)?[^/]*\.env$"))`,
		`(deny file-write* (regex #"^/realroot/(.*/)?[^/]*\.env$"))`,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
}

// Mirrors Rust's relative-glob resolution: a relative deny glob is anchored at
// the sandbox cwd, and because it is scoped it keeps ancestor unlink denies.
func TestBuildSeatbeltPolicyResolvesRelativeGlobsAgainstCWDLikeRust(t *testing.T) {
	profile := ReadOnlyPermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "glob_pattern", Pattern: "**/.env"},
		Access: FileSystemAccessDeny,
	}}
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	for _, want := range []string{
		`(deny file-read* (regex #"^/workspace/(.*/)?\.env$"))`,
		`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (regex #"^/workspace/.*$")))`,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
}

// An unrestricted profile contributes no unreadable globs.
func TestBuildSeatbeltPolicyUnrestrictedProfileSkipsGlobDeniesLikeRust(t *testing.T) {
	profile := PermissionProfile{SandboxPolicy: NewDangerFullAccessPolicy(), NetworkEnabled: true}
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if strings.Contains(policy, "(regex #") {
		t.Fatalf("unrestricted profile emitted glob denies:\n%s", policy)
	}
}
