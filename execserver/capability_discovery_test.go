package execserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverCapabilityRootsReturnsCompleteRustBundle(t *testing.T) {
	root := t.TempDir()
	writeCapabilityFixture(t, root, ".codex-plugin/plugin.json", `{
  "name": "demo",
  "interface": {"displayName": "Demo Plugin"},
  "mcpServers": "./config/mcp.json",
  "apps": "./config/apps.json"
}`)
	writeCapabilityFixture(t, root, ".claude-plugin/plugin.json", `{"name":"lower-priority"}`)
	writeCapabilityFixture(t, root, "config/mcp.json", `{"mcpServers":{"demo":{"command":"demo-server"}}}`)
	writeCapabilityFixture(t, root, "config/apps.json", `{"apps":{"demo":{"connector_id":"connector-demo"}}}`)
	writeCapabilityFixture(t, root, "skills/deploy/SKILL.md", "# Deploy\n")
	writeCapabilityFixture(t, root, "skills/deploy/agents/openai.yaml", "policy: {}\n")
	writeCapabilityFixture(t, root, "nested/.claude-plugin/plugin.json", `{"name":"nested"}`)
	writeCapabilityFixture(t, root, "nested/skills/audit/SKILL.md", "# Audit\n")
	writeCapabilityFixture(t, root, "nested-cursor/.cursor-plugin/plugin.json", `{"name":"cursor-nested"}`)
	writeCapabilityFixture(t, root, "nested-cursor/skills/review/SKILL.md", "# Review\n")

	rootURI := fileURI(root)
	response, err := discoverCapabilityRoots(&CapabilityRootsDiscoverParams{Roots: []CapabilityRootDiscoverRequest{{ID: "demo@1", Path: rootURI}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Roots) != 1 {
		t.Fatalf("roots = %#v", response.Roots)
	}
	discovery := response.Roots[0]
	if discovery.ID != "demo@1" || discovery.Path != rootURI || discovery.Error != nil || len(discovery.Warnings) != 0 {
		t.Fatalf("discovery header = %#v", discovery)
	}
	if discovery.Plugin == nil || !strings.Contains(discovery.Plugin.Manifest.Contents, "Demo Plugin") {
		t.Fatalf("plugin = %#v", discovery.Plugin)
	}
	if discovery.Plugin.MCPConfig == nil || !strings.HasSuffix(discovery.Plugin.MCPConfig.Path, "/config/mcp.json") {
		t.Fatalf("mcp config = %#v", discovery.Plugin.MCPConfig)
	}
	if discovery.Plugin.AppsConfig == nil || !strings.HasSuffix(discovery.Plugin.AppsConfig.Path, "/config/apps.json") {
		t.Fatalf("apps config = %#v", discovery.Plugin.AppsConfig)
	}
	if len(discovery.Skills) != 3 || !strings.HasSuffix(discovery.Skills[0].Instructions.Path, "/nested-cursor/skills/review/SKILL.md") || !strings.HasSuffix(discovery.Skills[2].Instructions.Path, "/skills/deploy/SKILL.md") || discovery.Skills[2].Metadata == nil {
		t.Fatalf("skills = %#v", discovery.Skills)
	}
	if len(discovery.NamespaceManifests) != 3 || !strings.HasSuffix(discovery.NamespaceManifests[0].Path, "/.codex-plugin/plugin.json") || !strings.HasSuffix(discovery.NamespaceManifests[1].Path, "/nested/.claude-plugin/plugin.json") || !strings.HasSuffix(discovery.NamespaceManifests[2].Path, "/nested-cursor/.cursor-plugin/plugin.json") {
		t.Fatalf("namespace manifests = %#v", discovery.NamespaceManifests)
	}
}

func TestDiscoverCapabilityRootsMatchesRustInlineAndUnsafeDeclarations(t *testing.T) {
	inlineRoot := t.TempDir()
	writeCapabilityFixture(t, inlineRoot, ".cursor-plugin/plugin.json", `{"name":"cursor","mcpServers":{"inline":{"command":"inline"}}}`)
	writeCapabilityFixture(t, inlineRoot, ".mcp.json", `{"mcpServers":{"wrong":{"command":"wrong"}}}`)
	response, err := discoverCapabilityRoots(&CapabilityRootsDiscoverParams{Roots: []CapabilityRootDiscoverRequest{{ID: "inline", Path: fileURI(inlineRoot)}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Roots[0].Plugin == nil || response.Roots[0].Plugin.MCPConfig != nil || len(response.Roots[0].Warnings) != 0 {
		t.Fatalf("inline discovery = %#v", response.Roots[0])
	}

	unsafeRoot := t.TempDir()
	writeCapabilityFixture(t, unsafeRoot, ".codex-plugin/plugin.json", `{"name":"unsafe","mcpServers":"../outside.json","apps":"apps.json"}`)
	response, err = discoverCapabilityRoots(&CapabilityRootsDiscoverParams{Roots: []CapabilityRootDiscoverRequest{{ID: "unsafe", Path: fileURI(unsafeRoot)}}})
	if err != nil {
		t.Fatal(err)
	}
	discovery := response.Roots[0]
	if discovery.Plugin == nil || discovery.Plugin.MCPConfig != nil || discovery.Plugin.AppsConfig != nil || len(discovery.Warnings) != 2 {
		t.Fatalf("unsafe discovery = %#v", discovery)
	}
	if !strings.Contains(discovery.Warnings[0], "path must start with `./`") || !strings.Contains(discovery.Warnings[1], "path must start with `./`") {
		t.Fatalf("warnings = %#v", discovery.Warnings)
	}
}

func TestDiscoverCapabilityRootsUsesNearestAncestorNamespace(t *testing.T) {
	pluginRoot := t.TempDir()
	writeCapabilityFixture(t, pluginRoot, ".claude-plugin/plugin.json", `{"name":"ancestor"}`)
	skillRoot := filepath.Join(pluginRoot, "skills", "one")
	writeCapabilityFixture(t, pluginRoot, "skills/one/SKILL.md", "# One\n")
	response, err := discoverCapabilityRoots(&CapabilityRootsDiscoverParams{Roots: []CapabilityRootDiscoverRequest{{ID: "skill", Path: fileURI(skillRoot)}}})
	if err != nil {
		t.Fatal(err)
	}
	discovery := response.Roots[0]
	if discovery.Plugin != nil || len(discovery.NamespaceManifests) != 1 || !strings.HasSuffix(discovery.NamespaceManifests[0].Path, "/.claude-plugin/plugin.json") {
		t.Fatalf("ancestor discovery = %#v", discovery)
	}
}

func TestClientDiscoverCapabilityRootsBatchesMoreThanExecutorLimit(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	client, err := DialClient(context.Background(), serverURL, "capability-batch-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	root := fileURI(t.TempDir())
	roots := make([]CapabilityRootDiscoverRequest, maxCapabilityDiscoveryRoots+1)
	for i := range roots {
		roots[i] = CapabilityRootDiscoverRequest{ID: fmt.Sprintf("root-%03d", i), Path: root}
	}
	response, err := client.DiscoverCapabilityRoots(context.Background(), &CapabilityRootsDiscoverParams{Roots: roots})
	if err != nil {
		t.Fatalf("DiscoverCapabilityRoots() error = %v", err)
	}
	if len(response.Roots) != len(roots) || response.Roots[0].ID != "root-000" || response.Roots[len(response.Roots)-1].ID != "root-128" {
		t.Fatalf("response roots = %#v", response.Roots)
	}
	cancelServer()
	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func writeCapabilityFixture(t *testing.T, root, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileURI(path string) string {
	return "file:///" + filepath.ToSlash(path)
}
