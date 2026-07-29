package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestMarketplaceLifecycle(t *testing.T) {
	service := NewPluginService()
	service.SetMarketplaceMaterializer(fakeMarketplaceMaterializer(t))
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(source *ParsedMarketplaceSource) (string, error) {
		return "rev-lifecycle", nil
	}))
	added, err := service.AddMarketplace(&MarketplaceAddParams{URL: "https://github.com/acme/plugins.git"})
	if err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}
	if added.Marketplace.Name != "plugins" || added.AlreadyPresent {
		t.Fatalf("added = %#v", added)
	}
	addedPayload := marshalObject(t, added)
	for _, legacyKey := range []string{"marketplace", "alreadyPresent"} {
		if _, ok := addedPayload[legacyKey]; ok {
			t.Fatalf("legacy marketplace add key %q should not be emitted: %#v", legacyKey, addedPayload)
		}
	}
	refName := "main"
	addParamsPayload := marshalObject(t, &MarketplaceAddParams{
		Name:        "plugins",
		URL:         "https://github.com/acme/plugins.git",
		RefName:     &refName,
		SparsePaths: []string{"plugins/demo"},
	})
	for _, legacyKey := range []string{"name", "url"} {
		if _, ok := addParamsPayload[legacyKey]; ok {
			t.Fatalf("legacy marketplace add params key %q should not be emitted: %#v", legacyKey, addParamsPayload)
		}
	}
	if addParamsPayload["source"] != "https://github.com/acme/plugins.git" || addParamsPayload["refName"] != "main" {
		t.Fatalf("marketplace add params = %#v", addParamsPayload)
	}
	addOptionalPayload := marshalObject(t, &MarketplaceAddParams{Source: "owner/repo"})
	for _, nullableKey := range []string{"refName", "sparsePaths"} {
		if value, ok := addOptionalPayload[nullableKey]; !ok || value != nil {
			t.Fatalf("marketplace add nullable key %q = %#v in %#v", nullableKey, value, addOptionalPayload)
		}
	}
	again, err := service.AddMarketplace(&MarketplaceAddParams{Name: "plugins", URL: "https://github.com/acme/plugins.git"})
	if err != nil || !again.AlreadyPresent {
		t.Fatalf("again = %#v/%v", again, err)
	}
	upgradeParamsPayload := marshalObject(t, &MarketplaceUpgradeParams{})
	if value, ok := upgradeParamsPayload["marketplaceName"]; !ok || value != nil {
		t.Fatalf("marketplace upgrade nullable name = %#v in %#v", value, upgradeParamsPayload)
	}
	targetMarketplace := " plugins "
	upgraded, err := service.UpgradeMarketplace(&MarketplaceUpgradeParams{MarketplaceName: &targetMarketplace})
	if err != nil || len(upgraded.SelectedMarketplaces) != 1 {
		t.Fatalf("upgraded = %#v/%v", upgraded, err)
	}
	upgraded.ErrorMap = map[string]string{"plugins": "legacy"}
	upgradedPayload := marshalObject(t, upgraded)
	if _, ok := upgradedPayload["errorMap"]; ok {
		t.Fatalf("legacy marketplace upgrade errorMap should not be emitted: %#v", upgradedPayload)
	}
	removed, err := service.RemoveMarketplace(&MarketplaceRemoveParams{Name: "plugins"})
	if err != nil || !removed.Removed {
		t.Fatalf("removed = %#v/%v", removed, err)
	}
	removedPayload := marshalObject(t, removed)
	if _, ok := removedPayload["removed"]; ok {
		t.Fatalf("legacy marketplace remove key should not be emitted: %#v", removedPayload)
	}
	removeParamsPayload := marshalObject(t, &MarketplaceRemoveParams{Name: "plugins"})
	if _, ok := removeParamsPayload["name"]; ok {
		t.Fatalf("legacy marketplace remove params name should not be emitted: %#v", removeParamsPayload)
	}
	if removeParamsPayload["marketplaceName"] != "plugins" {
		t.Fatalf("marketplace remove params = %#v", removeParamsPayload)
	}
	if _, err := service.AddMarketplace(&MarketplaceAddParams{}); !errors.Is(err, ErrInvalidPluginRequest) {
		t.Fatalf("AddMarketplace(empty) error = %v, want ErrInvalidPluginRequest", err)
	}
}

func TestAddMarketplaceGitMaterializer(t *testing.T) {
	installRoot := t.TempDir()
	service := NewPluginService()
	service.SetMarketplaceInstallRoot(installRoot)
	var materializedSource *ParsedMarketplaceSource
	var materializedSparse []string
	var materializedDestination string
	service.SetMarketplaceMaterializer(MarketplaceMaterializerFunc(func(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
		materializedSource = source
		materializedSparse = append([]string(nil), sparsePaths...)
		materializedDestination = destination
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "materialized.txt"), []byte("ok"), 0o600)
	}))

	refName := "main"
	added, err := service.AddMarketplace(&MarketplaceAddParams{
		Name:        "debug",
		Source:      "owner/repo",
		RefName:     &refName,
		SparsePaths: []string{"plugins/debug"},
	})
	if err != nil {
		t.Fatalf("AddMarketplace(git) error = %v", err)
	}
	if materializedSource == nil || materializedSource.Kind != MarketplaceSourceGit || materializedSource.URL != "https://github.com/owner/repo.git" {
		t.Fatalf("materialized source = %#v", materializedSource)
	}
	if materializedSource.RefName == nil || *materializedSource.RefName != "main" {
		t.Fatalf("materialized ref = %#v", materializedSource.RefName)
	}
	if len(materializedSparse) != 1 || materializedSparse[0] != "plugins/debug" {
		t.Fatalf("materialized sparse = %#v", materializedSparse)
	}
	if materializedDestination != filepath.Join(installRoot, "debug") || added.InstalledRoot != materializedDestination {
		t.Fatalf("destination = %q added = %#v", materializedDestination, added)
	}
	if _, err := os.Stat(filepath.Join(added.InstalledRoot, "materialized.txt")); err != nil {
		t.Fatalf("materialized file stat error = %v", err)
	}
}

func TestMarketplaceConfigPersistence(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(fakeMarketplaceMaterializer(t))

	refName := "main"
	added, err := service.AddMarketplace(&MarketplaceAddParams{
		Name:        "debug",
		Source:      "owner/repo",
		RefName:     &refName,
		SparsePaths: []string{"plugins/debug"},
	})
	if err != nil {
		t.Fatalf("AddMarketplace(git) error = %v", err)
	}
	if added.InstalledRoot != filepath.Join(home, InstalledMarketplacesDir, "debug") {
		t.Fatalf("InstalledRoot = %q", added.InstalledRoot)
	}
	configValues := readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	debug := marketplaceConfigEntryForTest(t, configValues, "debug")
	if debug["source_type"] != string(MarketplaceSourceGit) || debug["source"] != "https://github.com/owner/repo.git" || debug["ref"] != "main" {
		t.Fatalf("debug config = %#v", debug)
	}
	if sparse, ok := debug["sparse_paths"].([]any); !ok || len(sparse) != 1 || sparse[0] != "plugins/debug" {
		t.Fatalf("debug sparse_paths = %#v", debug["sparse_paths"])
	}

	localRoot := t.TempDir()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "local", Source: localRoot}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}
	configValues = readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	local := marketplaceConfigEntryForTest(t, configValues, "local")
	if local["source_type"] != string(MarketplaceSourceLocal) || local["source"] != localRoot {
		t.Fatalf("local config = %#v", local)
	}

	if _, err := service.RemoveMarketplace(&MarketplaceRemoveParams{MarketplaceName: "debug"}); err != nil {
		t.Fatalf("RemoveMarketplace(debug) error = %v", err)
	}
	configValues = readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	if marketplaces := configValues["marketplaces"].(map[string]any); marketplaces["debug"] != nil {
		t.Fatalf("debug marketplace config was not removed: %#v", marketplaces)
	}
}

func TestListLoadsMarketplaceManifestPlugins(t *testing.T) {
	root := t.TempDir()
	writeTestMarketplacePlugin(t, root, "sample")
	service := NewPluginService()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	list := service.List(&PluginListParams{})
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "sample@debug" || !list.Plugins[0].HasSkills {
		t.Fatalf("List() = %#v", list)
	}
	if len(list.Marketplaces) != 1 || len(list.Marketplaces[0].Plugins) != 1 {
		t.Fatalf("marketplace entries = %#v", list.Marketplaces)
	}
	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "sample"})
	if err != nil {
		t.Fatalf("Read(sample) error = %v", err)
	}
	if read.Plugin.ManifestPath == "" || len(read.Plugin.MCPServers) != 1 || read.Plugin.MCPServers[0] != "sample-docs" {
		t.Fatalf("Read(sample) = %#v", read.Plugin)
	}
}

func TestMarketplaceManifestLoadsAppsAndAppTemplates(t *testing.T) {
	root := t.TempDir()
	marketplaceDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(marketplaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(marketplace) error = %v", err)
	}
	manifest := `{
  "name": "debug",
  "plugins": [
    {
      "name": "sample",
      "source": {
        "source": "local",
        "path": "./plugins/sample"
      },
      "apps": [
        {
          "id": "linear",
          "display_name": "Linear",
          "description": "Track work",
          "install_url": "https://chatgpt.com/connectors/linear",
          "category": "productivity"
        }
      ],
      "app_templates": [
        {
          "template_id": "linear-triage",
          "display_name": "Linear triage",
          "description": "Triage issues",
          "canonical_connector_id": "linear",
          "logo_url": "https://example.com/logo.png",
          "logo_url_dark": "https://example.com/logo-dark.png",
          "materialized_app_ids": ["linear", "slack"],
          "reason": "recommended"
        }
      ]
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, "marketplace.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(marketplace) error = %v", err)
	}
	pluginRoot := filepath.Join(root, "plugins", "sample", ".codex-plugin")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin manifest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(`{"name":"sample","description":"Sample"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin manifest) error = %v", err)
	}
	service := NewPluginService()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "sample"})
	if err != nil {
		t.Fatalf("Read(sample) error = %v", err)
	}
	if strings.Join(read.Plugin.Summary.AppConnectors, ",") != "linear,slack" {
		t.Fatalf("AppConnectors = %#v", read.Plugin.Summary.AppConnectors)
	}
	if len(read.Plugin.Apps) != 1 || read.Plugin.Apps[0].ID != "linear" || read.Plugin.Apps[0].DisplayName != "Linear" {
		t.Fatalf("Apps = %#v", read.Plugin.Apps)
	}
	if read.Plugin.Apps[0].InstallURL == nil || *read.Plugin.Apps[0].InstallURL != "https://chatgpt.com/connectors/linear" {
		t.Fatalf("App install URL = %#v", read.Plugin.Apps[0].InstallURL)
	}
	if len(read.Plugin.AppTemplates) != 1 || read.Plugin.AppTemplates[0].TemplateID != "linear-triage" {
		t.Fatalf("AppTemplates = %#v", read.Plugin.AppTemplates)
	}
	template := read.Plugin.AppTemplates[0]
	if template.CanonicalConnectorID == nil || *template.CanonicalConnectorID != "linear" || strings.Join(template.MaterializedAppIDs, ",") != "linear,slack" {
		t.Fatalf("template connector fields = %#v", template)
	}
	payload := marshalObject(t, &read.Plugin)
	templatePayload := payload["appTemplates"].([]any)[0].(map[string]any)
	if templatePayload["templateId"] != "linear-triage" || templatePayload["canonicalConnectorId"] != "linear" {
		t.Fatalf("template payload = %#v", templatePayload)
	}
}

func TestPluginInterfaceUnmarshalAcceptsSnakeCaseMarketplaceFields(t *testing.T) {
	var iface PluginInterface
	if err := json.Unmarshal([]byte(`{
		"display_name": "Linear",
		"short_description": "Plan and track work",
		"developer_name": "Acme",
		"capabilities": ["Read", " Write "],
		"default_prompt": "Use the legacy prompt",
		"default_prompts": ["Create an issue", "Review projects"],
		"logo_url": "https://example.com/logo.png",
		"logo_url_dark": "https://example.com/logo-dark.png",
		"screenshot_urls": ["https://example.com/shot.png"]
	}`), &iface); err != nil {
		t.Fatalf("Unmarshal PluginInterface error = %v", err)
	}
	if iface.DisplayName == nil || *iface.DisplayName != "Linear" || iface.ShortDescription == nil || *iface.ShortDescription != "Plan and track work" {
		t.Fatalf("interface labels = %#v", iface)
	}
	if iface.DeveloperName == nil || *iface.DeveloperName != "Acme" {
		t.Fatalf("DeveloperName = %#v", iface.DeveloperName)
	}
	if strings.Join(iface.Capabilities, ",") != "Read,Write" {
		t.Fatalf("Capabilities = %#v", iface.Capabilities)
	}
	if strings.Join(iface.DefaultPrompt, "|") != "Create an issue|Review projects" {
		t.Fatalf("DefaultPrompt = %#v", iface.DefaultPrompt)
	}
	if iface.LogoURL == nil || *iface.LogoURL != "https://example.com/logo.png" || iface.LogoURLDark == nil || *iface.LogoURLDark != "https://example.com/logo-dark.png" {
		t.Fatalf("Logo URLs = %#v %#v", iface.LogoURL, iface.LogoURLDark)
	}
	if len(iface.ScreenshotURLs) != 1 || iface.ScreenshotURLs[0] != "https://example.com/shot.png" {
		t.Fatalf("ScreenshotURLs = %#v", iface.ScreenshotURLs)
	}
	payload := marshalObject(t, &iface)
	if _, ok := payload["default_prompts"]; ok {
		t.Fatalf("snake_case default_prompts should not be emitted: %#v", payload)
	}
	prompts, ok := payload["defaultPrompt"].([]any)
	if !ok || len(prompts) != 2 {
		t.Fatalf("defaultPrompt payload = %#v", payload["defaultPrompt"])
	}
	if payload["logoUrl"] != "https://example.com/logo.png" || payload["logoUrlDark"] != "https://example.com/logo-dark.png" {
		t.Fatalf("logo payload = %#v", payload)
	}
}

func TestPluginPayloadMarshalRustShape(t *testing.T) {
	sourceCases := []struct {
		name string
		in   PluginSource
		want string
	}{
		{name: "local", in: PluginSource{Type: "local", Path: "/plugins/docs"}, want: `{"type":"local","path":"/plugins/docs"}`},
		{name: "git nullable fields", in: PluginSource{Type: "git", URL: "https://example.com/plugins.git"}, want: `{"type":"git","url":"https://example.com/plugins.git","path":null,"refName":null,"sha":null}`},
		{name: "npm nullable fields", in: PluginSource{Type: "npm", Package: "@acme/plugin"}, want: `{"type":"npm","package":"@acme/plugin","version":null,"registry":null}`},
		{name: "remote", in: PluginSource{Type: "remote"}, want: `{"type":"remote"}`},
		{name: "internal suggestion folds to remote", in: PluginSource{Type: "suggestion"}, want: `{"type":"remote"}`},
	}
	for _, tc := range sourceCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(&tc.in)
			if err != nil {
				t.Fatalf("Marshal PluginSource error = %v", err)
			}
			if string(data) != tc.want {
				t.Fatalf("PluginSource JSON = %s, want %s", data, tc.want)
			}
		})
	}

	ifacePayload := marshalObject(t, &PluginInterface{Icon: "legacy"})
	if value, ok := ifacePayload["defaultPrompt"]; !ok || value != nil {
		t.Fatalf("defaultPrompt = %#v, want required null in %#v", value, ifacePayload)
	}
	if _, ok := ifacePayload["icon"]; ok {
		t.Fatalf("legacy icon should not be emitted: %#v", ifacePayload)
	}

	skillPayload := marshalObject(t, &PluginSkill{Name: "deploy", Description: "Deploy", Enabled: true})
	if value, ok := skillPayload["interface"]; !ok || value != nil {
		t.Fatalf("skill interface = %#v, want required null in %#v", value, skillPayload)
	}

	hookPayload := marshalObject(t, &PluginHookSummary{Key: "pre", EventName: "preToolUse", Enabled: true})
	if _, ok := hookPayload["enabled"]; ok {
		t.Fatalf("legacy hook enabled should not be emitted: %#v", hookPayload)
	}
	if hookPayload["eventName"] != "preToolUse" {
		t.Fatalf("hook eventName = %#v", hookPayload)
	}
}

func TestMarketplacePluginSkillsListsNestedSkillFiles(t *testing.T) {
	root := t.TempDir()
	writeTestMarketplacePlugin(t, root, "sample")
	pluginRoot := filepath.Join(root, "plugins", "sample")
	nested := filepath.Join(pluginRoot, "skills", "deploy")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(nested skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("---\nname: deploy-app\ndescription: deploy\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(nested skill) error = %v", err)
	}
	service := NewPluginService()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "sample"})
	if err != nil {
		t.Fatalf("Read(sample) error = %v", err)
	}
	if len(read.Plugin.Skills) != 2 || read.Plugin.Skills[0].Name != "deploy-app" || read.Plugin.Skills[1].Name != "sample" {
		t.Fatalf("plugin skills = %#v", read.Plugin.Skills)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "sample", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(sample) error = %v", err)
	}
	skill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "sample@debug", SkillName: "deploy-app"})
	if err != nil {
		t.Fatalf("ReadSkill(deploy-app) error = %v", err)
	}
	if skill.Contents == nil || !strings.Contains(*skill.Contents, "name: deploy-app") {
		t.Fatalf("ReadSkill(deploy-app) = %#v", skill)
	}
}

func TestInstallMarketplaceManifestPluginByPath(t *testing.T) {
	root := t.TempDir()
	writeTestMarketplacePlugin(t, root, "sample")
	marketplacePath := filepath.Join(root, ".agents", "plugins", "marketplace.json")
	service := NewPluginService()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	read, err := service.Read(&PluginReadParams{MarketplacePath: " " + marketplacePath + " ", PluginName: " sample "})
	if err != nil {
		t.Fatalf("Read(sample by marketplacePath) error = %v", err)
	}
	if read.Plugin.MarketplacePath == nil || *read.Plugin.MarketplacePath != marketplacePath {
		t.Fatalf("read marketplace path = %#v", read.Plugin.MarketplacePath)
	}

	installed, err := service.Install(&PluginInstallParams{MarketplacePath: marketplacePath, PluginName: "sample"})
	if err != nil {
		t.Fatalf("Install(sample by marketplacePath) error = %v", err)
	}
	if installed.PluginID != "sample@debug" {
		t.Fatalf("installed = %#v", installed)
	}
	got := service.Installed(&PluginInstalledParams{})
	if len(got.Plugins) != 1 || got.Plugins[0].ID != "sample@debug" || !got.Plugins[0].Enabled {
		t.Fatalf("installed list = %#v", got)
	}
	skill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "sample@debug", SkillName: "sample"})
	if err != nil {
		t.Fatalf("ReadSkill(sample) error = %v", err)
	}
	if skill.Contents == nil || !strings.Contains(*skill.Contents, "description: sample") {
		t.Fatalf("ReadSkill(sample) = %#v", skill)
	}
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 || capabilities[0].ConfigName != "sample@debug" || capabilities[0].Name != "sample" || len(capabilities[0].MCPServers) != 1 {
		t.Fatalf("enabled capabilities = %#v", capabilities)
	}
}

func TestReadPluginTrimsRemoteIDAndMarketplaceName(t *testing.T) {
	service := NewPluginService()
	service.AddPlugin(PluginDetail{Summary: PluginSummary{
		ID:              "sample@debug",
		Name:            "sample",
		MarketplaceName: "debug",
		RemotePluginID:  "remote-1",
	}})

	read, err := service.Read(&PluginReadParams{RemoteMarketplaceName: " debug ", RemotePluginID: " remote-1 "})
	if err != nil {
		t.Fatalf("Read(remote plugin) error = %v", err)
	}
	if read.Plugin.Summary.ID != "sample@debug" {
		t.Fatalf("read plugin = %#v", read.Plugin.Summary)
	}
	read, err = service.Read(&PluginReadParams{RemoteMarketplaceName: " debug ", RemotePluginID: " sample@debug "})
	if err != nil {
		t.Fatalf("Read(canonical plugin id) error = %v", err)
	}
	if read.Plugin.Summary.ID != "sample@debug" {
		t.Fatalf("read canonical plugin = %#v", read.Plugin.Summary)
	}
}

func TestReadSkillMatchesPluginNameWithRemoteMarketplaceName(t *testing.T) {
	root := t.TempDir()
	writeTestMarketplacePlugin(t, root, "sample")
	service := NewPluginService()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	skill, err := service.ReadSkill(&PluginSkillReadParams{RemoteMarketplaceName: " debug ", RemotePluginID: " sample ", SkillName: " sample "})
	if err != nil {
		t.Fatalf("ReadSkill(sample) error = %v", err)
	}
	if skill.Contents == nil || !strings.Contains(*skill.Contents, "description: sample") {
		t.Fatalf("ReadSkill(sample) = %#v", skill)
	}
}

func TestReadRemoteMarketplaceManifestPluginMaterializesSource(t *testing.T) {
	root := t.TempDir()
	writeTestRemoteMarketplacePlugin(t, root, "remote-sample", "plugin")
	installRoot := t.TempDir()
	service := NewPluginService()
	service.SetMarketplaceInstallRoot(installRoot)
	var materializedSource *ParsedMarketplaceSource
	var materializedDestination string
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializedSource = source
		materializedDestination = destination
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	list := service.List(&PluginListParams{})
	if len(list.Plugins) != 1 || list.Plugins[0].Source.Type != "git" || list.Plugins[0].Source.Path != "plugin" {
		t.Fatalf("List(remote placeholder) = %#v", list.Plugins)
	}
	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "remote-sample"})
	if err != nil {
		t.Fatalf("Read(remote-sample) error = %v", err)
	}
	if materializedSource == nil || materializedSource.URL != "https://github.com/acme/remote-sample.git" || materializedSource.RefName == nil || *materializedSource.RefName != "main" {
		t.Fatalf("materialized source = %#v", materializedSource)
	}
	if materializedDestination != filepath.Join(installRoot, InstalledMarketplacePluginsDir, "debug", "remote-sample") {
		t.Fatalf("materialized destination = %q", materializedDestination)
	}
	if read.Plugin.ManifestPath != filepath.Join(materializedDestination, "plugin", ".codex-plugin", "plugin.json") || !read.Plugin.Summary.HasSkills {
		t.Fatalf("Read(remote-sample) = %#v", read.Plugin)
	}
	if len(read.Plugin.MCPServers) != 1 || read.Plugin.MCPServers[0] != "remote-sample-docs" {
		t.Fatalf("read MCP servers = %#v", read.Plugin.MCPServers)
	}
}

func TestReadSkillRemoteMarketplaceManifestPluginMaterializesSource(t *testing.T) {
	root := t.TempDir()
	writeTestRemoteMarketplacePlugin(t, root, "remote-sample", "plugin")
	installRoot := t.TempDir()
	service := NewPluginService()
	service.SetMarketplaceInstallRoot(installRoot)
	materializeCalls := 0
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializeCalls++
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	skill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "remote-sample@debug", SkillName: "remote"})
	if err != nil {
		t.Fatalf("ReadSkill(remote) error = %v", err)
	}
	if materializeCalls != 1 {
		t.Fatalf("materialize calls = %d, want 1", materializeCalls)
	}
	if skill.Contents == nil || !strings.Contains(*skill.Contents, "name: remote") {
		t.Fatalf("ReadSkill(remote) = %#v", skill)
	}
	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "remote-sample"})
	if err != nil {
		t.Fatalf("Read(remote-sample) error = %v", err)
	}
	if materializeCalls != 1 {
		t.Fatalf("cached materialize calls = %d, want 1", materializeCalls)
	}
	if read.Plugin.ManifestPath != filepath.Join(installRoot, InstalledMarketplacePluginsDir, "debug", "remote-sample", "plugin", ".codex-plugin", "plugin.json") {
		t.Fatalf("cached read plugin = %#v", read.Plugin)
	}
}

func TestInstallRemoteMarketplaceManifestPluginMaterializesSource(t *testing.T) {
	root := t.TempDir()
	writeTestRemoteMarketplacePlugin(t, root, "remote-sample", "plugin")
	installRoot := t.TempDir()
	service := NewPluginService()
	service.SetMarketplaceInstallRoot(installRoot)
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}

	installed, err := service.Install(&PluginInstallParams{PluginName: "remote-sample", MarketplaceName: "debug"})
	if err != nil {
		t.Fatalf("Install(remote-sample) error = %v", err)
	}
	if installed.PluginID != "remote-sample@debug" {
		t.Fatalf("installed = %#v", installed)
	}
	got := service.Installed(&PluginInstalledParams{})
	if len(got.Plugins) != 1 || got.Plugins[0].ID != "remote-sample@debug" || !got.Plugins[0].HasSkills {
		t.Fatalf("installed list = %#v", got)
	}
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 || len(capabilities[0].MCPServers) != 1 || capabilities[0].MCPServers[0] != "remote-sample-docs" {
		t.Fatalf("enabled capabilities = %#v", capabilities)
	}
	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "remote-sample"})
	if err != nil {
		t.Fatalf("Read(installed remote-sample) error = %v", err)
	}
	if read.Plugin.Summary.Source.Path != filepath.Join(installRoot, InstalledMarketplacePluginsDir, "debug", "remote-sample", "plugin") {
		t.Fatalf("read source = %#v", read.Plugin.Summary.Source)
	}
	materializedRoot := filepath.Join(installRoot, InstalledMarketplacePluginsDir, "debug", "remote-sample")
	if _, err := os.Stat(materializedRoot); err != nil {
		t.Fatalf("materialized root stat error = %v", err)
	}
	if _, err := service.Uninstall(&PluginUninstallParams{PluginID: "remote-sample@debug"}); err != nil {
		t.Fatalf("Uninstall(remote-sample) error = %v", err)
	}
	if _, err := os.Stat(materializedRoot); !os.IsNotExist(err) {
		t.Fatalf("materialized root should be removed after uninstall, stat error = %v", err)
	}
}

func TestInstallStoredRemotePlaceholderMaterializesSource(t *testing.T) {
	installRoot := t.TempDir()
	service := NewPluginService()
	service.SetMarketplaceInstallRoot(installRoot)
	refName := "main"
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:              "remote-sample@debug",
			Name:            "remote-sample",
			MarketplaceName: "debug",
			Source: PluginSource{
				Type:    "git",
				URL:     "https://github.com/acme/remote-sample.git",
				Path:    "plugin",
				RefName: &refName,
			},
		},
	})
	materializeCalls := 0
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializeCalls++
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))

	installed, err := service.Install(&PluginInstallParams{PluginID: "remote-sample@debug"})
	if err != nil {
		t.Fatalf("Install(remote placeholder) error = %v", err)
	}
	if installed.PluginID != "remote-sample@debug" || materializeCalls != 1 {
		t.Fatalf("installed=%#v materializeCalls=%d", installed, materializeCalls)
	}
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 || len(capabilities[0].MCPServers) != 1 || capabilities[0].MCPServers[0] != "remote-sample-docs" {
		t.Fatalf("enabled capabilities = %#v", capabilities)
	}
}

func TestReadInstalledRemotePluginRematerializesMissingRoot(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeTestRemoteMarketplacePlugin(t, root, "remote-sample", "plugin")
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "remote-sample", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(remote-sample) error = %v", err)
	}

	materializedRoot := filepath.Join(home, InstalledMarketplacesDir, InstalledMarketplacePluginsDir, "debug", "remote-sample")
	if err := os.RemoveAll(materializedRoot); err != nil {
		t.Fatalf("RemoveAll(materialized root) error = %v", err)
	}
	reloaded := NewPluginService()
	reloaded.SetCodexHome(home)
	materializeCalls := 0
	reloaded.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializeCalls++
		if destination != materializedRoot {
			t.Fatalf("materialized destination = %q, want %q", destination, materializedRoot)
		}
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))

	capabilities := reloaded.EnabledCapabilities()
	if materializeCalls != 1 {
		t.Fatalf("capabilities materialize calls = %d, want 1", materializeCalls)
	}
	if len(capabilities) != 1 || len(capabilities[0].MCPServers) != 1 || capabilities[0].MCPServers[0] != "remote-sample-docs" {
		t.Fatalf("rematerialized capabilities = %#v", capabilities)
	}
	roots := reloaded.EnabledSkillRoots()
	if len(roots) != 1 || roots[0].Root != filepath.Join(materializedRoot, "plugin", "skills") {
		t.Fatalf("rematerialized skill roots = %#v", roots)
	}
	read, err := reloaded.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "remote-sample"})
	if err != nil {
		t.Fatalf("Read(remote-sample) error = %v", err)
	}
	if materializeCalls != 1 {
		t.Fatalf("materialize calls = %d, want 1", materializeCalls)
	}
	if read.Plugin.ManifestPath != filepath.Join(materializedRoot, "plugin", ".codex-plugin", "plugin.json") || !read.Plugin.Summary.HasSkills {
		t.Fatalf("rematerialized plugin = %#v", read.Plugin)
	}
	if len(read.Plugin.MCPServers) != 1 || read.Plugin.MCPServers[0] != "remote-sample-docs" {
		t.Fatalf("rematerialized MCP servers = %#v", read.Plugin.MCPServers)
	}
}

func TestPluginInstallConfigPersistence(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeTestMarketplacePlugin(t, root, "sample")
	service := NewPluginService()
	service.SetCodexHome(home)
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "sample", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(sample) error = %v", err)
	}
	configValues := readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	plugins, ok := configValues["plugins"].(map[string]any)
	if !ok {
		t.Fatalf("plugins config missing: %#v", configValues)
	}
	sample, ok := plugins["sample@debug"].(map[string]any)
	if !ok || sample["installed"] != true || sample["enabled"] != true || sample["path"] == "" {
		t.Fatalf("sample plugin config = %#v", plugins["sample@debug"])
	}

	reloaded := NewPluginService()
	reloaded.SetCodexHome(home)
	installed := reloaded.Installed(&PluginInstalledParams{})
	if len(installed.Plugins) != 1 || installed.Plugins[0].ID != "sample@debug" || !installed.Plugins[0].HasSkills {
		t.Fatalf("reloaded installed = %#v", installed)
	}
	capabilities := reloaded.EnabledCapabilities()
	if len(capabilities) != 1 || len(capabilities[0].MCPServers) != 1 || capabilities[0].MCPServers[0] != "sample-docs" {
		t.Fatalf("reloaded capabilities = %#v", capabilities)
	}
	if _, err := reloaded.Uninstall(&PluginUninstallParams{PluginID: " sample@debug "}); err != nil {
		t.Fatalf("Uninstall(sample) error = %v", err)
	}
	if got := reloaded.Installed(&PluginInstalledParams{}); len(got.Plugins) != 0 {
		t.Fatalf("installed after trimmed uninstall = %#v", got.Plugins)
	}
	configValues = readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	if plugins, ok := configValues["plugins"].(map[string]any); ok && plugins["sample@debug"] != nil {
		t.Fatalf("sample plugin config should be removed: %#v", plugins)
	}
}

func TestRemoveMarketplaceCleansInstalledPluginConfigAndMaterializedPlugins(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeTestRemoteMarketplacePlugin(t, root, "remote-sample", "plugin")
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		writeMaterializedTestPlugin(t, filepath.Join(destination, "plugin"), "remote-sample")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "remote-sample", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(remote-sample) error = %v", err)
	}
	materializedRoot := filepath.Join(home, InstalledMarketplacesDir, InstalledMarketplacePluginsDir, "debug", "remote-sample")
	if _, err := os.Stat(materializedRoot); err != nil {
		t.Fatalf("materialized root stat error = %v", err)
	}

	if _, err := service.RemoveMarketplace(&MarketplaceRemoveParams{MarketplaceName: "debug"}); err != nil {
		t.Fatalf("RemoveMarketplace(debug) error = %v", err)
	}
	if got := service.Installed(&PluginInstalledParams{}); len(got.Plugins) != 0 {
		t.Fatalf("installed after marketplace removal = %#v", got)
	}
	if _, err := os.Stat(materializedRoot); !os.IsNotExist(err) {
		t.Fatalf("materialized root should be removed, stat error = %v", err)
	}
	configValues := readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	if marketplaces, ok := configValues["marketplaces"].(map[string]any); ok && marketplaces["debug"] != nil {
		t.Fatalf("debug marketplace config should be removed: %#v", marketplaces)
	}
	if plugins, ok := configValues["plugins"].(map[string]any); ok && plugins["remote-sample@debug"] != nil {
		t.Fatalf("remote plugin config should be removed: %#v", plugins)
	}
}

func TestUpgradeMarketplaceRevisionAndConfig(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	upgrader := &recordingMarketplaceUpgrader{}
	service.SetMarketplaceMaterializer(upgrader)
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(source *ParsedMarketplaceSource) (string, error) {
		return "rev-1", nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: "owner/repo"}); err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}

	upgraded, err := service.UpgradeMarketplace(&MarketplaceUpgradeParams{})
	if err != nil {
		t.Fatalf("UpgradeMarketplace() error = %v", err)
	}
	if len(upgraded.SelectedMarketplaces) != 1 || upgraded.SelectedMarketplaces[0] != "debug" || len(upgraded.UpgradedRoots) != 1 || len(upgraded.Errors) != 0 {
		t.Fatalf("upgraded = %#v", upgraded)
	}
	if len(upgrader.upgraded) != 1 || upgrader.upgraded[0] != filepath.Join(home, InstalledMarketplacesDir, "debug") {
		t.Fatalf("upgrader = %#v", upgrader.upgraded)
	}
	configValues := readMarketplaceConfigForTest(t, filepath.Join(home, ConfigTOMLFilename))
	debug := marketplaceConfigEntryForTest(t, configValues, "debug")
	if debug["last_revision"] != "rev-1" {
		t.Fatalf("debug config = %#v", debug)
	}

	upgraded, err = service.UpgradeMarketplace(&MarketplaceUpgradeParams{})
	if err != nil {
		t.Fatalf("UpgradeMarketplace(second) error = %v", err)
	}
	if len(upgraded.UpgradedRoots) != 0 || len(upgrader.upgraded) != 1 {
		t.Fatalf("second upgrade should skip unchanged revision: response=%#v upgrader=%#v", upgraded, upgrader.upgraded)
	}
}

func TestUpgradeMarketplaceRefreshesInstalledLocalPlugin(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(&versionedMarketplaceUpgrader{
		materialize: func(destination string) error {
			writeVersionedMarketplacePlugin(t, destination, "sample", "sample-v1", "sample-docs-v1", "Version one")
			return nil
		},
		upgrade: func(destination string) error {
			if err := os.RemoveAll(destination); err != nil {
				return err
			}
			writeVersionedMarketplacePlugin(t, destination, "sample", "sample-v2", "sample-docs-v2", "Version two")
			return nil
		},
	})
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(source *ParsedMarketplaceSource) (string, error) {
		return "rev-2", nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: "owner/repo"}); err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "sample", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(sample) error = %v", err)
	}

	upgraded, err := service.UpgradeMarketplace(&MarketplaceUpgradeParams{})
	if err != nil {
		t.Fatalf("UpgradeMarketplace() error = %v", err)
	}
	if len(upgraded.Errors) != 0 {
		t.Fatalf("UpgradeMarketplace() errors = %#v", upgraded.Errors)
	}
	read, err := service.Read(&PluginReadParams{PluginName: "sample", MarketplaceName: "debug"})
	if err != nil {
		t.Fatalf("Read(sample) error = %v", err)
	}
	if read.Plugin.Description == nil || *read.Plugin.Description != "Version two" {
		t.Fatalf("refreshed description = %#v", read.Plugin.Description)
	}
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 || len(capabilities[0].MCPServers) != 1 || capabilities[0].MCPServers[0] != "sample-docs-v2" {
		t.Fatalf("enabled capabilities = %#v", capabilities)
	}
	skill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "sample@debug", SkillName: "sample-v2"})
	if err != nil {
		t.Fatalf("ReadSkill(sample-v2) error = %v", err)
	}
	if skill.Contents == nil || !strings.Contains(*skill.Contents, "name: sample-v2") {
		t.Fatalf("skill contents = %#v", skill)
	}
	roots := service.EnabledSkillRoots()
	wantRoot := filepath.Join(home, InstalledMarketplacesDir, "debug", "plugins", "sample", "skills")
	if len(roots) != 1 || roots[0].Root != wantRoot || roots[0].PluginNamespace != "sample" {
		t.Fatalf("enabled skill roots = %#v, want root %q", roots, wantRoot)
	}
}

func TestUpgradeMarketplaceRematerializesInstalledRemotePlugin(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(&versionedMarketplaceUpgrader{
		materialize: func(destination string) error {
			writeTestRemoteMarketplacePlugin(t, destination, "remote-sample", "plugin")
			return nil
		},
		upgrade: func(destination string) error {
			if err := os.RemoveAll(destination); err != nil {
				return err
			}
			writeTestRemoteMarketplacePlugin(t, destination, "remote-sample", "plugin")
			return nil
		},
	})
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(source *ParsedMarketplaceSource) (string, error) {
		return "rev-remote-2", nil
	}))
	materializeCalls := 0
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializeCalls++
		if materializeCalls == 2 {
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("upgrade should remove old materialized destination before refresh, stat error = %v", err)
			}
		}
		version := strconv.Itoa(materializeCalls)
		writeVersionedPluginRoot(t, filepath.Join(destination, "plugin"), "remote-sample", "remote-v"+version, "remote-docs-v"+version, "Remote version")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: "owner/repo"}); err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "remote-sample", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(remote-sample) error = %v", err)
	}

	upgraded, err := service.UpgradeMarketplace(&MarketplaceUpgradeParams{})
	if err != nil {
		t.Fatalf("UpgradeMarketplace() error = %v", err)
	}
	if len(upgraded.Errors) != 0 || materializeCalls != 2 {
		t.Fatalf("upgrade errors=%#v materializeCalls=%d", upgraded.Errors, materializeCalls)
	}
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 || len(capabilities[0].MCPServers) != 1 || capabilities[0].MCPServers[0] != "remote-docs-v2" {
		t.Fatalf("enabled capabilities = %#v", capabilities)
	}
	skill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "remote-sample@debug", SkillName: "remote-v2"})
	if err != nil {
		t.Fatalf("ReadSkill(remote-v2) error = %v", err)
	}
	if skill.Contents == nil || !strings.Contains(*skill.Contents, "name: remote-v2") {
		t.Fatalf("skill contents = %#v", skill)
	}
}

func TestEnabledSkillRootsPreserveRemotePluginIdentityLikeRust(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "review")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("---\nname: review\ndescription: review\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewPluginService()
	service.SetRuntimeRoute("chatgpt", "openai")
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:             "sample@openai-curated-remote",
			Name:           "sample",
			RemotePluginID: "plugins~Plugin_sample",
			HasSkills:      true,
			Installed:      true,
			Enabled:        true,
			Source:         PluginSource{Type: "local", Path: root},
		},
		ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json"),
	})
	roots := service.EnabledSkillRoots()
	if len(roots) != 1 || roots[0].PluginID != "sample@openai-curated-remote" || roots[0].RemotePluginID != "plugins~Plugin_sample" {
		t.Fatalf("enabled roots = %#v", roots)
	}
}

func TestUpgradeMarketplaceClearsUninstalledMaterializedRemotePlugin(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(&versionedMarketplaceUpgrader{
		materialize: func(destination string) error {
			writeTestRemoteMarketplacePlugin(t, destination, "remote-sample", "plugin")
			return nil
		},
		upgrade: func(destination string) error {
			if err := os.RemoveAll(destination); err != nil {
				return err
			}
			writeTestRemoteMarketplacePlugin(t, destination, "remote-sample", "plugin")
			return nil
		},
	})
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(source *ParsedMarketplaceSource) (string, error) {
		return "rev-remote-2", nil
	}))
	materializeCalls := 0
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializeCalls++
		if materializeCalls == 2 {
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("upgrade should clear uninstalled materialized destination before next read, stat error = %v", err)
			}
		}
		version := strconv.Itoa(materializeCalls)
		writeVersionedPluginRoot(t, filepath.Join(destination, "plugin"), "remote-sample", "remote-v"+version, "remote-docs-v"+version, "Remote version")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: "owner/repo"}); err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}
	firstSkill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "remote-sample@debug", SkillName: "remote-v1"})
	if err != nil {
		t.Fatalf("ReadSkill(remote-v1) error = %v", err)
	}
	if firstSkill.Contents == nil || !strings.Contains(*firstSkill.Contents, "name: remote-v1") {
		t.Fatalf("first skill contents = %#v", firstSkill)
	}
	materializedRoot := filepath.Join(home, InstalledMarketplacesDir, InstalledMarketplacePluginsDir, "debug", "remote-sample")
	if _, err := os.Stat(materializedRoot); err != nil {
		t.Fatalf("materialized root stat error = %v", err)
	}

	upgraded, err := service.UpgradeMarketplace(&MarketplaceUpgradeParams{})
	if err != nil {
		t.Fatalf("UpgradeMarketplace() error = %v", err)
	}
	if len(upgraded.Errors) != 0 {
		t.Fatalf("UpgradeMarketplace() errors = %#v", upgraded.Errors)
	}
	if _, err := os.Stat(materializedRoot); !os.IsNotExist(err) {
		t.Fatalf("uninstalled materialized root should be removed, stat error = %v", err)
	}
	secondSkill, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "remote-sample@debug", SkillName: "remote-v2"})
	if err != nil {
		t.Fatalf("ReadSkill(remote-v2) error = %v", err)
	}
	if materializeCalls != 2 {
		t.Fatalf("materialize calls = %d, want 2", materializeCalls)
	}
	if secondSkill.Contents == nil || !strings.Contains(*secondSkill.Contents, "name: remote-v2") {
		t.Fatalf("second skill contents = %#v", secondSkill)
	}
}

func TestMarketplaceSourceParsing(t *testing.T) {
	parsed, err := ParseMarketplaceSource("owner/repo@main", nil)
	if err != nil {
		t.Fatalf("ParseMarketplaceSource(shorthand) error = %v", err)
	}
	if parsed.Kind != MarketplaceSourceGit || parsed.URL != "https://github.com/owner/repo.git" || parsed.RefName == nil || *parsed.RefName != "main" {
		t.Fatalf("parsed shorthand = %#v", parsed)
	}

	override := "release"
	parsed, err = ParseMarketplaceSource("https://example.com/team/repo.git#v1", &override)
	if err != nil {
		t.Fatalf("ParseMarketplaceSource(url) error = %v", err)
	}
	if parsed.URL != "https://example.com/team/repo.git" || parsed.RefName == nil || *parsed.RefName != "release" || parsed.Display() != "https://example.com/team/repo.git#release" {
		t.Fatalf("parsed url = %#v display=%q", parsed, parsed.Display())
	}

	if _, err := ParseMarketplaceSource("file:///tmp/marketplace.git", nil); !errors.Is(err, ErrInvalidPluginRequest) || !strings.Contains(err.Error(), "invalid marketplace source format") {
		t.Fatalf("file url error = %v, want invalid format", err)
	}
	if !looksLikeLocalMarketplacePath(`C:\Users\alice\marketplace`) || !looksLikeLocalMarketplacePath(`\\server\share\marketplace`) || looksLikeLocalMarketplacePath(`C:relative\path`) {
		t.Fatalf("windows path detection mismatch")
	}
}

func TestAddMarketplaceLocalSourceAndSparseValidation(t *testing.T) {
	root := t.TempDir()
	service := NewPluginService()
	added, err := service.AddMarketplace(&MarketplaceAddParams{Source: root})
	if err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}
	if added.Marketplace.SourceType != string(MarketplaceSourceLocal) || added.Marketplace.RootPath != root || added.InstalledRoot != root {
		t.Fatalf("local marketplace = %#v", added)
	}

	file := filepath.Join(t.TempDir(), "marketplace.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Source: file}); !errors.Is(err, ErrInvalidPluginRequest) || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("AddMarketplace(file) error = %v", err)
	}
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Source: root, Name: "local-sparse", SparsePaths: []string{"plugins/demo"}}); !errors.Is(err, ErrInvalidPluginRequest) || !strings.Contains(err.Error(), "--sparse is only supported") {
		t.Fatalf("AddMarketplace(local sparse) error = %v", err)
	}
}

func TestAddPluginNormalizesSummaryIdentity(t *testing.T) {
	service := NewPluginService()
	service.AddPlugin(PluginDetail{Summary: PluginSummary{
		Name:            " sample ",
		MarketplaceName: " local ",
		DisplayName:     " Sample ",
		RemotePluginID:  " remote-1 ",
	}})

	list := service.List(&PluginListParams{})
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "sample@local" || list.Plugins[0].Name != "sample" || list.Plugins[0].RemotePluginID != "remote-1" {
		t.Fatalf("list = %#v", list.Plugins)
	}
	if _, err := service.Read(&PluginReadParams{PluginName: "sample", MarketplaceName: "local"}); err != nil {
		t.Fatalf("Read(normalized plugin) error = %v", err)
	}
	installed, err := service.Install(&PluginInstallParams{PluginID: "sample@local"})
	if err != nil {
		t.Fatalf("Install(normalized plugin) error = %v", err)
	}
	if installed.PluginID != "sample@local" {
		t.Fatalf("installed = %#v", installed)
	}
}

func TestPluginLifecycleAndShares(t *testing.T) {
	service := NewPluginService()
	service.AddPlugin(PluginDetail{Summary: PluginSummary{Name: "sample", MarketplaceName: "local", DisplayName: "Sample"}, Skills: []PluginSkill{{Name: "skill-a", Enabled: true}}})
	list := service.List(&PluginListParams{})
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "sample@local" {
		t.Fatalf("list = %#v", list)
	}
	encodedList, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("Marshal list returned error: %v", err)
	}
	var listPayload map[string]any
	if err := json.Unmarshal(encodedList, &listPayload); err != nil {
		t.Fatalf("Unmarshal list returned error: %v", err)
	}
	if _, ok := listPayload["plugins"]; ok {
		t.Fatalf("legacy top-level plugins key should not be emitted: %#v", listPayload)
	}
	if marketplaces, ok := listPayload["marketplaces"].([]any); !ok || len(marketplaces) != 1 {
		t.Fatalf("marketplaces missing plugin entries: %#v", listPayload)
	}
	listParamsPayload := marshalObject(t, &PluginListParams{
		CWDs:             []string{"/repo"},
		MarketplaceKinds: []string{"local"},
		IncludeInstalled: true,
	})
	if includeInstalled, ok := listParamsPayload["includeInstalled"].(bool); !ok || !includeInstalled {
		t.Fatalf("plugin list includeInstalled = %#v", listParamsPayload["includeInstalled"])
	}
	if cwds, ok := listParamsPayload["cwds"].([]any); !ok || len(cwds) != 1 || cwds[0] != "/repo" {
		t.Fatalf("plugin list cwds = %#v", listParamsPayload["cwds"])
	}
	marketplacePayload := marshalObject(t, &PluginMarketplaceEntry{
		Name: "local",
		Interface: map[string]any{
			"displayName": "Local Marketplace",
			"legacy":      "drop-me",
		},
	})
	marketplaceInterface := marketplacePayload["interface"].(map[string]any)
	if marketplaceInterface["displayName"] != "Local Marketplace" {
		t.Fatalf("marketplace interface = %#v", marketplaceInterface)
	}
	if _, ok := marketplaceInterface["legacy"]; ok {
		t.Fatalf("marketplace interface leaked extension: %#v", marketplaceInterface)
	}
	read, err := service.Read(&PluginReadParams{PluginName: "sample", MarketplaceName: "local"})
	if err != nil || len(read.Plugin.Skills) != 1 {
		t.Fatalf("read = %#v/%v", read, err)
	}
	skillRead, err := service.ReadSkill(&PluginSkillReadParams{RemotePluginID: "remote-1", SkillName: "skill-a"})
	if err != nil {
		t.Fatalf("ReadSkill() error = %v", err)
	}
	skillReadPayload := marshalObject(t, skillRead)
	if _, ok := skillReadPayload["markdown"]; ok {
		t.Fatalf("legacy skill markdown should not be emitted: %#v", skillReadPayload)
	}
	installed, err := service.Install(&PluginInstallParams{PluginID: "sample@local"})
	if err != nil || installed.PluginID != "sample@local" {
		t.Fatalf("install = %#v/%v", installed, err)
	}
	encodedInstall, err := json.Marshal(installed)
	if err != nil {
		t.Fatalf("Marshal install returned error: %v", err)
	}
	var installPayload map[string]any
	if err := json.Unmarshal(encodedInstall, &installPayload); err != nil {
		t.Fatalf("Unmarshal install returned error: %v", err)
	}
	if _, ok := installPayload["pluginId"]; ok {
		t.Fatalf("legacy pluginId key should not be emitted: %#v", installPayload)
	}
	readParamsPayload := marshalObject(t, &PluginReadParams{
		MarketplaceName:       "legacy-local",
		MarketplacePath:       "/marketplaces/local",
		RemoteMarketplaceName: "remote-catalog",
		RemotePluginID:        "remote-1",
		PluginName:            "sample",
	})
	for _, legacyKey := range []string{"marketplaceName", "remotePluginId"} {
		if _, ok := readParamsPayload[legacyKey]; ok {
			t.Fatalf("legacy read params key %q should not be emitted: %#v", legacyKey, readParamsPayload)
		}
	}
	if readParamsPayload["remoteMarketplaceName"] != "remote-catalog" || readParamsPayload["pluginName"] != "sample" {
		t.Fatalf("read params payload = %#v", readParamsPayload)
	}
	readNullablePayload := marshalObject(t, &PluginReadParams{MarketplacePath: "/marketplaces/local", PluginName: "sample"})
	if value, ok := readNullablePayload["remoteMarketplaceName"]; !ok || value != nil {
		t.Fatalf("read nullable remote marketplace = %#v in %#v", value, readNullablePayload)
	}
	installParamsPayload := marshalObject(t, &PluginInstallParams{
		PluginID:              "sample@local",
		MarketplaceName:       "legacy-local",
		RemoteMarketplaceName: "remote-catalog",
		PluginName:            "sample",
	})
	for _, legacyKey := range []string{"pluginId", "marketplaceName"} {
		if _, ok := installParamsPayload[legacyKey]; ok {
			t.Fatalf("legacy install params key %q should not be emitted: %#v", legacyKey, installParamsPayload)
		}
	}
	if installParamsPayload["remoteMarketplaceName"] != "remote-catalog" || installParamsPayload["pluginName"] != "sample" {
		t.Fatalf("install params payload = %#v", installParamsPayload)
	}
	installNullablePayload := marshalObject(t, &PluginInstallParams{MarketplacePath: "/marketplaces/local", PluginName: "sample"})
	if value, ok := installNullablePayload["remoteMarketplaceName"]; !ok || value != nil {
		t.Fatalf("install nullable remote marketplace = %#v in %#v", value, installNullablePayload)
	}
	summaryEncoded, err := json.Marshal(list.Plugins[0])
	if err != nil {
		t.Fatalf("Marshal summary returned error: %v", err)
	}
	var summaryPayload map[string]any
	if err := json.Unmarshal(summaryEncoded, &summaryPayload); err != nil {
		t.Fatalf("Unmarshal summary returned error: %v", err)
	}
	for _, legacyKey := range []string{"displayName", "description", "marketplaceName", "hasSkills", "mcpServers", "appConnectors", "installSuggestion", "pluginDisplayNameTag"} {
		if _, ok := summaryPayload[legacyKey]; ok {
			t.Fatalf("legacy summary key %q should not be emitted: %#v", legacyKey, summaryPayload)
		}
	}
	detailEncoded, err := json.Marshal(&PluginDetail{
		MarketplaceName: "local",
		Summary:         list.Plugins[0],
		ManifestPath:    "manifest.json",
		MarketplaceRoot: "root",
		Apps:            []AppSummary{{ID: "app", DisplayName: "App"}},
		AppTemplates:    []AppTemplateSummary{{ID: "template", DisplayName: "Template"}},
	})
	if err != nil {
		t.Fatalf("Marshal detail returned error: %v", err)
	}
	var detailPayload map[string]any
	if err := json.Unmarshal(detailEncoded, &detailPayload); err != nil {
		t.Fatalf("Unmarshal detail returned error: %v", err)
	}
	for _, legacyKey := range []string{"manifestPath", "marketplaceRoot"} {
		if _, ok := detailPayload[legacyKey]; ok {
			t.Fatalf("legacy detail key %q should not be emitted: %#v", legacyKey, detailPayload)
		}
	}
	appPayload := detailPayload["apps"].([]any)[0].(map[string]any)
	if _, ok := appPayload["displayName"]; ok {
		t.Fatalf("legacy app displayName should not be emitted: %#v", appPayload)
	}
	templatePayload := detailPayload["appTemplates"].([]any)[0].(map[string]any)
	for _, legacyKey := range []string{"id", "displayName"} {
		if _, ok := templatePayload[legacyKey]; ok {
			t.Fatalf("legacy template key %q should not be emitted: %#v", legacyKey, templatePayload)
		}
	}
	if got := service.Installed(&PluginInstalledParams{}); len(got.Plugins) != 1 {
		t.Fatalf("installed list = %#v", got)
	}
	installedParamsPayload := marshalObject(t, &PluginInstalledParams{
		InstallSuggestionPluginNames: []string{"computer-use"},
	})
	if value, ok := installedParamsPayload["cwds"]; !ok || value != nil {
		t.Fatalf("installed params cwds = %#v in %#v", value, installedParamsPayload)
	}
	if suggestions, ok := installedParamsPayload["installSuggestionPluginNames"].([]any); !ok || len(suggestions) != 1 || suggestions[0] != "computer-use" {
		t.Fatalf("installed params suggestions = %#v", installedParamsPayload)
	}
	if _, err := service.SaveShare(&PluginShareSaveParams{RemotePluginID: "remote-1", Discoverability: "workspace"}); err != nil {
		t.Fatalf("SaveShare() error = %v", err)
	}
	shares := service.ListShares(&PluginShareListParams{})
	if len(shares.Items) != 1 {
		t.Fatalf("shares missing")
	}
	sharesPayload := marshalObject(t, shares)
	if _, ok := sharesPayload["items"]; ok {
		t.Fatalf("legacy share items should not be emitted: %#v", sharesPayload)
	}
	shareContextPayload := marshalObject(t, &shares.Items[0])
	if _, ok := shareContextPayload["principals"]; ok {
		t.Fatalf("legacy share principals should not be emitted: %#v", shareContextPayload)
	}
	principalPayload := marshalObject(t, &PluginSharePrincipal{
		Type: " user ",
		ID:   " user-1 ",
		Role: " reader ",
		Name: " Ada ",
	})
	for _, legacyKey := range []string{"type", "id"} {
		if _, ok := principalPayload[legacyKey]; ok {
			t.Fatalf("legacy principal key %q should not be emitted: %#v", legacyKey, principalPayload)
		}
	}
	if principalPayload["principalType"] != "user" || principalPayload["principalId"] != "user-1" || principalPayload["name"] != "Ada" {
		t.Fatalf("principal payload = %#v", principalPayload)
	}
	saveParamsPayload := marshalObject(t, &PluginShareSaveParams{
		PluginPath: "/plugins/sample",
		Targets: []PluginSharePrincipal{{
			Type: " user ",
			ID:   " user-1 ",
			Role: " reader ",
			Name: " Ada ",
		}},
	})
	if _, ok := saveParamsPayload["targets"]; ok {
		t.Fatalf("legacy targets should not be emitted: %#v", saveParamsPayload)
	}
	saveTargets := saveParamsPayload["shareTargets"].([]any)
	saveTarget := saveTargets[0].(map[string]any)
	if _, ok := saveTarget["name"]; ok {
		t.Fatalf("share target should not include principal name: %#v", saveTarget)
	}
	if saveTarget["principalType"] != "user" || saveTarget["principalId"] != "user-1" || saveTarget["role"] != "reader" {
		t.Fatalf("save share target = %#v", saveTarget)
	}
	saveNullablePayload := marshalObject(t, &PluginShareSaveParams{PluginPath: "/plugins/sample"})
	for _, nullableKey := range []string{"remotePluginId", "discoverability", "shareTargets"} {
		if value, ok := saveNullablePayload[nullableKey]; !ok || value != nil {
			t.Fatalf("save nullable key %q = %#v in %#v", nullableKey, value, saveNullablePayload)
		}
	}
	checkoutPayload := marshalObject(t, &PluginShareCheckoutResponse{
		RemotePluginID:  "remote-1",
		PluginID:        "sample@local",
		PluginName:      "sample",
		PluginPath:      "/plugins/sample",
		MarketplaceName: "local",
		MarketplacePath: "/marketplaces/local",
		LocalPath:       "/legacy/local/path",
	})
	if _, ok := checkoutPayload["localPath"]; ok {
		t.Fatalf("legacy checkout localPath should not be emitted: %#v", checkoutPayload)
	}
	if value, ok := checkoutPayload["remoteVersion"]; !ok || value != nil {
		t.Fatalf("checkout remoteVersion = %#v in %#v", value, checkoutPayload)
	}
	updateParamsPayload := marshalObject(t, &PluginShareUpdateTargetsParams{
		RemotePluginID:  "remote-1",
		Discoverability: "PRIVATE",
	})
	if _, ok := updateParamsPayload["pluginPath"]; ok {
		t.Fatalf("update targets should not include pluginPath: %#v", updateParamsPayload)
	}
	if targets, ok := updateParamsPayload["shareTargets"].([]any); !ok || len(targets) != 0 {
		t.Fatalf("update shareTargets = %#v", updateParamsPayload["shareTargets"])
	}
	updatedShare, err := service.UpdateShareTargets(&PluginShareUpdateTargetsParams{
		RemotePluginID:  " remote-1 ",
		Discoverability: "PRIVATE",
		ShareTargets: []PluginSharePrincipal{{
			Type: " user ",
			ID:   " user-2 ",
			Role: " reader ",
			Name: " Ada ",
		}},
	})
	if err != nil {
		t.Fatalf("UpdateShareTargets() error = %v", err)
	}
	if updatedShare.Discoverability != "PRIVATE" || len(updatedShare.Principals) != 1 {
		t.Fatalf("updated share = %#v", updatedShare)
	}
	updatedPrincipal := updatedShare.Principals[0]
	if updatedPrincipal.Type != "" || updatedPrincipal.ID != "" || updatedPrincipal.PrincipalType != "user" || updatedPrincipal.PrincipalID != "user-2" || updatedPrincipal.Role != "reader" || updatedPrincipal.Name != "Ada" {
		t.Fatalf("updated principal = %#v", updatedPrincipal)
	}
	checkout, err := service.CheckoutShare(&PluginShareCheckoutParams{RemotePluginID: " remote-1 "})
	if err != nil {
		t.Fatalf("CheckoutShare() error = %v", err)
	}
	if checkout.RemotePluginID != "remote-1" || checkout.PluginID != "remote-1" || !strings.HasSuffix(checkout.PluginPath, "remote-1") {
		t.Fatalf("checkout = %#v", checkout)
	}
	if _, err := service.DeleteShare(&PluginShareDeleteParams{RemotePluginID: " remote-1 "}); err != nil {
		t.Fatalf("DeleteShare() error = %v", err)
	}
	if len(service.ListShares(&PluginShareListParams{}).Items) != 0 {
		t.Fatalf("share not deleted")
	}
	if _, err := service.Install(nil); !errors.Is(err, ErrInvalidPluginRequest) {
		t.Fatalf("Install(nil) error = %v, want ErrInvalidPluginRequest", err)
	}
	if _, err := service.ReadSkill(&PluginSkillReadParams{}); !errors.Is(err, ErrInvalidPluginRequest) {
		t.Fatalf("ReadSkill(empty) error = %v, want ErrInvalidPluginRequest", err)
	}
}

func TestPluginInstallPolicyAndAuthApps(t *testing.T) {
	service := NewPluginService()
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			Name:            "blocked",
			MarketplaceName: "local",
			DisplayName:     "Blocked",
			InstallPolicy:   InstallBlocked,
		},
	})
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			Name:            "needs-auth",
			MarketplaceName: "local",
			DisplayName:     "Needs Auth",
			AuthPolicy:      AuthOnInstall,
		},
		Apps: []AppSummary{{ID: "app-1", DisplayName: "App One"}},
	})

	if _, err := service.Install(&PluginInstallParams{PluginName: "needs-auth"}); !errors.Is(err, ErrInvalidPluginRequest) || !strings.Contains(err.Error(), "requires exactly one") {
		t.Fatalf("Install(missing source) error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "needs-auth", MarketplaceName: "local", RemoteMarketplaceName: "remote"}); !errors.Is(err, ErrInvalidPluginRequest) || !strings.Contains(err.Error(), "requires exactly one") {
		t.Fatalf("Install(multiple sources) error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginID: "blocked@local"}); !errors.Is(err, ErrInvalidPluginRequest) || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("Install(blocked) error = %v", err)
	}
	installed, err := service.Install(&PluginInstallParams{PluginName: "needs-auth", MarketplaceName: "local"})
	if err != nil {
		t.Fatalf("Install(needs-auth) error = %v", err)
	}
	if installed.AuthPolicy != AuthOnInstall || len(installed.AppsNeedingAuth) != 1 || installed.AppsNeedingAuth[0].ID != "app-1" {
		t.Fatalf("installed = %#v", installed)
	}
	if got := service.Installed(&PluginInstalledParams{}); len(got.Plugins) != 1 || got.Plugins[0].ID != "needs-auth@local" {
		t.Fatalf("installed list = %#v", got)
	}
	if _, err := service.Uninstall(&PluginUninstallParams{PluginID: "needs-auth@local"}); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if got := service.Installed(&PluginInstalledParams{}); len(got.Plugins) != 0 {
		t.Fatalf("installed after uninstall = %#v", got)
	}
	read, err := service.Read(&PluginReadParams{PluginName: "needs-auth", MarketplaceName: "local"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Plugin.Summary.Enabled {
		t.Fatalf("plugin should be disabled after uninstall: %#v", read.Plugin.Summary)
	}
}

type pluginShareBackendFunc func(*PluginShareSaveParams) (*PluginShareSaveResponse, error)

func (f pluginShareBackendFunc) SaveShare(params *PluginShareSaveParams) (*PluginShareSaveResponse, error) {
	return f(params)
}

func TestPluginShareBackendPropagatesWorkspacePublishCapability(t *testing.T) {
	allowed := true
	service := NewPluginService()
	service.SetShareBackend(pluginShareBackendFunc(func(params *PluginShareSaveParams) (*PluginShareSaveResponse, error) {
		if params.RemotePluginID != "local-id" || params.Discoverability != "PRIVATE" || len(params.ShareTargets) != 1 {
			t.Fatalf("backend params = %#v", params)
		}
		return &PluginShareSaveResponse{
			RemotePluginID:        "remote-id",
			ShareURL:              "https://example.test/share/remote-id",
			CanPublishToWorkspace: &allowed,
		}, nil
	}))
	response, err := service.SaveShare(&PluginShareSaveParams{
		RemotePluginID:  " local-id ",
		Discoverability: " PRIVATE ",
		Targets: []PluginSharePrincipal{{
			Type: "workspace", ID: "workspace-1", Role: "reader",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.RemotePluginID != "remote-id" || response.CanPublishToWorkspace == nil || !*response.CanPublishToWorkspace {
		t.Fatalf("response = %#v", response)
	}
	shares := service.ListShares(&PluginShareListParams{})
	if len(shares.Items) != 1 || shares.Items[0].CanPublishToWorkspace == nil || !*shares.Items[0].CanPublishToWorkspace {
		t.Fatalf("shares = %#v", shares.Items)
	}

	fallback := NewPluginService()
	fallbackResponse, err := fallback.SaveShare(&PluginShareSaveParams{RemotePluginID: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	if fallbackResponse.CanPublishToWorkspace != nil {
		t.Fatalf("offline capability = %#v, want nil", fallbackResponse.CanPublishToWorkspace)
	}
}

func TestDiscoverableInstallCandidatesIncludesInstalledPluginConnectors(t *testing.T) {
	description := "Track work"
	installURL := "https://chatgpt.com/connectors/linear"
	slackID := "slack"
	service := NewPluginService()
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			Name:            "linear-plugin",
			MarketplaceName: "local",
			DisplayName:     "Linear Plugin",
			Installed:       true,
			Enabled:         true,
		},
		Apps: []AppSummary{{
			ID:          "linear",
			DisplayName: "Linear",
			Description: &description,
			InstallURL:  &installURL,
		}},
		AppTemplates: []AppTemplateSummary{{
			TemplateID:           "slack-template",
			DisplayName:          "Slack Template",
			Description:          &description,
			CanonicalConnectorID: &slackID,
		}},
	})
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			Name:            "linear-helper",
			MarketplaceName: "local",
			DisplayName:     "Linear Helper",
			Installed:       true,
			Enabled:         true,
		},
		Apps: []AppSummary{{
			ID:          "linear",
			DisplayName: "Linear",
		}},
	})
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			Name:            "docs",
			MarketplaceName: "local",
			DisplayName:     "Docs",
			AppConnectors:   []string{"docs-app"},
		},
	})

	candidates := service.DiscoverableInstallCandidates()
	byID := map[string]DiscoverableInfo{}
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	if _, ok := byID["linear-plugin@local"]; ok {
		t.Fatalf("installed plugin should not be a plugin install candidate: %#v", candidates)
	}
	linear := byID["linear"]
	if linear.ToolType != "connector" || linear.Name != "Linear" || linear.Description != "Track work" || linear.InstallURL != installURL {
		t.Fatalf("linear connector candidate = %#v", linear)
	}
	if strings.Join(linear.AppConnectorIDs, ",") != "linear" {
		t.Fatalf("linear connector ids = %#v", linear.AppConnectorIDs)
	}
	linearPluginNames := map[string]bool{}
	for _, name := range linear.PluginDisplayNames {
		linearPluginNames[name] = true
	}
	if !linearPluginNames["Linear Plugin"] || !linearPluginNames["Linear Helper"] {
		t.Fatalf("linear plugin display names = %#v", linear.PluginDisplayNames)
	}
	slack := byID["slack"]
	if slack.ToolType != "connector" || slack.Name != "Slack Template" || slack.Description != "Track work" || slack.InstallURL != "https://chatgpt.com/apps/slack-template/slack" {
		t.Fatalf("slack connector candidate = %#v", slack)
	}
	if strings.Join(slack.PluginDisplayNames, ",") != "Linear Plugin" {
		t.Fatalf("slack plugin display names = %#v", slack.PluginDisplayNames)
	}
	docs := byID["docs@local"]
	if docs.ToolType != "plugin" || strings.Join(docs.AppConnectorIDs, ",") != "docs-app" {
		t.Fatalf("docs plugin candidate = %#v", docs)
	}
}

func TestDiscoverableInstallCandidatesRematerializesInstalledRemotePluginConnectors(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeTestRemoteMarketplacePlugin(t, root, "remote-app", "plugin")
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		writeMaterializedTestPluginWithApp(t, filepath.Join(destination, "plugin"), "remote-app", "linear")
		return nil
	}))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace(local) error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "remote-app", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install(remote-app) error = %v", err)
	}

	materializedRoot := filepath.Join(home, InstalledMarketplacesDir, InstalledMarketplacePluginsDir, "debug", "remote-app")
	if err := os.RemoveAll(materializedRoot); err != nil {
		t.Fatalf("RemoveAll(materialized root) error = %v", err)
	}
	reloaded := NewPluginService()
	reloaded.SetCodexHome(home)
	materializeCalls := 0
	reloaded.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		materializeCalls++
		if destination != materializedRoot {
			t.Fatalf("materialized destination = %q, want %q", destination, materializedRoot)
		}
		writeMaterializedTestPluginWithApp(t, filepath.Join(destination, "plugin"), "remote-app", "linear")
		return nil
	}))

	candidates := reloaded.DiscoverableInstallCandidates()
	if materializeCalls != 1 {
		t.Fatalf("materialize calls = %d, want 1", materializeCalls)
	}
	byID := map[string]DiscoverableInfo{}
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	linear := byID["linear"]
	if linear.ToolType != "connector" || linear.Name != "Linear" || linear.InstallURL != "https://chatgpt.com/connectors/linear" {
		t.Fatalf("linear connector candidate = %#v", linear)
	}
	if strings.Join(linear.PluginDisplayNames, ",") != "Remote App Plugin" {
		t.Fatalf("linear plugin names = %#v", linear.PluginDisplayNames)
	}
}

func marshalObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	return payload
}

func readMarketplaceConfigForTest(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	values := map[string]any{}
	if err := toml.Unmarshal(data, &values); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return values
}

func marketplaceConfigEntryForTest(t *testing.T, values map[string]any, name string) map[string]any {
	t.Helper()
	marketplaces, ok := values["marketplaces"].(map[string]any)
	if !ok {
		t.Fatalf("marketplaces config missing: %#v", values)
	}
	entry, ok := marketplaces[name].(map[string]any)
	if !ok {
		t.Fatalf("marketplace %q config missing: %#v", name, marketplaces)
	}
	return entry
}

func writeTestMarketplacePlugin(t *testing.T, root string, pluginName string) {
	t.Helper()
	marketplaceDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(marketplaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(marketplace) error = %v", err)
	}
	manifest := `{
  "name": "debug",
  "plugins": [
    {
      "name": "` + pluginName + `",
      "source": {
        "source": "local",
        "path": "./plugins/` + pluginName + `"
      }
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, "marketplace.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(marketplace) error = %v", err)
	}
	pluginRoot := filepath.Join(root, "plugins", pluginName)
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin manifest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{
  "name": "`+pluginName+`",
  "description": "Plugin that includes skills and MCP servers"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin manifest) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(skills) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "SKILL.md"), []byte("---\nname: sample\ndescription: sample\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".mcp.json"), []byte(`{"mcpServers":{"sample-docs":{"type":"http","url":"https://sample.example/mcp"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(mcp) error = %v", err)
	}
}

func writeTestRemoteMarketplacePlugin(t *testing.T, root string, pluginName string, pluginPath string) {
	t.Helper()
	marketplaceDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(marketplaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(marketplace) error = %v", err)
	}
	manifest := `{
  "name": "debug",
  "plugins": [
    {
      "name": "` + pluginName + `",
      "source": {
        "source": "git",
        "url": "https://github.com/acme/` + pluginName + `.git",
        "ref": "main",
        "path": "` + pluginPath + `"
      }
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, "marketplace.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(remote marketplace) error = %v", err)
	}
}

func writeMaterializedTestPlugin(t *testing.T, pluginRoot string, pluginName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin manifest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{
  "name": "`+pluginName+`",
  "description": "Materialized remote plugin"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin manifest) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(skills) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "SKILL.md"), []byte("---\nname: remote\ndescription: remote\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".mcp.json"), []byte(`{"mcpServers":{"`+pluginName+`-docs":{"type":"http","url":"https://sample.example/mcp"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(mcp) error = %v", err)
	}
}

func writeMaterializedTestPluginWithApp(t *testing.T, pluginRoot string, pluginName string, appID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin manifest) error = %v", err)
	}
	manifest := `{
  "name": "` + pluginName + `",
  "displayName": "Remote App Plugin",
  "description": "Materialized remote app plugin",
  "apps": [
    {
      "id": "` + appID + `",
      "displayName": "Linear",
      "description": "Track work",
      "installUrl": "https://chatgpt.com/connectors/` + appID + `"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin manifest) error = %v", err)
	}
}

func writeVersionedMarketplacePlugin(t *testing.T, root string, pluginName string, skillName string, mcpServer string, description string) {
	t.Helper()
	marketplaceDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(marketplaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(marketplace) error = %v", err)
	}
	manifest := `{
  "name": "debug",
  "plugins": [
    {
      "name": "` + pluginName + `",
      "source": {
        "source": "local",
        "path": "./plugins/` + pluginName + `"
      }
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, "marketplace.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(marketplace) error = %v", err)
	}
	writeVersionedPluginRoot(t, filepath.Join(root, "plugins", pluginName), pluginName, skillName, mcpServer, description)
}

func writeVersionedPluginRoot(t *testing.T, pluginRoot string, pluginName string, skillName string, mcpServer string, description string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin manifest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{
  "name": "`+pluginName+`",
  "description": "`+description+`"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin manifest) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(skills) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "SKILL.md"), []byte("---\nname: "+skillName+"\ndescription: "+skillName+"\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".mcp.json"), []byte(`{"mcpServers":{"`+mcpServer+`":{"type":"http","url":"https://sample.example/mcp"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(mcp) error = %v", err)
	}
}

func fakeMarketplaceMaterializer(t *testing.T) MarketplaceMaterializer {
	t.Helper()
	return MarketplaceMaterializerFunc(func(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
		if source != nil && source.Kind == MarketplaceSourceGit {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(destination, "materialized.txt"), []byte(source.Display()), 0o600)
		}
		return nil
	})
}

type versionedMarketplaceUpgrader struct {
	materialize func(destination string) error
	upgrade     func(destination string) error
}

func (u *versionedMarketplaceUpgrader) MaterializeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	if u.materialize == nil {
		return nil
	}
	return u.materialize(destination)
}

func (u *versionedMarketplaceUpgrader) UpgradeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	if u.upgrade == nil {
		return u.MaterializeMarketplace(source, sparsePaths, destination)
	}
	return u.upgrade(destination)
}

type recordingMarketplaceUpgrader struct {
	materialized []string
	upgraded     []string
}

func (r *recordingMarketplaceUpgrader) MaterializeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	r.materialized = append(r.materialized, destination)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "materialized.txt"), []byte(source.Display()), 0o600)
}

func (r *recordingMarketplaceUpgrader) UpgradeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	r.upgraded = append(r.upgraded, destination)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "upgraded.txt"), []byte(source.Display()), 0o600)
}

func TestEnabledHookSourcesUsesPluginRoot(t *testing.T) {
	root := t.TempDir()
	service := NewPluginService()
	service.AddPlugin(PluginDetail{
		Summary:      PluginSummary{Name: "sample", MarketplaceName: "local", Installed: true, Enabled: true},
		Hooks:        []PluginHookSummary{{Key: "hook-1", Enabled: true}},
		ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json"),
	})

	sources := service.EnabledHookSources()
	if len(sources) != 1 {
		t.Fatalf("EnabledHookSources() = %+v", sources)
	}
	source := sources[0]
	if source.PluginID != "sample@local" || source.PluginRoot != root {
		t.Fatalf("source identity = %+v", source)
	}
	if source.SourcePath != filepath.Join(root, "hooks", "hooks.json") {
		t.Fatalf("SourcePath = %q", source.SourcePath)
	}
	if source.PluginDataRoot != filepath.Join(root, "data") || source.SourceRelativePath != "hooks/hooks.json" {
		t.Fatalf("source roots = %+v", source)
	}
}

func TestEnabledHookSourcesSkipsUninstalledPlugins(t *testing.T) {
	root := t.TempDir()
	service := NewPluginService()
	service.AddPlugin(PluginDetail{
		Summary:      PluginSummary{Name: "sample", MarketplaceName: "local", Enabled: true},
		Hooks:        []PluginHookSummary{{Key: "hook-1", Enabled: true}},
		ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json"),
	})

	if sources := service.EnabledHookSources(); len(sources) != 0 {
		t.Fatalf("EnabledHookSources() = %+v, want no hooks for uninstalled plugin", sources)
	}
}
