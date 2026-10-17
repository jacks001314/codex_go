package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestExecutableIdentityFromReaderMatchesWholeFileLikeRust mirrors Rust #47591:
// streaming the digest is compatible with whole-file hashing for content that
// spans several reads (including a partial final buffer) and for an empty file,
// and changing a byte changes the identity.
func TestExecutableIdentityFromReaderMatchesWholeFileLikeRust(t *testing.T) {
	payload := make([]byte, 200_003)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	for _, contents := range [][]byte{payload, {}} {
		streamed, err := ExecutableIdentityFromReader(bytes.NewReader(contents))
		if err != nil {
			t.Fatalf("ExecutableIdentityFromReader() error = %v", err)
		}
		if want := ExecutableIdentityFromBytes(contents); streamed != want {
			t.Fatalf("streamed identity = %#v, want the whole-file identity %#v", streamed, want)
		}
	}

	changed := append([]byte(nil), payload...)
	changed[100_000] ^= 1
	original, _ := ExecutableIdentityFromReader(bytes.NewReader(payload))
	modified, _ := ExecutableIdentityFromReader(bytes.NewReader(changed))
	if original == modified {
		t.Fatal("changing a byte must change the executable identity")
	}
}

// TestExecutableIdentityFromFileStreamsLikeRust pins that the file helper uses
// the same digest as the reader/bytes helpers while adding the path digest.
func TestExecutableIdentityFromFileStreamsLikeRust(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "codex")
	payload := bytes.Repeat([]byte("codex-binary-"), 20_000)
	if err := os.WriteFile(executable, payload, 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fromFile, err := ExecutableIdentityFromFile(executable)
	if err != nil {
		t.Fatalf("ExecutableIdentityFromFile() error = %v", err)
	}
	if fromFile.Digest != ExecutableIdentityFromBytes(payload).Digest {
		t.Fatalf("file digest = %q, want the whole-file digest", fromFile.Digest)
	}
	if fromFile.PathDigest == "" {
		t.Fatal("file identity is missing the path digest")
	}
}
