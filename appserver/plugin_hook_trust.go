package appserver

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errPluginContentsChanged mirrors Rust plugin_trust.rs's message raised when a
// plugin bundle no longer matches the approved contents.
var errPluginContentsChanged = errors.New("plugin contents changed; hook approval required")

// pluginHookTrust mirrors Rust hooks/src/engine/plugin_trust.rs::PluginHookTrust
// (codex-rs #54619). A plugin hook approval is bound to the plugin bundle
// contents: the recorded declaration hash keeps the older "same hook
// definition" approval valid, while the recorded content hash pins the bundle
// bytes. Discovery stores declarations and approvals without scanning bundles;
// every execution verifies fresh contents, so a changed bundle needs a new
// approval.
type pluginHookTrust struct {
	// Root is the plugin bundle root whose contents are fingerprinted.
	Root string
	// DeclarationHash is the normalized hook identity hash (Rust hook_hash).
	DeclarationHash string
	// TrustedHash is the approval recorded in config for this hook key.
	TrustedHash string
	// TrustedContentHash is the approved bundle content hash, when a client
	// recorded one (Rust HookStateToml::trusted_content_hash).
	TrustedContentHash *string
	// LegacyPath is the baseline file shared by legacy approvals of the same
	// plugin and config home; empty when no user config layer exists.
	LegacyPath string
}

// verify mirrors Rust PluginHookTrust::verify: fingerprint fresh contents and
// require them to match the approval, migrating a legacy baseline if needed.
func (t *pluginHookTrust) verify() error {
	if t == nil {
		return nil
	}
	contentHash, err := pluginContentHash(t.Root)
	if err != nil {
		return err
	}
	matches, err := t.matchesHash(contentHash)
	if err != nil {
		return err
	}
	if !matches {
		return errPluginContentsChanged
	}
	return nil
}

// matchesHash mirrors Rust PluginHookTrust::matches_hash, including the
// one-time legacy approval migration.
func (t *pluginHookTrust) matchesHash(contentHash string) (bool, error) {
	if t == nil {
		return false, nil
	}
	if t.TrustedHash != t.DeclarationHash {
		return false, nil
	}
	if t.TrustedContentHash != nil {
		if *t.TrustedContentHash != contentHash {
			return false, nil
		}
		if t.LegacyPath != "" {
			// A modern approval also pins the baseline for legacy siblings.
			if _, err := migrateLegacyTrust(t.LegacyPath, contentHash); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if t.LegacyPath != "" {
		baseline, err := migrateLegacyTrust(t.LegacyPath, contentHash)
		if err != nil {
			return false, err
		}
		return baseline == contentHash, nil
	}
	return false, nil
}

// pluginLegacyTrustPath mirrors Rust plugin_trust.rs::legacy_trust_path: the
// baseline lives in `<config parent>/.hook-trust/<hex>` where the digest binds
// the user config file and the plugin id, so every hook, profile, version and
// session of one plugin shares a single immutable baseline per config home.
func pluginLegacyTrustPath(configFile string, pluginID string) string {
	configFile = strings.TrimSpace(configFile)
	pluginID = strings.TrimSpace(pluginID)
	if configFile == "" || pluginID == "" {
		return ""
	}
	hasher := sha256.New()
	for _, part := range []string{configFile, pluginID} {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(part)))
		hasher.Write(size[:])
		hasher.Write([]byte(part))
	}
	return filepath.Join(filepath.Dir(configFile), ".hook-trust", hex.EncodeToString(hasher.Sum(nil)))
}

// migrateLegacyTrust mirrors Rust plugin_trust.rs::migrate_legacy_trust: the
// first legacy approval writes the current bundle hash as an immutable baseline
// (exclusive create, so a concurrent writer wins) and later approvals compare
// against it.
func migrateLegacyTrust(path string, currentHash string) (string, error) {
	existing, err := os.ReadFile(path)
	if err == nil {
		return string(existing), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	directory := filepath.Dir(path)
	if directory == "" {
		return "", errors.New("hook trust baseline has no parent")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".hook-trust-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	if _, err := temporary.WriteString(currentHash); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return "", err
	}
	if err := temporary.Close(); err != nil {
		os.Remove(temporaryPath)
		return "", err
	}
	// persist_noclobber semantics: an atomic exclusive link, so an already
	// written baseline wins instead of being overwritten.
	if err := os.Link(temporaryPath, path); err != nil {
		os.Remove(temporaryPath)
		if errors.Is(err, fs.ErrExist) {
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return "", readErr
			}
			return string(existing), nil
		}
		return "", err
	}
	os.Remove(temporaryPath)
	return currentHash, nil
}

// clone keeps the trust metadata safe to copy into a cloned HookMetadata.
func (t *pluginHookTrust) clone() *pluginHookTrust {
	if t == nil {
		return nil
	}
	cloned := *t
	if t.TrustedContentHash != nil {
		value := *t.TrustedContentHash
		cloned.TrustedContentHash = &value
	}
	return &cloned
}

func (t *pluginHookTrust) String() string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("pluginHookTrust(root=%s declaration=%s trusted=%s)", t.Root, t.DeclarationHash, t.TrustedHash)
}
