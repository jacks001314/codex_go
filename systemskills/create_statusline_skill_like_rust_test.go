package systemskills

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

const createStatuslineSkillFilename = "SKILL.md"

// Rust #62036 bundles `create-statusline` as an explicitly invoked skill with a
// Python stdio MCP starter and the #62034 reference contract. The embed tree is
// the only source of truth, so every asset must ship together.
func TestCreateStatuslineSkillBundlesAuthoringAssetsLikeRust(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := embeddedSamples.ReadFile(path.Join(embeddedRoot, "create-statusline", name))
		if err != nil {
			t.Fatalf("read bundled create-statusline/%s: %v", name, err)
		}
		return string(data)
	}
	skill := read(createStatuslineSkillFilename)
	for _, want := range []string{"name: create-statusline", "description: Build and enable a custom MCP statusline", "assets/statusline.py", "references/statusline.d.ts"} {
		if !strings.Contains(skill, want) {
			t.Fatalf("SKILL.md missing %q:\n%s", want, skill)
		}
	}
	agent := read("agents/openai.yaml")
	if !strings.Contains(agent, "allow_implicit_invocation: false") {
		t.Fatalf("agents/openai.yaml must keep the skill explicitly invoked: %q", agent)
	}
	starter := read("assets/statusline.py")
	for _, want := range []string{`"openai/ui"`, "resources/read", "com.openai/codex-statusline-state", "def render(state)"} {
		if !strings.Contains(starter, want) {
			t.Fatalf("statusline.py starter missing %q", want)
		}
	}
	contract := read("references/statusline.d.ts")
	for _, want := range []string{"export type Account =", "export type StatuslineState =", "export type UiTree ="} {
		if !strings.Contains(contract, want) {
			t.Fatalf("statusline.d.ts missing %q", want)
		}
	}
}

// The authoring skill is a system skill, so installing the bundle must place it
// under the cache root with its nested assets (Rust skills loader).
func TestCreateStatuslineSkillInstallsIntoCacheLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	if err := Install(codexHome); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, rel := range []string{
		"create-statusline/" + createStatuslineSkillFilename,
		"create-statusline/agents/openai.yaml",
		"create-statusline/assets/statusline.py",
		"create-statusline/references/statusline.d.ts",
	} {
		path := filepath.Join(CacheRoot(codexHome), filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("installed asset %s: %v", rel, err)
		}
	}
}
