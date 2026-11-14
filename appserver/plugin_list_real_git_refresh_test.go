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

func TestFirstPluginListRefreshesRealGitVersions(t *testing.T) {
	git, ok := envutil.TrustedExecutable("git")
	if !ok {
		t.Skip("trusted Git unavailable")
	}
	home, cache := t.TempDir(), t.TempDir()
	service := plugin.NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceInstallRoot(cache)
	materializer := &plugin.GitMarketplaceMaterializer{Automatic: true, CodexHome: home}
	service.SetMarketplacePluginMaterializer(materializer)
	run := func(repo string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(root, name, version, marker string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0755); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]string{"name": name, "version": version})
		if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "marker"), []byte(marker), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"docs", "release"} {
		repo := t.TempDir()
		run(repo, "init")
		write(repo, name, "1.0.0", "production")
		run(repo, "add", ".")
		run(repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
		path := filepath.ToSlash(repo)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		sourceURL := (&url.URL{Scheme: "file", Path: path}).String()
		destination := filepath.Join(cache, plugin.InstalledMarketplacePluginsDir, "team", name)
		if err := materializer.MaterializeMarketplacePlugin(&plugin.ParsedMarketplaceSource{Kind: plugin.MarketplaceSourceGit, URL: sourceURL}, destination); err != nil {
			t.Fatal(err)
		}
		service.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{ID: name + "@team", Name: name, MarketplaceName: "team", Installed: true, Enabled: true, Source: plugin.PluginSource{Type: "git", URL: sourceURL}}})
		version := "1.0.0"
		if name == "release" {
			version = "1.1.0"
		}
		write(repo, name, version, "staging")
		run(repo, "add", ".")
		run(repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "publish")
	}
	router := NewRuntimeRouter(RuntimeServices{Plugins: service})
	defer closeNotificationTestRouter(t, router)
	epoch := router.testMCPEpoch()
	response := router.Handle(requestWithParams(t, IntID(1), MethodPluginList, plugin.PluginListParams{}))
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(cache, plugin.InstalledMarketplacePluginsDir, "team", "release", "marker"))
		if string(data) == "staging" && router.testMCPEpoch() > epoch {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plugin/list did not install new Git version")
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(filepath.Join(cache, plugin.InstalledMarketplacePluginsDir, "team", "docs", "marker"))
	if err != nil || string(data) != "production" {
		t.Fatalf("same version cache=%q err=%v", data, err)
	}
}
