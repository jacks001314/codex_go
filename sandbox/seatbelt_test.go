package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSeatbeltPolicyWorkspaceWriteAndNetwork(t *testing.T) {
	cwd := "/workspace"
	profile := WorkspaceWritePermissionProfile()
	policy, parameters, err := buildSeatbeltPolicy(cwd, &profile, []string{"/tmp/agent.sock", "relative.sock"})
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

func TestBuildSeatbeltPolicyDeniedReadAndCommand(t *testing.T) {
	denied := "/private/secret"
	profile := ReadOnlyPermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{{Path: FileSystemPath{Type: "path", Path: denied}, Access: FileSystemAccessDeny}}
	args, err := createSeatbeltCommandArgs([]string{"echo", "ok"}, "", &profile, nil)
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
	policy, _, err := buildSeatbeltPolicy("/workspace", &profile, nil)
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if !strings.Contains(policy, `(deny mach-lookup (xpc-service-name-prefix ""))`) {
		t.Fatalf("policy missing XPC deny rule:\n%s", policy)
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
	policy, parameters, err := buildSeatbeltPolicy("/workspace", &profile, nil)
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
	policy, parameters, err := buildSeatbeltPolicy("/workspace", &profile, nil)
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
	if _, _, err := buildSeatbeltPolicy(root, &profile, nil); err != nil {
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
