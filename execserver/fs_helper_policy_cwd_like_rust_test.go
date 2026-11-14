package execserver

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// Mirrors Rust #61276 sandbox_command_rejects_non_native_policy_cwd: a
// filesystem-helper request whose policy working directory belongs to another
// platform is rejected before any sandbox preparation.
func TestFSHelperPolicyCWDRejectsForeignConventionLikeRust(t *testing.T) {
	cases := []struct {
		goos    string
		cwd     string
		wantErr bool
	}{
		{goos: "windows", cwd: "/usr/local/checkout", wantErr: true},
		{goos: "linux", cwd: "/usr/local/checkout"},
		{goos: "darwin", cwd: "/usr/local/checkout"},
		{goos: "windows", cwd: `C:\repo`, wantErr: false},
		{goos: "windows", cwd: "C:/repo"},
		{goos: "linux", cwd: `C:\repo`, wantErr: true},
		{goos: "windows", cwd: ""},
	}
	for _, test := range cases {
		err := validateFSHelperPolicyCWD(test.goos, test.cwd)
		if !test.wantErr {
			if err != nil {
				t.Fatalf("validateFSHelperPolicyCWD(%q, %q) error = %v", test.goos, test.cwd, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("validateFSHelperPolicyCWD(%q, %q) accepted a foreign path", test.goos, test.cwd)
		}
		want := "'" + test.cwd + "' is invalid on '" + test.goos + "'"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

// The sandbox command reports the rejection as an invalid request instead of
// launching the helper with a working directory this executor cannot anchor.
func TestFSHelperSandboxCommandRejectsForeignPolicyCWDLikeRust(t *testing.T) {
	foreign := "/usr/local/checkout"
	if runtime.GOOS == "windows" {
		// Covered on Windows below; keep the native branch for other hosts.
	} else {
		foreign = `C:\repo`
	}
	raw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots"}},"access":"write"}]},"network":"enabled"}`)
	ctx := &FileSystemSandboxContext{Permissions: raw, CWD: foreign}
	_, _, err := fsSandboxCommand(ctx, nil, "", foreign, nil, "")
	if err == nil {
		t.Fatal("sandbox command accepted a foreign policy cwd")
	}
	message := err.Error()
	if !strings.Contains(message, "is invalid on '"+runtime.GOOS+"'") {
		t.Fatalf("error = %v, want the invalid-on-this-executor message", err)
	}
}
