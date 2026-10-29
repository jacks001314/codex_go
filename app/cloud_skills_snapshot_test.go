package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/cli"
)

// TestAppServerCloudSkillsSnapshotFailsStartupLikeRust covers Rust #55631: a
// missing or invalid snapshot is a startup error, while a valid file installs
// the native cloud provider.
func TestAppServerCloudSkillsSnapshotFailsStartupLikeRust(t *testing.T) {
	dir := t.TempDir()
	if _, err := appServerRuntimeOptionsFromCLI(cli.AppServerOptions{
		CloudSkillsSnapshot: filepath.Join(dir, "missing.json"),
	}, nil); err == nil {
		t.Fatal("missing snapshot accepted, want a startup error")
	}
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := appServerRuntimeOptionsFromCLI(cli.AppServerOptions{CloudSkillsSnapshot: invalid}, nil); err == nil {
		t.Fatal("invalid snapshot accepted, want a startup error")
	}
	valid := filepath.Join(dir, "skills.json")
	snapshot := `{"skills":[{"package":"skill://demo/work","name":"demo:work",` +
		`"description":"Do the work.","alias_root":"skill://demo",` +
		`"main_resource":"skill://demo/work/SKILL.md",` +
		`"resources":{"skill://demo/work/SKILL.md":"# Work\n"}}]}`
	if err := os.WriteFile(valid, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	options, err := appServerRuntimeOptionsFromCLI(cli.AppServerOptions{CloudSkillsSnapshot: valid}, nil)
	if err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if options.CloudSkillProvider == nil {
		t.Fatal("valid snapshot did not install the cloud skill provider")
	}
}
