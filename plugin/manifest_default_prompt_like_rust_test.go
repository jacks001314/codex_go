package plugin

import (
	"strings"
	"testing"
)

// Rust core-plugins/src/manifest.rs (#60048): a plugin starter prompt may be up
// to 512 characters, longer entries are dropped, and at most three survive.
func TestPluginManifestDefaultPromptsAllow512CharactersLikeRust(t *testing.T) {
	atLimit := strings.Repeat("技", maxPluginDefaultPromptRunes)
	overLimit := strings.Repeat("技", maxPluginDefaultPromptRunes+1)
	root := t.TempDir()
	writeAgentPluginManifestForTest(t, root, `,
  "extensions": {"com.openai":{"interface":{"defaultPrompt":["`+atLimit+`","`+overLimit+`","third","fourth","fifth"]}}}`)

	resolved, err := loadPluginManifest(root)
	if err != nil {
		t.Fatalf("loadPluginManifest() error = %v", err)
	}
	if resolved.Manifest.Interface == nil {
		t.Fatal("expected a plugin interface")
	}
	got := resolved.Manifest.Interface.DefaultPrompt
	want := []string{atLimit, "third", "fourth"}
	if len(got) != len(want) {
		t.Fatalf("DefaultPrompt = %d entries, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("DefaultPrompt[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}
