package appserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Rust parity: codex-rs/core-plugins/src/remote/remote_installed_plugin_sync.rs
// (#61865): a same-version cache counts only when its identity receipt names
// the catalog plugin; a conflicting or unreadable receipt reinstalls and keeps
// the original receipt when the replacement download fails.

type receiptTestClient struct {
	calls int
	err   error
}

func (c *receiptTestClient) Do(request *http.Request) (*http.Response, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return nil, errors.New("receipt test client has no transport")
}

func writeReceiptTestCache(t *testing.T, home string, version string, receipt string, writeReceipt bool) (string, string) {
	t.Helper()
	pluginBase := filepath.Join(home, "plugins", "cache", remoteInstalledGlobalMarketplace, "linear")
	root := filepath.Join(pluginBase, version)
	manifestDir := filepath.Join(root, ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("create cached plugin: %v", err)
	}
	manifest := []byte(`{"name":"linear","version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if writeReceipt {
		if err := writeRemotePluginInstallMetadata(pluginBase, receipt); err != nil {
			t.Fatalf("write receipt: %v", err)
		}
	}
	return pluginBase, root
}

func receiptTestCandidate(version string, bundleURL string) remoteInstalledPlugin {
	candidate := remoteInstalledPlugin{ID: "plugins~Plugin_a", Name: "linear"}
	candidate.Release.Version = &version
	if bundleURL != "" {
		candidate.Release.BundleDownloadURL = &bundleURL
	}
	return candidate
}

func TestRemoteInstalledPluginReusesCacheOnlyWithMatchingReceiptLikeRust(t *testing.T) {
	home := t.TempDir()
	pluginBase, root := writeReceiptTestCache(t, home, "1.2.3", "plugins~Plugin_a", true)
	client := &receiptTestClient{}
	got, err := ensureRemoteInstalledPluginCache(context.Background(), client, home, remoteInstalledGlobalMarketplace, receiptTestCandidate("1.2.3", "https://example.invalid/bundle.tar.gz"), remotePluginArchiveSigningContext{})
	if err != nil {
		t.Fatalf("ensureRemoteInstalledPluginCache() error = %v", err)
	}
	if got != root {
		t.Fatalf("root = %q, want the cached root", got)
	}
	if client.calls != 0 {
		t.Fatalf("bundle downloads = %d, want the matching cache reused", client.calls)
	}
	if persisted, err := readRemotePluginInstallMetadata(pluginBase); err != nil || persisted != "plugins~Plugin_a" {
		t.Fatalf("receipt = (%q, %v)", persisted, err)
	}
}

func TestRemoteInstalledPluginMigratesReceiptlessCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	pluginBase, root := writeReceiptTestCache(t, home, "1.2.3", "", false)
	client := &receiptTestClient{}
	got, err := ensureRemoteInstalledPluginCache(context.Background(), client, home, remoteInstalledGlobalMarketplace, receiptTestCandidate("1.2.3", "https://example.invalid/bundle.tar.gz"), remotePluginArchiveSigningContext{})
	if err != nil {
		t.Fatalf("ensureRemoteInstalledPluginCache() error = %v", err)
	}
	if got != root || client.calls != 0 {
		t.Fatalf("root = %q calls = %d, want the migrated cache reused", got, client.calls)
	}
	if persisted, err := readRemotePluginInstallMetadata(pluginBase); err != nil || persisted != "plugins~Plugin_a" {
		t.Fatalf("receipt = (%q, %v), want the migrated identity", persisted, err)
	}
}

func TestRemoteInstalledPluginReinstallsConflictingReceiptLikeRust(t *testing.T) {
	home := t.TempDir()
	pluginBase, root := writeReceiptTestCache(t, home, "1.2.3", "plugins~Plugin_original", true)
	// Without a download URL the cache cannot be replaced, and the original
	// receipt must survive the failed attempt.
	client := &receiptTestClient{}
	if got, err := ensureRemoteInstalledPluginCache(context.Background(), client, home, remoteInstalledGlobalMarketplace, receiptTestCandidate("1.2.3", ""), remotePluginArchiveSigningContext{}); err != nil || got != root {
		t.Fatalf("ensureRemoteInstalledPluginCache() = (%q, %v), want the cached root", got, err)
	}
	if persisted, err := readRemotePluginInstallMetadata(pluginBase); err != nil || persisted != "plugins~Plugin_original" {
		t.Fatalf("receipt = (%q, %v), want the original identity", persisted, err)
	}
	// A download attempt is made once a URL exists; its failure keeps the
	// original receipt too.
	client.err = errors.New("download unavailable")
	if _, err := ensureRemoteInstalledPluginCache(context.Background(), client, home, remoteInstalledGlobalMarketplace, receiptTestCandidate("1.2.3", "https://example.invalid/bundle.tar.gz"), remotePluginArchiveSigningContext{}); err == nil {
		t.Fatal("ensureRemoteInstalledPluginCache() accepted a failed replacement download")
	}
	if client.calls != 1 {
		t.Fatalf("bundle downloads = %d, want one replacement attempt", client.calls)
	}
	if persisted, err := readRemotePluginInstallMetadata(pluginBase); err != nil || persisted != "plugins~Plugin_original" {
		t.Fatalf("receipt = (%q, %v), want the original identity after a failed download", persisted, err)
	}
}

func TestRemoteInstalledPluginReinstallsUnreadableReceiptLikeRust(t *testing.T) {
	home := t.TempDir()
	pluginBase, _ := writeReceiptTestCache(t, home, "1.2.3", "", false)
	if err := os.WriteFile(filepath.Join(pluginBase, remotePluginInstallMetadataFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt receipt: %v", err)
	}
	client := &receiptTestClient{err: errors.New("download unavailable")}
	if _, err := ensureRemoteInstalledPluginCache(context.Background(), client, home, remoteInstalledGlobalMarketplace, receiptTestCandidate("1.2.3", "https://example.invalid/bundle.tar.gz"), remotePluginArchiveSigningContext{}); err == nil {
		t.Fatal("ensureRemoteInstalledPluginCache() trusted an unreadable receipt")
	}
	if client.calls != 1 {
		t.Fatalf("bundle downloads = %d, want one replacement attempt", client.calls)
	}
}
