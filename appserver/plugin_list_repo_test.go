package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/plugin"
	"codex_go/sandbox"
)

func TestPluginListHonorsPerRepositoryConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	localMarket := filepath.Join(workspace, ".gcode", "market")
	if err := os.MkdirAll(localMarket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localMarket, "plugin.json"), []byte(`{"name":"sample","description":"Sample"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(workspace, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o755); err != nil {
		t.Fatal(err)
	}
	marketSource := strings.ReplaceAll(localMarket, `\`, `\\`)
	body := "[marketplaces]\nlocal = { source_type = \"local\", source = \"" + marketSource + "\" }\n"
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	canonical := strings.ToLower(filepath.Clean(workspace))
	projectTrust := strings.ReplaceAll(canonical, `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Plugins:     plugin.NewPluginService(),
		Config:      config.NewConfigService(home),
		Models:      model.NewModelService(nil),
		Permissions: sandbox.NewPermissionProfileService(nil),
		MCP:         mcp.NewMCPService(nil),
	})
	list := router.Handle(requestWithParams(t, IntID(1), MethodPluginList, plugin.PluginListParams{CWDs: []string{workspace}}))
	if list.Error != nil {
		t.Fatalf("plugin/list error = %+v", list.Error)
	}
	response := list.Result.(*plugin.PluginListResponse)
	found := false
	for _, m := range response.Marketplaces {
		if m.Name == "local" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("per-repository marketplace not surfaced: %#v", response.Marketplaces)
	}
	// plugin/installed honors the same per-repository config.
	installed := router.Handle(requestWithParams(t, IntID(2), MethodPluginInstalled, plugin.PluginInstalledParams{CWDs: []string{workspace}}))
	if installed.Error != nil {
		t.Fatalf("plugin/installed error = %+v", installed.Error)
	}
	installedResponse := installed.Result.(*plugin.PluginInstalledResponse)
	installedFound := false
	for _, m := range installedResponse.Marketplaces {
		if m.Name == "local" {
			installedFound = true
			break
		}
	}
	if !installedFound {
		t.Fatalf("per-repository marketplace not surfaced in installed: %#v", installedResponse.Marketplaces)
	}
}
