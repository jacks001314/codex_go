package appserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// pluginFingerprintBufferSize mirrors Rust plugin_fingerprint.rs's 64 KiB read
// buffer (hooks/src/engine/plugin_fingerprint.rs).
const pluginFingerprintBufferSize = 64 * 1024

// errPluginFingerprintCancelled mirrors Rust's io::ErrorKind::Interrupted
// cancellation signal raised between entries and between bounded reads.
var errPluginFingerprintCancelled = errors.New("plugin fingerprint cancelled")

// pluginContentHash mirrors Rust hooks/src/engine/plugin_fingerprint.rs::
// plugin_content_hash (codex-rs #54619 "Bind plugin hook approval to plugin
// contents"). It is the best-effort source identity of an installed plugin
// bundle: bundle entries are hashed with a fixed domain tag, length-prefixed
// relative paths, a directory/file discriminator and length-prefixed file
// contents, while generated Python bytecode, virtual environments and package
// metadata are skipped so runtime artifacts cannot invalidate an approval.
//
// The traversal mirrors the Rust walker exactly so a Rust-computed bundle hash
// validates in Go for the same tree:
//   - a LIFO work stack seeded with the plugin root (the root is hashed first
//     with the empty relative path),
//   - directory entries are sorted by file name and pushed in reverse so they
//     pop in ascending name order,
//   - each file read is bounded to the observed length plus one byte so growth
//     or truncation is detected instead of trusting a partial fingerprint.
//
// Cancellation, when provided, is checked between entries and between bounded
// reads. A nil cancel func never cancels (Rust listing mode passes `&|| false`).
func pluginContentHash(root string) (string, error) {
	return pluginContentHashWithCancel(root, nil)
}

func pluginContentHashWithCancel(root string, cancelled func() bool) (string, error) {
	check := func() error {
		if cancelled != nil && cancelled() {
			return errPluginFingerprintCancelled
		}
		return nil
	}
	if err := check(); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("plugin root is not a directory")
	}

	hasher := sha256.New()
	hasher.Write([]byte("codex-plugin-bundle-v1"))
	pending := []string{root}
	buffer := make([]byte, pluginFingerprintBufferSize)
	for len(pending) > 0 {
		if err := check(); err != nil {
			return "", err
		}
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		relativePath := ""
		if path != root {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return "", err
			}
			relativePath = relative
		}
		base := ""
		if relativePath != "" {
			base = filepath.Base(relativePath)
		}
		if isSkippedPluginBundleEntry(base) {
			continue
		}

		hasher.Write(lengthPrefixedBytes(relativePath))
		metadata, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		switch {
		case metadata.IsDir():
			hasher.Write([]byte("directory"))
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", err
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				if err := check(); err != nil {
					return "", err
				}
				names = append(names, entry.Name())
			}
			sort.Strings(names)
			for index := len(names) - 1; index >= 0; index-- {
				pending = append(pending, filepath.Join(path, names[index]))
			}
		case metadata.Mode().IsRegular():
			hasher.Write([]byte("file"))
			length := metadata.Size()
			hasher.Write(littleEndianUint64(uint64(length)))
			if err := check(); err != nil {
				return "", err
			}
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			var bytesRead int64
			reader := io.LimitReader(file, length+1)
			for {
				if err := check(); err != nil {
					file.Close()
					return "", err
				}
				count, readErr := reader.Read(buffer)
				if count > 0 {
					bytesRead += int64(count)
					hasher.Write(buffer[:count])
				}
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						file.Close()
						return "", readErr
					}
					break
				}
				if count == 0 {
					break
				}
			}
			file.Close()
			if bytesRead != length {
				return "", errors.New("plugin file changed while fingerprinting")
			}
		default:
			return "", fmt.Errorf("unsupported plugin entry type: %s", path)
		}
	}
	if err := check(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// isSkippedPluginBundleEntry mirrors the Rust skip predicate: generated Python
// bytecode directories and files, virtual environments and package metadata.
func isSkippedPluginBundleEntry(name string) bool {
	switch name {
	case "__pycache__", ".venv":
		return true
	}
	return strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".egg-info")
}

func lengthPrefixedBytes(value string) []byte {
	var out bytes.Buffer
	out.Write(littleEndianUint64(uint64(len(value))))
	out.WriteString(value)
	return out.Bytes()
}

func littleEndianUint64(value uint64) []byte {
	var out [8]byte
	binary.LittleEndian.PutUint64(out[:], value)
	return out[:]
}
