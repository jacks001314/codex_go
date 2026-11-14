package appserver

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/envutil"
	"codex_go/plugin"
)

// A marketplace entry whose plugin source is a Git subdirectory must
// materialize through the real Git materializer when the TUI reads it, not only
// through a test double (upstream #61129's configured marketplace path).
func TestPluginReadMaterializesConfiguredGitSubdirLikeRust(t *testing.T) {
	git, ok := envutil.TrustedExecutable("git")
	if !ok {
		t.Skip("trusted Git unavailable")
	}
	repo := t.TempDir()
	pluginRoot := filepath.Join(repo, "plugin")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"remote-sample","description":"git subdir plugin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "SKILL.md"), []byte("---\nname: remote-sample\ndescription: sample\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	runGit("init")
	runGit("add", ".")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	path := filepath.ToSlash(repo)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	sourceURL := (&url.URL{Scheme: "file", Path: path}).String()

	marketplaceRoot := t.TempDir()
	manifestDir := filepath.Join(marketplaceRoot, ".agents", "plugins")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"name": "debug",
		"plugins": []any{map[string]any{
			"name": "remote-sample",
			"source": map[string]any{
				"source": "git",
				"url":    sourceURL,
				"path":   "plugin",
			},
		}},
	}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(manifestDir, "marketplace.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	home, installRoot := t.TempDir(), t.TempDir()
	service := plugin.NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceInstallRoot(installRoot)
	service.SetMarketplacePluginMaterializer(&plugin.GitMarketplaceMaterializer{Automatic: true, CodexHome: home})
	if _, err := service.AddMarketplace(&plugin.MarketplaceAddParams{Name: "debug", Source: marketplaceRoot}); err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}
	destination := filepath.Join(installRoot, plugin.InstalledMarketplacePluginsDir, "debug", "remote-sample")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("plugin should be materialized lazily on read, stat error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Plugins: service})
	defer closeNotificationTestRouter(t, router)
	response := router.Handle(requestWithParams(t, IntID(1), MethodPluginRead, plugin.PluginReadParams{
		MarketplacePath: marketplaceRoot,
		PluginName:      "remote-sample",
	}))
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	read := response.Result.(*plugin.PluginReadResponse)
	if read.Plugin.ManifestPath != filepath.Join(destination, "plugin", ".codex-plugin", "plugin.json") {
		t.Fatalf("manifest path = %q", read.Plugin.ManifestPath)
	}
	if !read.Plugin.Summary.HasSkills {
		t.Fatalf("materialized plugin lost skills: %#v", read.Plugin.Summary)
	}
}

// Rust #61129's first full plugin/list case: configured git-subdir plugins are
// refreshed through the real materializer, and a release that kept its version
// must not be reinstalled while a new version is.
func TestFirstConfiguredPluginRefreshKeepsSameGitSubdirVersion(t *testing.T) {
	git, ok := envutil.TrustedExecutable("git")
	if !ok {
		t.Skip("trusted Git unavailable")
	}
	base, err := os.MkdirTemp("", "pgr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	home, installRoot, repo := filepath.Join(base, "h"), filepath.Join(base, "c"), filepath.Join(base, "r")
	for _, dir := range []string{home, installRoot, repo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeRelease := func(name, version, instructions string) {
		t.Helper()
		root := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
			t.Fatal(err)
		}
		manifest, _ := json.Marshal(map[string]string{"name": name, "version": version})
		if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), manifest, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "skills", "deploy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "skills", "deploy", "SKILL.md"), []byte(instructions), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init")
	names := []string{"docs", "release-guide"}
	for _, name := range names {
		writeRelease(name, "1.0.0", "production")
	}
	runGit("add", ".")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial release")
	runGit("tag", "-f", "stable")
	path := filepath.ToSlash(repo)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	sourceURL := (&url.URL{Scheme: "file", Path: path}).String()
	ref := "stable"
	service := plugin.NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceInstallRoot(installRoot)
	materializer := &plugin.GitMarketplaceMaterializer{Automatic: true, CodexHome: home}
	service.SetMarketplacePluginMaterializer(materializer)

	roots := map[string]string{}
	for _, name := range names {
		destination := filepath.Join(installRoot, plugin.InstalledMarketplacePluginsDir, "team", name)
		if err := materializer.MaterializeMarketplacePlugin(&plugin.ParsedMarketplaceSource{
			Kind: plugin.MarketplaceSourceGit, URL: sourceURL, RefName: &ref,
		}, destination); err != nil {
			t.Fatal(err)
		}
		roots[name] = filepath.Join(destination, name)
	}

	writeRelease("docs", "1.0.0", "staging")
	writeRelease("release-guide", "1.1.0", "staging")
	runGit("add", ".")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "publish")
	runGit("tag", "-f", "stable")

	catalog := t.TempDir()
	plugins := map[string]any{}
	for _, name := range names {
		plugins[name+"@team"] = map[string]any{
			"installed":        true,
			"enabled":          true,
			"name":             name,
			"marketplace":      "team",
			"path":             roots[name],
			"marketplace_path": catalog,
			"source_type":      "git",
			"source":           sourceURL,
			"ref":              ref,
		}
	}
	values := map[string]any{"plugins": plugins}
	reload := func() (map[string]any, error) { return values, nil }
	changed := make(chan struct{})
	if !service.StartConfiguredPluginCacheRefresh(values, false, reload, func() { close(changed) }) {
		t.Fatal("configured Git subdirectory refreshes were not scheduled")
	}
	select {
	case <-changed:
	case <-time.After(20 * time.Second):
		t.Fatal("configured Git subdirectory refresh did not finish")
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(installRoot, plugin.InstalledMarketplacePluginsDir, "team", name, name, "skills", "deploy", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := read("release-guide"); got != "staging" {
		t.Fatalf("new release instructions = %q", got)
	}
	if got := read("docs"); got != "production" {
		t.Fatalf("same-version release was reinstalled: %q", got)
	}
	for _, detail := range service.InstalledDetails() {
		if detail.Summary.ID == "release-guide@team" {
			if detail.Summary.Version == nil || *detail.Summary.Version != "1.1.0" {
				t.Fatalf("refreshed catalog version = %v", detail.Summary.Version)
			}
			return
		}
	}
	t.Fatal("refreshed release-guide was not added to the plugin catalog")
}
