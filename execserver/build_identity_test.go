package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust build_info_tests build-id vectors (#43513): SHA-256 of
// `git:<lowercase commit>:<target>` prefixed with `sha256:`.
func TestBuildIDVectorsMatchRust(t *testing.T) {
	cases := []struct {
		commit string
		target string
		want   string
	}{
		{
			commit: "0123456789ABCDEF0123456789ABCDEF01234567",
			target: "x86_64-unknown-linux-gnu",
			want:   "sha256:89f3a373036537bde64861b3ad8b1c9494924b0174622e47acda695619630d09",
		},
		{
			commit: "0123456789abcdef0123456789abcdef01234567",
			target: "aarch64-apple-darwin",
			want:   "sha256:90138d7ee35f3f61cd61eb55e8d64105f856055641af175388b102dffa772594",
		},
	}
	for _, tc := range cases {
		got, ok := BuildID(tc.commit, tc.target)
		if !ok || got != tc.want {
			t.Fatalf("BuildID(%q, %q) = %q ok=%v, want %q", tc.commit, tc.target, got, ok, tc.want)
		}
	}
	for _, tc := range []struct {
		name   string
		commit string
		target string
	}{
		{name: "short commit", commit: "abc123", target: "x86_64-unknown-linux-gnu"},
		{name: "non-hex commit", commit: strings.Repeat("g", 40), target: "x86_64-unknown-linux-gnu"},
		{name: "empty target", commit: strings.Repeat("a", 40), target: ""},
		{name: "empty commit", commit: "", target: "x86_64-unknown-linux-gnu"},
		{name: "unstamped commit", commit: "dev", target: "x86_64-unknown-linux-gnu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := BuildID(tc.commit, tc.target); ok || got != "" {
				t.Fatalf("BuildID(%q, %q) = %q ok=%v, want unavailable", tc.commit, tc.target, got, ok)
			}
		})
	}
}

func TestExecServerBuildIdentityMetadata(t *testing.T) {
	previousCommit := execServerBuildIdentity.Commit
	previousTarget := execServerBuildIdentity.Target
	previousVersion := execServerVersion
	t.Cleanup(func() {
		execServerBuildIdentity = BuildIdentity{Commit: previousCommit, Target: previousTarget}
		execServerVersion = previousVersion
	})

	// Historical/unstamped builds report the unknown version and omit the id.
	SetExecServerBuildIdentity("", "")
	SetExecServerVersion("")
	if got := ExecServerExecutorVersion(); got != "0.0.0" {
		t.Fatalf("default executor version = %q, want 0.0.0", got)
	}
	if got := ExecServerProviderID(); got != "" {
		t.Fatalf("unstamped provider id = %q, want empty", got)
	}
	encoded, err := json.Marshal(localEnvironmentInfo())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"executorVersion":"0.0.0"`) || strings.Contains(string(encoded), `"providerId"`) {
		t.Fatalf("unstamped environment info = %s", encoded)
	}

	// A stamped standard build reports both fields.
	SetExecServerBuildIdentity("0123456789abcdef0123456789abcdef01234567", "x86_64-unknown-linux-gnu")
	SetExecServerVersion("0.152.0")
	if got := ExecServerExecutorVersion(); got != "0.152.0" {
		t.Fatalf("executor version = %q, want 0.152.0", got)
	}
	if got := ExecServerProviderID(); got != "sha256:89f3a373036537bde64861b3ad8b1c9494924b0174622e47acda695619630d09" {
		t.Fatalf("provider id = %q", got)
	}
	encoded, err = json.Marshal(localEnvironmentInfo())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"executorVersion":"0.152.0"`) ||
		!strings.Contains(string(encoded), `"providerId":"sha256:89f3a373036537bde64861b3ad8b1c9494924b0174622e47acda695619630d09"`) {
		t.Fatalf("stamped environment info = %s", encoded)
	}
}
