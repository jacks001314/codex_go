package exec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
)

// Mirrors Rust's codex exec wiring: the config's startup warnings reach the exec
// client as warning items.
func TestRunEmitsConfigStartupWarningsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"never\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_approval_policies = [\"on-request\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "done"}
	var stdout, stderr bytes.Buffer
	if _, err := runner.Run(Request{Exec: cli.ExecOptions{Prompt: "check warnings"}}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "Configured value for `approval_policy` is disallowed by requirements") {
		t.Fatalf("stderr = %q, want the startup warning", stderr.String())
	}
}

// Mirrors Rust's load_agent_roles for the exec path: a trusted project's
// declared roles and `<config_folder>/agents` role files reach the multi-agent
// tool catalog.
func TestExecMultiAgentToolsUseLayeredProjectRolesLikeRust(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userConfig := "[projects.\"" + strings.ReplaceAll(repo, `\`, `\\`) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(repo, ".gcode")
	if err := os.MkdirAll(filepath.Join(dotCodex, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotCodex, "agents", "reviewer.toml"),
		[]byte("name = \"reviewer\"\ndescription = \"Review role\"\ndeveloper_instructions = \"Review carefully\"\nmodel = \"gpt-review\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte("[agents.planner]\ndescription = \"Plan work\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := NewLocalRunner(home)
	modelsManager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug: "catalog-v2", DisplayName: "catalog-v2", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "v2",
	}}})
	req := &Request{Exec: cli.ExecOptions{Prompt: "delegate", Shared: cli.SharedOptions{CWD: repo, Model: "catalog-v2"}}}
	cfg := &config.Config{Values: map[string]any{}}
	tools, err := runner.multiAgentToolsForRun(context.Background(), req, cfg, "thread-root", "turn-root", &model.ResponsesAgentRunner{ModelsManager: modelsManager})
	if err != nil {
		t.Fatalf("multiAgentToolsForRun() error = %v", err)
	}
	if tools == nil {
		t.Fatal("multiAgentToolsForRun() = nil, want V2 tools")
	}
	t.Cleanup(func() { closeExecMultiAgentTools(tools) })
	role, ok := tools.roles["reviewer"]
	if !ok || role.Description != "Review role" || role.Settings["model"] != "gpt-review" {
		t.Fatalf("layered roles = %#v, want the discovered reviewer role", tools.roles)
	}
	if planner, ok := tools.roles["planner"]; !ok || planner.Description != "Plan work" {
		t.Fatalf("layered roles = %#v, want the declared planner role", tools.roles)
	}
}
