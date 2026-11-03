package install

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestResolveGeneratesAndPersistsID(t *testing.T) {
	home := t.TempDir()
	id, err := ResolveInstallationID(home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if normalizeUUID(id) == "" {
		t.Fatalf("id = %q, want UUID", id)
	}
	data, err := os.ReadFile(filepath.Join(home, InstallationIDFilename))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != id {
		t.Fatalf("persisted = %q, want %q", data, id)
	}
}

func TestResolveReusesExistingUppercaseID(t *testing.T) {
	home := t.TempDir()
	existing := "A0B1C2D3-E4F5-4678-9234-ABCDEF123456"
	if err := os.WriteFile(filepath.Join(home, InstallationIDFilename), []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	id, err := ResolveInstallationID(home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if id != "a0b1c2d3-e4f5-4678-9234-abcdef123456" {
		t.Fatalf("id = %q", id)
	}
}

func TestResolveRewritesInvalidID(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, InstallationIDFilename), []byte("not-a-uuid"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	id, err := ResolveInstallationID(home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if id == "not-a-uuid" || normalizeUUID(id) == "" {
		t.Fatalf("id = %q, want generated UUID", id)
	}
}

// Concurrent starters must observe one installation ID: readers share a lock,
// and whoever upgrades to the exclusive lock rereads before generating
// (Rust #59021).
func TestResolveInstallationIDConcurrentStartersShareOneID(t *testing.T) {
	home := t.TempDir()
	const starters = 24
	ids := make([]string, starters)
	errs := make([]error, starters)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < starters; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			ids[index], errs[index] = ResolveInstallationID(home)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("starter %d: Resolve() error = %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Fatalf("starter %d resolved %q, want %q", i, ids[i], ids[0])
		}
	}
	data, err := os.ReadFile(filepath.Join(home, InstallationIDFilename))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != ids[0] {
		t.Fatalf("persisted = %q, want %q", data, ids[0])
	}
}

func TestResolveInstallationIDNormalizesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	home := t.TempDir()
	path := filepath.Join(home, InstallationIDFilename)
	existing := "a0b1c2d3-e4f5-4678-9234-abcdef123456"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := ResolveInstallationID(home); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %o, want 644", got)
	}
}
