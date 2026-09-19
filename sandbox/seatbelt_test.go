package sandbox

import (
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
