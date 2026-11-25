package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/cli"
)

func TestInteractiveModelListSourceAcceptsProviderBearerTokenWithoutAccountAuthLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configText := `model_provider = "qax"

[model_providers.qax]
name = "qax"
base_url = "http://127.0.0.1:1/v1"
experimental_bearer_token = "test-provider-token"
wire_api = "responses"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	source, err := newInteractiveModelListSource(&cli.RootOptions{}, false, cwd, "qax", nil)
	if err != nil {
		t.Fatalf("newInteractiveModelListSource: %v", err)
	}
	if source == nil || source.manager == nil {
		t.Fatal("model list source was not created")
	}
}
