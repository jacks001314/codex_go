package systemskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundledSkillAssetsMatchUpstreamLikeRust pins the sample assets to the
// upstream spellings and formatting: the statusline picker label from #62037
// and the formatted helper scripts the Rust repository ships.
func TestBundledSkillAssetsMatchUpstreamLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := Install(home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	root := CacheRoot(home)
	read := func(relative string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relative, err)
		}
		// Git may check the embedded assets out with CRLF; compare text form.
		return strings.ReplaceAll(string(data), "\r\n", "\n")
	}

	statusline := read("create-statusline/SKILL.md")
	for _, want := range []string{
		"choose **Choose a status line provider**",
		"Exercise `tools/list` and `resources/read` with a representative snapshot.",
	} {
		if !strings.Contains(statusline, want) {
			t.Fatalf("create-statusline SKILL.md is missing %q", want)
		}
	}
	if strings.Contains(statusline, "Choose a statusline plugin") {
		t.Fatal("create-statusline SKILL.md still carries the old picker label")
	}

	if got := read("skill-installer/scripts/list-skills.py"); !strings.Contains(got, `f"Skills path not found: https://github.com/{repo}/tree/{ref}/{path}"`) {
		t.Fatal("list-skills.py did not pick up the combined not-found message")
	}

	for _, check := range []struct {
		relative string
		want     string
	}{
		{"skill-creator/scripts/init_skill.py", "def create_resource_dirs(\n    skill_dir, skill_name, skill_title, resources, include_examples\n):"},
		{"skill-creator/scripts/quick_validate.py", "if fence_marker is None and re.fullmatch(\n"},
		{"skill-creator/scripts/generate_openai_yaml.py", "short_description = overrides.get(\n        \"short_description\"\n    ) or generate_short_description(display_name)"},
		{"skill-installer/scripts/install-skill-from-github.py", "    result = subprocess.run(\n        args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True\n    )"},
		{"imagegen/scripts/image_gen.py", "        _die(\n            \"gpt-image-2 size maximum edge length must be less than or equal to 3840px.\"\n        )"},
		{"imagegen/scripts/remove_chroma_key.py", "print(\n            \"Warning: no pixels matched the key color before feathering.\",\n            file=sys.stderr,\n        )"},
	} {
		if got := read(check.relative); !strings.Contains(got, check.want) {
			t.Fatalf("%s does not match the upstream formatting", check.relative)
		}
	}
}
