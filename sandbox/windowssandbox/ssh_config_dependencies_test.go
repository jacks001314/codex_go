package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSSHConfigDependencyPathsCollectsProfilePathDirectives(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(.ssh) error = %v", err)
	}
	config := `
Host devbox
  IdentityFile ~/.keys/id_ed25519
  IdentityFile '~/.keys/quoted key'
  CertificateFile = %d/.certs/devbox-cert.pub
  UserKnownHostsFile ${HOME}/.known_hosts_custom
  IdentityAgent=%d/.agent/socket
`
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	got := SSHConfigDependencyPaths(home)
	for _, want := range []string{
		filepath.Join(home, ".ssh", "config"),
		filepath.Join(home, ".keys", "id_ed25519"),
		filepath.Join(home, ".keys", "quoted key"),
		filepath.Join(home, ".certs", "devbox-cert.pub"),
		filepath.Join(home, ".known_hosts_custom"),
		filepath.Join(home, ".agent", "socket"),
	} {
		if !containsCanonical(got, want) {
			t.Fatalf("dependencies = %#v, missing %s", got, want)
		}
	}
}

func TestSSHConfigDependencyPathsRecursivelyCollectsIncludes(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	includeDir := filepath.Join(sshDir, "conf.d")
	if err := os.MkdirAll(includeDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(includeDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Include conf.d/*.conf\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	include := filepath.Join(includeDir, "devbox.conf")
	if err := os.WriteFile(include, []byte("CertificateFile ~/.included/devbox-cert.pub\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(include) error = %v", err)
	}
	got := SSHConfigDependencyPaths(home)
	for _, want := range []string{filepath.Join(sshDir, "config"), include, filepath.Join(home, ".included", "devbox-cert.pub")} {
		if !containsCanonical(got, want) {
			t.Fatalf("dependencies = %#v, missing %s", got, want)
		}
	}
}
