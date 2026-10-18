//go:build darwin

package sandbox

import (
	"fmt"
	"strings"
	"testing"
)

// Mirrors Rust #45984: a Unix-socket allowlist cannot reopen the fixed
// app-server rendezvous directory, and the deny is emitted after the allow.
func TestSeatbeltDeniesDaemonSocketDirectoryLikeRust(t *testing.T) {
	cwd := t.TempDir()
	profile := PermissionProfile{SandboxPolicy: NewWorkspaceWritePolicy(), NetworkEnabled: true}
	allowedSocket := "/tmp/user-socket.sock"
	policy, parameters, err := buildSeatbeltPolicy(cwd, &profile, []string{allowedSocket}, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy() error = %v", err)
	}
	directory, ok := sharedDaemonSocketDirectory()
	if !ok {
		t.Fatal("sharedDaemonSocketDirectory() unavailable")
	}
	daemonParameter := ""
	for _, parameter := range parameters {
		if parameter.Value == directory {
			daemonParameter = parameter.Name
		}
	}
	if daemonParameter == "" {
		t.Fatalf("daemon directory %q missing from parameters %#v", directory, parameters)
	}
	deny := fmt.Sprintf(`(deny network-outbound (remote unix-socket (subpath (param "%s"))))`, daemonParameter)
	denyIndex := strings.Index(policy, deny)
	allowIndex := strings.Index(policy, `(allow network-outbound (remote unix-socket (subpath`)
	if denyIndex < 0 || allowIndex < 0 || denyIndex < allowIndex {
		t.Fatalf("daemon deny must follow the socket allowlist:\n%s", policy)
	}
	if !strings.Contains(policy, fmt.Sprintf(`(deny file-read* file-write* (subpath (param "%s")))`, daemonParameter)) {
		t.Fatalf("daemon directory read/write deny missing:\n%s", policy)
	}
}
