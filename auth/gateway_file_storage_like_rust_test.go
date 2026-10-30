package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestGatewayCredentialsFileStoreLikeRust covers Rust #58219: a gateway may
// store its OAuth grants in <codex_home>/secrets/gateway_oauth.json instead of
// the keyring namespace, and the file store keeps independent entries.
func TestGatewayCredentialsFileStoreLikeRust(t *testing.T) {
	home := t.TempDir()
	storage := newGatewayAuthStorage(home, nil, "file")
	path := filepath.Join(home, "secrets", "gateway_oauth.json")
	if storage.filePath != path {
		t.Fatalf("file store path = %q, want %q", storage.filePath, path)
	}
	first := "provider-oauth|aaaa"
	second := "provider-oauth|bbbb"
	if _, found, err := storage.load(first); err != nil || found {
		t.Fatalf("load(empty) = %v %v", found, err)
	}
	if err := storage.save(first, `{"access_token":"one"}`); err != nil {
		t.Fatalf("save() error = %v", err)
	}
	if err := storage.save(second, `{"access_token":"two"}`); err != nil {
		t.Fatalf("save() error = %v", err)
	}
	value, found, err := storage.load(first)
	if err != nil || !found || value != `{"access_token":"one"}` {
		t.Fatalf("load(first) = %q %v %v", value, found, err)
	}
	if value, found, err := storage.load(second); err != nil || !found || value != `{"access_token":"two"}` {
		t.Fatalf("load(second) = %q %v %v", value, found, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	entries := map[string]string{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("stored file is not a credential map: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("stored entries = %#v", entries)
	}
	// Windows ignores POSIX permission bits on create; the Rust store has the
	// same platform limitation, so only assert the mode where it is honored.
	if runtime.GOOS != "windows" {
		if mode := fileMode(t, path); mode != 0o600 {
			t.Fatalf("file mode = %o, want 0600", mode)
		}
	}

	if removed, err := storage.delete(first); err != nil || !removed {
		t.Fatalf("delete(first) = %v %v", removed, err)
	}
	if removed, err := storage.delete(first); err != nil || removed {
		t.Fatalf("delete(first) again = %v %v", removed, err)
	}
	if _, found, err := storage.load(first); err != nil || found {
		t.Fatalf("load after delete = %v %v", found, err)
	}

	// A corrupt store reports a bounded error instead of leaking its contents.
	if err := os.WriteFile(path, []byte(`{"provider-oauth|aaaa":{"access_token":`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := storage.load(first); err == nil || err.Error() != "failed to load provider OAuth credentials" {
		t.Fatalf("load(corrupt) error = %v", err)
	}
	if err := storage.save(first, "value"); err == nil || err.Error() != "failed to save provider OAuth credentials" {
		t.Fatalf("save(corrupt) error = %v", err)
	}

	// An invalid credential id is rejected before touching the store.
	if _, _, err := storage.load("bad-id"); err == nil {
		t.Fatal("load(invalid id) must fail")
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	return info.Mode().Perm()
}
