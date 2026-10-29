package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectLayerConfigs lists the project-source layers of a config/read response.
func projectLayerConfigs(response *ConfigReadResponse) []Layer {
	var layers []Layer
	for _, layer := range response.Layers {
		if layer.Name.Type == LayerSourceProject {
			layers = append(layers, layer)
		}
	}
	return layers
}

// TestProjectlessFolderDoesNotPreapproveLaterProjectConfigLikeRust covers Rust
// #46328 end to end at the configuration boundary: reading the effective config
// for a projectless folder persists no trust decision, so project configuration
// added later is still not loaded until the folder is trusted explicitly.
func TestProjectlessFolderDoesNotPreapproveLaterProjectConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(home)

	read := func(t *testing.T) *ConfigReadResponse {
		t.Helper()
		response, err := service.Read(&ConfigReadParams{CWD: &cwd, IncludeLayers: true})
		if err != nil {
			t.Fatalf("ConfigService.Read() error = %v", err)
		}
		return response
	}

	first := read(t)
	if layers := projectLayerConfigs(first); len(layers) != 0 {
		t.Fatalf("projectless folder loaded project layers: %#v", layers)
	}
	if projects, ok := first.Config["projects"].(map[string]any); ok && len(projects) != 0 {
		t.Fatalf("reading a projectless folder persisted a trust decision: %#v", projects)
	}
	if data, err := os.ReadFile(ConfigPath(home)); err == nil && strings.Contains(string(data), "trust_level") {
		t.Fatalf("projectless read wrote a trust decision:\n%s", data)
	}

	// Adding project configuration after the fact must not be preapproved.
	dotCodex := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte("model = \"project-only-model\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := read(t)
	if layers := projectLayerConfigs(second); len(layers) != 0 {
		t.Fatalf("untrusted project configuration was loaded: %#v", layers)
	}
	if model, _ := second.Config["model"].(string); model == "project-only-model" {
		t.Fatal("project-local model leaked into the effective config without trust")
	}

	// A saved trust decision still loads the project layer, so the skip above is
	// about the missing decision and not about project discovery.
	key := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	body := "[projects.\"" + key + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	third := read(t)
	if layers := projectLayerConfigs(third); len(layers) == 0 {
		t.Fatalf("trusted folder did not load its project layer: %#v", third.Layers)
	}
	if model, _ := third.Config["model"].(string); model != "project-only-model" {
		t.Fatalf("trusted project model = %q, want project-only-model", model)
	}
}
