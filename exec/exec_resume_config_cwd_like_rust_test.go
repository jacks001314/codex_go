package exec

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/cli"
	"codex_go/session"
)

// writeExecProject prepares dir as a trusted project whose config layer sets a
// distinctive model.
func writeExecProject(t *testing.T, home, dir, model string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir project marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write project marker: %v", err)
	}
	configDir := filepath.Join(dir, ".gcode")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("model = \""+model+"\"\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	trust := "[projects.\"" + filepath.ToSlash(dir) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(trust), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}
}

// Rust #62194 applies resume settings ahead of config assembly: the resumed
// conversation's saved working directory seeds the config, so cwd-derived
// project defaults come from the resumed workspace.
func TestExecResumeLoadsConfigFromSavedWorkingDirectoryLikeRust(t *testing.T) {
	home := t.TempDir()
	savedDir := t.TempDir()
	writeExecProject(t, home, savedDir, "saved-workspace-model")

	store := session.NewStore(filepath.Join(home, "sessions"))
	record := &session.Record{
		ID:       session.ThreadID("thread-resume"),
		Metadata: session.Metadata{CWD: savedDir},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("save record: %v", err)
	}

	runner := NewRunner(home)
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume:     cli.ExecResumeOptions{SessionID: "thread-resume"},
	}}
	resumeRecord, err := runner.execResumeRecordForRun(req)
	if err != nil || resumeRecord == nil {
		t.Fatalf("resume record = %#v, err = %v", resumeRecord, err)
	}
	runner.applyExecResumeWorkingDirectory(req, resumeRecord)
	if got := requestCWD(req); got != savedDir {
		t.Fatalf("request cwd = %q, want %q", got, savedDir)
	}
	cfg, err := runner.loadExecRunConfig(req, false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := effectiveModel(req, cfg); got != "saved-workspace-model" {
		t.Fatalf("effective model = %q, want the resumed project default", got)
	}

	// A run that keeps another directory never sees the resumed project layer,
	// which is the ordering this resume path fixes.
	otherDir := t.TempDir()
	other := &Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume:     cli.ExecResumeOptions{SessionID: "thread-resume"},
	}}
	other.Exec.Shared.CWD = otherDir
	plainCfg, err := runner.loadExecRunConfig(other, false)
	if err != nil {
		t.Fatalf("load config for explicit cwd: %v", err)
	}
	if got := effectiveModel(other, plainCfg); got == "saved-workspace-model" {
		t.Fatalf("config from %q picked up the resumed project layer", otherDir)
	}
}

// An explicit -C keeps the caller's directory, mirroring an explicit override
// in Rust.
func TestExecResumeKeepsExplicitWorkingDirectoryLikeRust(t *testing.T) {
	home := t.TempDir()
	explicitDir := t.TempDir()
	record := &session.Record{
		ID:       session.ThreadID("thread-resume"),
		Metadata: session.Metadata{CWD: t.TempDir()},
	}
	runner := NewRunner(home)
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume:     cli.ExecResumeOptions{SessionID: "thread-resume"},
	}}
	req.Exec.Shared.CWD = explicitDir
	runner.applyExecResumeWorkingDirectory(req, record)
	if got := requestCWD(req); got != explicitDir {
		t.Fatalf("explicit cwd = %q, want %q", got, explicitDir)
	}
}
