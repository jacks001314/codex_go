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
