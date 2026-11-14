package execserver

import "testing"

// Rust #61276: a filesystem helper that asks for a Windows token sandbox must
// fail with SandboxUnavailable on a non-Windows executor, while a request that
// explicitly disables the Windows sandbox or targets a Unix path stays local.
func TestFSHelperRejectsWindowsTokenSandboxOnNonWindowsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		goos  string
		level string
		cwd   string
		want  bool
	}{
		{name: "default token level on windows path", goos: "linux", level: "", cwd: `C:\work`, want: true},
		{name: "elevated on windows path", goos: "linux", level: "elevated", cwd: `C:\work`, want: true},
		{name: "disabled keeps unconfined request", goos: "linux", level: "disabled", cwd: `C:\work`, want: false},
		{name: "unix path", goos: "linux", level: "", cwd: "/work", want: false},
		{name: "native windows executor", goos: "windows", level: "", cwd: `C:\work`, want: false},
	} {
		ctx := &FileSystemSandboxContext{WindowsSandboxLevel: tc.level}
		if got := fsHelperWindowsSandboxUnavailable(tc.goos, ctx, tc.cwd); got != tc.want {
			t.Fatalf("%s: unavailable = %v, want %v", tc.name, got, tc.want)
		}
	}
	if fsHelperWindowsSandboxUnavailable("linux", nil, `C:\work`) {
		t.Fatal("nil context reported an unavailable sandbox")
	}
}
