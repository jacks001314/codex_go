package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
