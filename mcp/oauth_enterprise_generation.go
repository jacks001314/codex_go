package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rust parity: codex-rs/rmcp-client/src/oauth/enterprise_generation.rs (#43844).
// Persistent, non-secret logout generations for staged enterprise logins. All
// reads and writes require the credential lock; a missing generation never
// admits an old attempt.
const (
	mcpOAuthEnterpriseGenerationSize = 32
	mcpOAuthEnterpriseGenerationExt  = ".enterprise-generation"
)

// mcpOAuthEnterpriseGeneration is a random 256-bit logout generation. It is
// never a credential and is only ever compared for equality.
type mcpOAuthEnterpriseGeneration [mcpOAuthEnterpriseGenerationSize]byte

// mcpOAuthEnterpriseGenerationFile owns the open generation file. The caller
// must hold the matching credential lock for the whole lifetime of the handle.
type mcpOAuthEnterpriseGenerationFile struct {
	path string
	file *os.File
}

// openMCPOAuthEnterpriseGenerationFile opens (creating when absent) the
// generation file for one credential, mirroring Rust
// EnterpriseOAuthGenerationFile::open. The name is the SHA-256 of the
// credential's store key so no credential material reaches the path.
func openMCPOAuthEnterpriseGenerationFile(codexHome string, credentialName string, issuer string) (*mcpOAuthEnterpriseGenerationFile, error) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, errors.New("failed to open enterprise login generation: CODEX_HOME is required")
	}
	storeKey, err := computeMCPOAuthStoreKey(codexHome, credentialName, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to open enterprise login generation: %w", err)
	}
	sum := sha256.Sum256([]byte(storeKey))
	name := hex.EncodeToString(sum[:]) + mcpOAuthEnterpriseGenerationExt
	dir := filepath.Join(codexHome, mcpOAuthCredentialLockDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to open enterprise login generation: %w", err)
	}
	path := filepath.Join(dir, name)
	// Rust refuses to follow a symlink/reparse point for this file. Go cannot
	// open with O_NOFOLLOW portably, so reject an existing non-regular entry
	// before opening and re-verify the opened file.
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("invalid enterprise generation file")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open enterprise login generation: %w", err)
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("invalid enterprise generation file")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to secure enterprise login generation: %w", err)
	}
	return &mcpOAuthEnterpriseGenerationFile{path: path, file: file}, nil
}

// current returns the stored generation, or false when the file is empty.
// A file that is neither empty nor exactly one generation is invalid.
func (f *mcpOAuthEnterpriseGenerationFile) current() (mcpOAuthEnterpriseGeneration, bool, error) {
	var generation mcpOAuthEnterpriseGeneration
	if f == nil || f.file == nil {
		return generation, false, errors.New("invalid enterprise generation file")
	}
	if _, err := f.file.Seek(0, 0); err != nil {
		return generation, false, err
	}
	info, err := f.file.Stat()
	if err != nil {
		return generation, false, err
	}
	switch info.Size() {
	case 0:
		return generation, false, nil
	case mcpOAuthEnterpriseGenerationSize:
		if _, err := readFullAt(f.file, generation[:]); err != nil {
			return generation, false, err
		}
		return generation, true, nil
	default:
		return generation, false, errors.New("invalid enterprise generation file")
	}
}

// replace writes a fresh random generation, returning it. Random
// initialization also prevents an old attempt becoming valid if metadata is
// removed or truncated.
func (f *mcpOAuthEnterpriseGenerationFile) replace() (mcpOAuthEnterpriseGeneration, error) {
	var generation mcpOAuthEnterpriseGeneration
	if f == nil || f.file == nil {
		return generation, errors.New("invalid enterprise generation file")
	}
	random := make([]byte, mcpOAuthEnterpriseGenerationSize)
	if _, err := rand.Read(random); err != nil {
		return generation, err
	}
	// Rust hashes the ASCII secret returned by CsrfToken::new_random_len, which
	// is unpadded base64url over 32 random bytes.
	secret := base64.RawURLEncoding.EncodeToString(random)
	generation = sha256.Sum256([]byte(secret))
	if err := f.file.Truncate(0); err != nil {
		return generation, err
	}
	if _, err := f.file.Seek(0, 0); err != nil {
		return generation, err
	}
	if _, err := f.file.Write(generation[:]); err != nil {
		return generation, err
	}
	if err := f.file.Sync(); err != nil {
		return generation, err
	}
	return generation, nil
}

// close releases the open generation file.
func (f *mcpOAuthEnterpriseGenerationFile) close() error {
	if f == nil || f.file == nil {
		return nil
	}
	return f.file.Close()
}

func readFullAt(file *os.File, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		n, err := file.Read(buffer[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, errors.New("unexpected end of enterprise generation file")
		}
	}
	return total, nil
}
