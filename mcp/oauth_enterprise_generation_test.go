package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestEnterpriseGenerationFileLifecycle covers Rust #43844's generation file:
// an empty file means no generation, replace() writes exactly one 32-byte
// generation, and the value is derived from the credential store key.
func TestEnterpriseGenerationFileLifecycle(t *testing.T) {
	home := t.TempDir()
	file, err := openMCPOAuthEnterpriseGenerationFile(home, "enterprise", "https://idp.example.com")
	if err != nil {
		t.Fatalf("open generation file: %v", err)
	}
	defer file.close()

	storeKey, err := computeMCPOAuthStoreKey(home, "enterprise", "https://idp.example.com")
	if err != nil {
		t.Fatalf("store key: %v", err)
	}
	sum := sha256.Sum256([]byte(storeKey))
	wantPath := filepath.Join(home, mcpOAuthCredentialLockDir, hex.EncodeToString(sum[:])+mcpOAuthEnterpriseGenerationExt)
	if file.path != wantPath {
		t.Fatalf("generation path = %q, want %q", file.path, wantPath)
	}
	if info, err := os.Stat(wantPath); err != nil {
		t.Fatalf("generation file missing: %v", err)
	} else if info.Size() != 0 {
		t.Fatalf("new generation file size = %d, want 0", info.Size())
	}

	if _, ok, err := file.current(); err != nil || ok {
		t.Fatalf("empty generation = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	first, err := file.replace()
	if err != nil {
		t.Fatalf("replace generation: %v", err)
	}
	current, ok, err := file.current()
	if err != nil || !ok {
		t.Fatalf("current generation = (ok=%v, err=%v), want present", ok, err)
	}
	if current != first {
		t.Fatalf("current generation %x != replaced %x", current, first)
	}
	if info, err := os.Stat(wantPath); err != nil || info.Size() != mcpOAuthEnterpriseGenerationSize {
		t.Fatalf("generation file size = %v (err=%v), want %d", info, err, mcpOAuthEnterpriseGenerationSize)
	}

	second, err := file.replace()
	if err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if second == first {
		t.Fatal("a replacement generation must differ so earlier attempts are invalidated")
	}
}

// TestEnterpriseGenerationFileCrossProcessInvalidation covers Rust #43844's
// coordination guarantee: another process (a second handle over the same path)
// sees the replacement and can reject a staged attempt.
func TestEnterpriseGenerationFileCrossProcessInvalidation(t *testing.T) {
	home := t.TempDir()
	attempt, err := openMCPOAuthEnterpriseGenerationFile(home, "enterprise", "https://idp.example.com")
	if err != nil {
		t.Fatalf("open attempt file: %v", err)
	}
	defer attempt.close()
	staged, err := attempt.replace()
	if err != nil {
		t.Fatalf("stage generation: %v", err)
	}

	// The "logout" process replaces the generation while holding its own handle.
	logout, err := openMCPOAuthEnterpriseGenerationFile(home, "enterprise", "https://idp.example.com")
	if err != nil {
		t.Fatalf("open logout file: %v", err)
	}
	defer logout.close()
	if _, err := logout.replace(); err != nil {
		t.Fatalf("logout replace: %v", err)
	}

	current, ok, err := attempt.current()
	if err != nil {
		t.Fatalf("read current generation: %v", err)
	}
	if !ok || current == staged {
		t.Fatalf("staged generation %x was not invalidated (ok=%v, current=%x)", staged, ok, current)
	}
}

// TestEnterpriseGenerationFileRejectsInvalidState covers the validation
// branches: a truncated/oversized file is invalid, and a non-regular path is
// refused.
func TestEnterpriseGenerationFileRejectsInvalidState(t *testing.T) {
	home := t.TempDir()
	file, err := openMCPOAuthEnterpriseGenerationFile(home, "enterprise", "https://idp.example.com")
	if err != nil {
		t.Fatalf("open generation file: %v", err)
	}
	path := file.path
	defer file.close()
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("write invalid file: %v", err)
	}
	if _, _, err := file.current(); err == nil {
		t.Fatal("a short generation file must be rejected")
	}
	_ = file.close()

	// A directory at the generation path is not a regular file.
	dirHome := t.TempDir()
	dirFile, err := openMCPOAuthEnterpriseGenerationFile(dirHome, "enterprise", "https://idp.example.com")
	if err != nil {
		t.Fatalf("open dir generation file: %v", err)
	}
	dirPath := dirFile.path
	_ = dirFile.close()
	if err := os.Remove(dirPath); err != nil {
		t.Fatalf("remove generation file: %v", err)
	}
	if err := os.Mkdir(dirPath, 0o700); err != nil {
		t.Fatalf("create directory at generation path: %v", err)
	}
	if _, err := openMCPOAuthEnterpriseGenerationFile(dirHome, "enterprise", "https://idp.example.com"); err == nil {
		t.Fatal("a non-regular generation path must be rejected")
	}
}
