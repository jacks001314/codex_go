package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"codex_go/cli"
	"codex_go/plugin"
)

// Mirrors Rust #42150: an explicitly selected remote marketplace lists catalog
// entries with source, version, install policy, and auth policy.
func TestPluginListRemoteCatalogLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ps/plugins/list" || request.URL.Query().Get("scope") != plugin.RemoteCatalogScopeGlobal {
			http.NotFound(w, request)
			return
		}
		if request.Header.Get("OAI-Product-Sku") != "tpp" {
			t.Errorf("product sku header = %q", request.Header.Get("OAI-Product-Sku"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plugins": []any{map[string]any{
				"id": "remote-sample", "name": "remote-sample", "scope": "GLOBAL",
				"installation_policy": "AVAILABLE", "authentication_policy": "ON_INSTALL", "status": "AVAILABLE",
				"release": map[string]any{"version": "1.4.0", "display_name": "Remote Sample", "description": "remote plugin"},
			}},
			"pagination": map[string]any{},
		})
	}))
	defer server.Close()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("chatgpt_base_url = \""+server.URL+"\"\napps_mcp_product_sku = \"tpp\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"token","account_id":"account"}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	ctx := newPluginCLIContext(home)
	var stdout bytes.Buffer
	if err := runPluginList(ctx, &cli.PluginOptions{
		Action:          "list",
		MarketplaceName: plugin.RemoteGlobalMarketplaceName,
		JSON:            true,
		Available:       true,
	}, &stdout); err != nil {
		t.Fatalf("runPluginList() error = %v", err)
	}
	var output pluginListJSONOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("Unmarshal() error = %v\n%s", err, stdout.String())
	}
	if len(output.Available) != 1 {
		t.Fatalf("available = %#v", output.Available)
	}
	entry := output.Available[0]
	if entry.PluginID != "remote-sample@openai-curated-remote" || entry.Version == nil || *entry.Version != "1.4.0" ||
		entry.InstallPolicy != plugin.InstallAllowed || entry.AuthPolicy != plugin.AuthOnInstall || entry.Source.Type != "remote" {
		t.Fatalf("remote entry = %#v", entry)
	}
}

func TestPluginRemoteAddAndRemoveLikeRust(t *testing.T) {
	t.Setenv("CODEX_TEST_ALLOW_HTTP_REMOTE_PLUGIN_BUNDLE_DOWNLOADS", "1")
	bundle := pluginCLIRemoteBundleForTest(t, "remote-sample")
	var installed, uninstalled bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/ps/plugins") && request.Header.Get("OAI-Product-Sku") != "tpp" {
			t.Errorf("product sku header for %s = %q", request.URL.Path, request.Header.Get("OAI-Product-Sku"))
		}
		switch {
		case request.URL.Path == "/ps/plugins/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plugins": []any{map[string]any{
					"id": "remote-1", "name": "remote-sample", "scope": "GLOBAL",
					"installation_policy": "AVAILABLE", "authentication_policy": "ON_USE", "status": "AVAILABLE",
					"release": map[string]any{"version": "1.0.0", "display_name": "Remote Sample", "description": "remote"},
				}},
				"pagination": map[string]any{},
			})
		case request.URL.Path == "/ps/plugins/remote-1/install":
			installed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "remote-1", "enabled": true, "app_ids_needing_auth": []string{}})
		case request.URL.Path == "/ps/plugins/remote-1" && request.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "remote-1", "name": "remote-sample", "scope": "GLOBAL",
				"installation_policy": "AVAILABLE", "authentication_policy": "ON_USE", "status": "AVAILABLE",
				"release": map[string]any{
					"version": "1.0.0", "display_name": "Remote Sample", "description": "remote",
					"bundle_download_url": server.URL + "/bundle.tar.gz", "app_manifest": map[string]any{},
				},
			})
		case request.URL.Path == "/bundle.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(bundle)
		case request.URL.Path == "/ps/plugins/remote-1/uninstall":
			uninstalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "remote-1", "enabled": false})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("chatgpt_base_url = \""+server.URL+"\"\napps_mcp_product_sku = \"tpp\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"token","account_id":"account"}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	ctx := newPluginCLIContext(home)
	var stdout bytes.Buffer
	if err := runPluginAdd(ctx, &cli.PluginOptions{
		Action: "add", Plugin: "remote-sample@openai-curated-remote",
	}, &stdout); err != nil {
		t.Fatalf("runPluginAdd() error = %v", err)
	}
	if !installed || !strings.Contains(stdout.String(), "Added plugin `remote-sample`") {
		t.Fatalf("installed=%v stdout=%q", installed, stdout.String())
	}
	stdout.Reset()
	if err := runPluginRemove(ctx, &cli.PluginOptions{
		Action: "remove", Plugin: "remote-sample@openai-curated-remote",
	}, &stdout); err != nil {
		t.Fatalf("runPluginRemove() error = %v", err)
	}
	if !uninstalled {
		t.Fatal("remote uninstall mutation was not called")
	}
}

type pluginListFailingWriter struct {
	err error
}

func (w pluginListFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

// Mirrors Rust #53109: a consumer closing stdout early must not turn a
// successful plugin listing into a CLI error, while other write failures
// still propagate.
func TestPluginListBrokenPipeLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	root := writePluginCLITestMarketplace(t, t.TempDir(), "debug", "sample")
	if err := Run(context.Background(), []string{"plugin", "marketplace", "add", root}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("marketplace add error = %v", err)
	}
	ctx := newPluginCLIContext(home)
	options := &cli.PluginOptions{Action: "list", MarketplaceName: "debug", JSON: true}
	if err := runPluginList(ctx, options, pluginListFailingWriter{err: syscall.EPIPE}); err != nil {
		t.Fatalf("broken pipe error = %v", err)
	}
	if err := runPluginList(ctx, options, pluginListFailingWriter{err: fmt.Errorf("boom")}); err == nil {
		t.Fatal("non-pipe write error was swallowed")
	}
}

func pluginCLIRemoteBundleForTest(t *testing.T, pluginName string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	archive := tar.NewWriter(gzipWriter)
	files := map[string]string{
		".codex-plugin/plugin.json": fmt.Sprintf(`{"name":%q}`, pluginName),
		"skills/remote/SKILL.md":    "---\nname: remote\ndescription: Remote skill\n---\n\n# Body\n",
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		contents := []byte(files[path])
		if err := archive.WriteHeader(&tar.Header{Name: path, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("WriteHeader(%s) error = %v", path, err)
		}
		if _, err := archive.Write(contents); err != nil {
			t.Fatalf("Write(%s) error = %v", path, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("Close(tar) error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("Close(gzip) error = %v", err)
	}
	return output.Bytes()
}

// TestPluginMarketplaceUpgradeUsesConfigOverridesLikeRust covers Rust #42149's
// CLI path: a -c override can define the Git marketplace selected for upgrade,
// and the loader used for selection also verifies the definition before
// activation without writing it into the user's config.toml.
func TestPluginMarketplaceUpgradeUsesConfigOverridesLikeRust(t *testing.T) {
	home := shortCLITempDir(t)
	t.Setenv("CODEX_HOME", home)
	source := initPluginCLIGitMarketplace(t)
	override := fmt.Sprintf(`marketplaces.debug={source_type="git",source=%q}`, source)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run(context.Background(), []string{"-c", override, "plugin", "marketplace", "upgrade", "--json"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("marketplace upgrade returned error: %v; stdout = %s; stderr = %s", err, stdout.String(), stderr.String())
	}
	var output struct {
		SelectedMarketplaces []string `json:"selectedMarketplaces"`
		UpgradedRoots        []string `json:"upgradedRoots"`
		Errors               []any    `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("Unmarshal upgrade output error = %v; stdout = %s", err, stdout.String())
	}
	root := filepath.Join(home, plugin.InstalledMarketplacesDir, "debug")
	if len(output.SelectedMarketplaces) != 1 || output.SelectedMarketplaces[0] != "debug" {
		t.Fatalf("selected = %#v", output.SelectedMarketplaces)
	}
	if len(output.UpgradedRoots) != 1 || filepath.Clean(output.UpgradedRoots[0]) != root {
		t.Fatalf("roots = %#v, want %q", output.UpgradedRoots, root)
	}
	if len(output.Errors) != 0 {
		t.Fatalf("errors = %#v", output.Errors)
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".agents", "plugins", "marketplace.json"))
	if err != nil || string(manifest) != `{"name":"debug","plugins":[]}` {
		t.Fatalf("installed manifest = %q, %v", manifest, err)
	}
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("upgrade wrote config.toml, stat error = %v", err)
	}
}

// shortCLITempDir keeps Windows marketplace staging paths below legacy path
// limits while the test exercises a real git clone.
func shortCLITempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func initPluginCLIGitMarketplace(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".agents", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".agents", "plugins", "marketplace.json"), []byte(`{"name":"debug","plugins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "."},
		{"-c", "user.name=Codex Tests", "-c", "user.email=codex@example.com", "commit", "-m", "marketplace"},
	} {
		command := exec.Command("git", args...)
		command.Dir = source
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, output)
		}
	}
	return source
}

func TestPluginMarketplaceLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	marketplaceRoot := writePluginCLITestMarketplace(t, t.TempDir(), "debug", "sample")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"plugin", "marketplace", "add", marketplaceRoot}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("marketplace add returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Added marketplace `debug`") || !strings.Contains(stdout.String(), marketplaceRoot) {
		t.Fatalf("add stdout = %q", stdout.String())
	}
	configData, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile config.toml error = %v", err)
	}
	if !strings.Contains(string(configData), "[marketplaces.debug]") || strings.Contains(string(configData), "plugins.json") {
		t.Fatalf("config.toml = %q", string(configData))
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "marketplace", "list", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("marketplace list returned error: %v", err)
	}
	var listJSON struct {
		Marketplaces []struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &listJSON); err != nil {
		t.Fatalf("Unmarshal marketplace list error = %v; stdout = %s", err, stdout.String())
	}
	if len(listJSON.Marketplaces) != 1 || listJSON.Marketplaces[0].Name != "debug" || listJSON.Marketplaces[0].Root != marketplaceRoot {
		t.Fatalf("list JSON = %#v", listJSON)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "marketplace", "upgrade"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("marketplace upgrade returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No configured Git marketplaces to upgrade.") {
		t.Fatalf("upgrade stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "marketplace", "remove", "debug"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("marketplace remove returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed marketplace `debug`.") {
		t.Fatalf("remove stdout = %q", stdout.String())
	}
}

func TestPluginAddListRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	marketplaceRoot := writePluginCLITestMarketplace(t, t.TempDir(), "debug", "sample")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"plugin", "marketplace", "add", marketplaceRoot}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("marketplace add returned error: %v", err)
	}
	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "add", "sample@debug", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("plugin add returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"pluginId": "sample@debug"`) || !strings.Contains(stdout.String(), `"version": "local"`) {
		t.Fatalf("add stdout = %q", stdout.String())
	}
	configData, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile config.toml error = %v", err)
	}
	if !strings.Contains(string(configData), "[plugins.'sample@debug']") || strings.Contains(string(configData), "plugins.json") {
		t.Fatalf("config.toml after plugin add = %q", string(configData))
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("plugin list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "sample@debug") || !strings.Contains(stdout.String(), "installed, enabled") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "remove", "sample@debug"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("plugin remove returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed plugin `sample` from marketplace `debug`.") {
		t.Fatalf("remove stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, "plugins.json")); !os.IsNotExist(err) {
		t.Fatalf("plugins.json should not be used, stat error = %v", err)
	}
}

func TestPluginSelectionValidatesSegments(t *testing.T) {
	valid, err := parsePluginSelection("sample-1@debug_2", "")
	if err != nil {
		t.Fatalf("parsePluginSelection valid returned error: %v", err)
	}
	if valid.PluginID != "sample-1@debug_2" {
		t.Fatalf("valid selection = %#v", valid)
	}

	for _, tc := range []struct {
		name        string
		plugin      string
		marketplace string
		want        string
	}{
		{
			name:   "plugin key plugin segment",
			plugin: "bad/name@debug",
			want:   "invalid plugin name: only ASCII letters, digits, `_`, and `-` are allowed in `bad/name@debug`",
		},
		{
			name:   "plugin key marketplace segment",
			plugin: "sample@bad/name",
			want:   "invalid marketplace name: only ASCII letters, digits, `_`, and `-` are allowed in `sample@bad/name`",
		},
		{
			name:        "separate plugin segment",
			plugin:      "bad/name",
			marketplace: "debug",
			want:        "invalid plugin name: only ASCII letters, digits, `_`, and `-` are allowed",
		},
		{
			name:        "separate marketplace segment",
			plugin:      "sample",
			marketplace: "bad/name",
			want:        "invalid marketplace name: only ASCII letters, digits, `_`, and `-` are allowed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePluginSelection(tc.plugin, tc.marketplace)
			if err == nil {
				t.Fatal("parsePluginSelection returned nil error, want validation failure")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("error = %q, want %q", got, tc.want)
			}
		})
	}
}

func writePluginCLITestMarketplace(t *testing.T, parent string, marketplaceName string, pluginName string) string {
	t.Helper()
	root := filepath.Join(parent, marketplaceName)
	pluginRoot := filepath.Join(root, "plugins", pluginName)
	if err := os.MkdirAll(filepath.Join(root, ".agents", "plugins"), 0o700); err != nil {
		t.Fatalf("MkdirAll marketplace manifest dir error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o700); err != nil {
		t.Fatalf("MkdirAll plugin manifest dir error = %v", err)
	}
	marketplaceManifest := `{
  "name": "` + marketplaceName + `",
  "plugins": [
    {
      "name": "` + pluginName + `",
      "source": { "source": "local", "path": "plugins/` + pluginName + `" }
    }
  ]
}
`
	if err := os.WriteFile(filepath.Join(root, ".agents", "plugins", "marketplace.json"), []byte(marketplaceManifest), 0o600); err != nil {
		t.Fatalf("WriteFile marketplace manifest error = %v", err)
	}
	pluginManifest := `{
  "name": "` + pluginName + `",
  "displayName": "` + pluginName + `",
  "description": "Test plugin"
}
`
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(pluginManifest), 0o600); err != nil {
		t.Fatalf("WriteFile plugin manifest error = %v", err)
	}
	return filepath.Clean(root)
}
