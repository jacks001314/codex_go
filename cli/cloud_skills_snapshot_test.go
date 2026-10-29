package cli

import "testing"

// TestAppServerParsesCloudSkillsSnapshotLikeRust covers the Rust #55631
// `--cloud-skills-snapshot PATH` surface in both the separated and `=` forms.
func TestAppServerParsesCloudSkillsSnapshotLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"app-server", "--cloud-skills-snapshot", "/tmp/skills.json", "--listen", "off"},
		{"app-server", "--cloud-skills-snapshot=/tmp/skills.json", "--listen", "off"},
	} {
		parsed, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v) error = %v", args, err)
		}
		if parsed.Command != CommandAppServer || parsed.AppServer.CloudSkillsSnapshot != "/tmp/skills.json" {
			t.Fatalf("Parse(%v) = %#v", args, parsed.AppServer)
		}
	}
	parsed, err := Parse([]string{"app-server", "--listen", "off"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.AppServer.CloudSkillsSnapshot != "" {
		t.Fatalf("default snapshot = %q, want empty", parsed.AppServer.CloudSkillsSnapshot)
	}
}
