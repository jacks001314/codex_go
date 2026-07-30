package execserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverCapabilitiesFindsPluginAndSkillManifests(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, ".codex-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"sample"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "sample")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := discoverCapabilities(&CapabilityDiscoveryParams{Roots: []CapabilityDiscoveryRoot{{ID: "root", Path: fileURI(root)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Errors) != 0 || len(response.Manifests) != 2 || response.Manifests[0].Kind != "plugin" || response.Manifests[1].Kind != "skill" {
		t.Fatalf("response = %#v", response)
	}
}

func TestClientDiscoverCapabilitiesBatchesMoreThanExecutorLimit(t *testing.T) {
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
	roots := make([]CapabilityDiscoveryRoot, maxCapabilityDiscoveryRoots+1)
	for i := range roots {
		roots[i] = CapabilityDiscoveryRoot{ID: fmt.Sprintf("root-%03d", i), Path: root}
	}
	response, err := client.DiscoverCapabilities(context.Background(), &CapabilityDiscoveryParams{Roots: roots})
	if err != nil {
		t.Fatalf("DiscoverCapabilities() error = %v", err)
	}
	if len(response.Manifests) != 0 || len(response.Errors) != 0 {
		t.Fatalf("response = %#v", response)
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

func fileURI(path string) string {
	return "file:///" + filepath.ToSlash(path)
}
