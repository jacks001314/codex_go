package applypatch

import "testing"

func wrappedPatchForInterceptionTest(body string) string {
	return "*** Begin Patch\n" + body + "\n*** End Patch"
}

func bashArgsForInterceptionTest(script string) []string {
	return []string{"bash", "-lc", script}
}

// Rust #57573 `classifies_intercepted_commands_without_a_filesystem`.
func TestClassifiesInterceptedCommandsWithoutFilesystemLikeRust(t *testing.T) {
	cwd := "/workspace"
	patch := wrappedPatchForInterceptionTest("*** Delete File: target")
	explicit := "apply_patch <<'PATCH'\n" + patch + "\nPATCH"
	malformed := "apply_patch <<'PATCH'\ninvalid patch\nPATCH"
	for _, testCase := range []struct {
		name   string
		argv   []string
		square bool
	}{
		{name: "explicit heredoc", argv: bashArgsForInterceptionTest(explicit), square: true},
		{name: "implicit raw patch", argv: bashArgsForInterceptionTest(patch), square: true},
		{name: "malformed heredoc body", argv: bashArgsForInterceptionTest(malformed), square: true},
		{name: "unrelated echo", argv: bashArgsForInterceptionTest("echo apply_patch"), square: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := InterceptsCommand(testCase.argv, cwd); got != testCase.square {
				t.Fatalf("InterceptsCommand(%q) = %t, want %t", testCase.argv, got, testCase.square)
			}
		})
	}
}

func TestInterceptsDirectApplyPatchArgumentsLikeRust(t *testing.T) {
	patch := wrappedPatchForInterceptionTest("*** Add File: foo\n+hi")
	intercepted := [][]string{
		{"apply_patch", patch},
		{"applypatch", patch},
		{"apply_patch", "not a patch"},
		{patch},
		{"sh", "-c", "apply_patch <<EOF\n" + patch + "\nEOF"},
		{"pwsh", "-NoProfile", "-Command", "apply_patch <<'EOF'\n" + patch + "\nEOF"},
		{"cmd.exe", "/c", "apply_patch <<'EOF'\n" + patch + "\nEOF"},
	}
	for _, argv := range intercepted {
		if !InterceptsCommand(argv, "/workspace") {
			t.Fatalf("InterceptsCommand(%q) = false, want true", argv)
		}
	}
	notIntercepted := [][]string{
		{"apply_patch"},
		{"cat", "patch.txt"},
		{"bash", "-lc", "cat <<'EOF'\nhello\nEOF"},
		{"bash", "-lc", "cd /tmp && apply_patch <<'EOF'\n" + patch + "\nEOF && echo done"},
		{"bash", "-lc", "echo apply_patch <<'EOF'\n" + patch + "\nEOF"},
		{"python", "-c", "apply_patch <<'EOF'\n" + patch + "\nEOF"},
	}
	for _, argv := range notIntercepted {
		if InterceptsCommand(argv, "/workspace") {
			t.Fatalf("InterceptsCommand(%q) = true, want false", argv)
		}
	}
}

func TestInterceptsCommandRecognizesCDPrefixLikeRust(t *testing.T) {
	patch := wrappedPatchForInterceptionTest("*** Delete File: target")
	script := "cd /workspace/repo && apply_patch <<-'PATCH'\n" + patch + "\n\tPATCH"
	if !InterceptsCommand(bashArgsForInterceptionTest(script), "/workspace") {
		t.Fatal("cd-prefixed apply_patch heredoc was not intercepted")
	}
}
