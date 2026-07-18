package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBundledExecutablePathForExeChecksResourceDir(t *testing.T) {
	tmp := t.TempDir()
	releaseDir := filepath.Join(tmp, "release")
	resourcesDir := filepath.Join(releaseDir, ResourcesDirname)
	if err := os.MkdirAll(resourcesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	exe := filepath.Join(releaseDir, "codex.exe")
	helper := filepath.Join(resourcesDir, "codex-command-runner.exe")
	if err := os.WriteFile(exe, []byte("codex"), 0o600); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	if err := os.WriteFile(helper, []byte("runner"), 0o600); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	if got := BundledExecutablePathForExe(exe, "codex-command-runner.exe"); got != helper {
		t.Fatalf("BundledExecutablePathForExe() = %q, want %q", got, helper)
	}
}

func TestBundledExecutablePathForExePrefersPackageResourcesForBinExe(t *testing.T) {
	tmp := t.TempDir()
	packageDir := filepath.Join(tmp, "package")
	binDir := filepath.Join(packageDir, BinDirname)
	resourcesDir := filepath.Join(packageDir, ResourcesDirname)
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.MkdirAll(resourcesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(resources) error = %v", err)
	}
	exe := filepath.Join(binDir, "codex.exe")
	helper := filepath.Join(resourcesDir, "codex-command-runner.exe")
	_ = os.WriteFile(exe, []byte("codex"), 0o600)
	_ = os.WriteFile(helper, []byte("runner"), 0o600)
	if got := BundledExecutablePathForExe(exe, "codex-command-runner.exe"); got != helper {
		t.Fatalf("BundledExecutablePathForExe() = %q, want %q", got, helper)
	}
}

func TestCopyFromSourceIfNeededCopiesAndReusesFreshDestination(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source.exe")
	destination := filepath.Join(tmp, "bin", "helper.exe")
	if err := os.WriteFile(source, []byte("runner-v1"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	outcome, err := CopyFromSourceIfNeeded(source, destination)
	if err != nil {
		t.Fatalf("CopyFromSourceIfNeeded() error = %v", err)
	}
	if outcome != CopyOutcomeReCopied {
		t.Fatalf("outcome = %s", outcome)
	}
	outcome, err = CopyFromSourceIfNeeded(source, destination)
	if err != nil {
		t.Fatalf("CopyFromSourceIfNeeded(reuse) error = %v", err)
	}
	if outcome != CopyOutcomeReused {
		t.Fatalf("reuse outcome = %s", outcome)
	}
}

func TestDestinationIsFreshUsesSizeAndMtime(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source.exe")
	destination := filepath.Join(tmp, "destination.exe")
	if err := os.WriteFile(destination, []byte("same-size"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination) error = %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(source, []byte("same-size"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	fresh, err := DestinationIsFresh(source, destination)
	if err != nil {
		t.Fatalf("DestinationIsFresh() error = %v", err)
	}
	if fresh {
		t.Fatalf("destination unexpectedly fresh")
	}
	if err := os.WriteFile(destination, []byte("same-size"), 0o600); err != nil {
		t.Fatalf("rewrite destination error = %v", err)
	}
	fresh, err = DestinationIsFresh(source, destination)
	if err != nil || !fresh {
		t.Fatalf("fresh = %v err = %v", fresh, err)
	}
}

func TestMaterializedFileNameAddsSuffixBeforeExtension(t *testing.T) {
	got := MaterializedFileName(HelperCommandRunner, "test-suffix")
	if got != "codex-command-runner-test-suffix.exe" {
		t.Fatalf("MaterializedFileName() = %q", got)
	}
}
