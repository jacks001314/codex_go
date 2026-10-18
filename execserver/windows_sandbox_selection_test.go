package execserver

import "testing"

// Mirrors Rust #45730: the executor selects a concrete Windows sandbox backend
// from the compatibility level, treats MXC as a backend, and disables Windows
// selection for non-Windows path conventions.
func TestSelectWindowsSandboxForExecutorLikeRust(t *testing.T) {
	tests := []struct {
		name  string
		level string
		cwd   string
		want  WindowsSandboxSelection
	}{
		{name: "empty windows defaults to restricted token", level: "", cwd: `C:\repo`, want: WindowsSandboxSelectionRestrictedToken},
		{name: "restricted token", level: windowsSandboxLevelRestrictedToken, cwd: `C:\repo`, want: WindowsSandboxSelectionRestrictedToken},
		{name: "elevated", level: windowsSandboxLevelElevated, cwd: `C:\repo`, want: WindowsSandboxSelectionElevated},
		{name: "mxc is a backend", level: windowsSandboxLevelMxc, cwd: `C:\repo`, want: WindowsSandboxSelectionMXC},
		{name: "disabled", level: windowsSandboxLevelDisabled, cwd: `C:\repo`, want: WindowsSandboxSelectionDisabled},
		{name: "posix path disables windows sandbox", level: windowsSandboxLevelMxc, cwd: "/repo", want: WindowsSandboxSelectionDisabled},
		{name: "posix path disables elevated", level: windowsSandboxLevelElevated, cwd: "/repo", want: WindowsSandboxSelectionDisabled},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := selectWindowsSandboxForExecutor(testCase.level, testCase.cwd)
			if err != nil {
				t.Fatalf("selectWindowsSandboxForExecutor(%q, %q) error = %v", testCase.level, testCase.cwd, err)
			}
			if got != testCase.want {
				t.Fatalf("selectWindowsSandboxForExecutor(%q, %q) = %q, want %q", testCase.level, testCase.cwd, got, testCase.want)
			}
		})
	}
	if _, err := selectWindowsSandboxForExecutor("bogus", `C:\repo`); err == nil {
		t.Fatal("unknown sandbox level must fail closed")
	}
}
