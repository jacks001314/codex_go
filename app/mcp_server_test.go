package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/cli"
	codexexec "codex_go/exec"
	"codex_go/mcp"
)

func TestCodexToolExecRequestMapsConfig(t *testing.T) {
	model := "gpt-test"
	cwd := "."
	sandbox := "workspace-write"
	approval := "on-request"
	baseInstructions := "base"
	developerInstructions := "developer"
	compactPrompt := "compact"
	request, err := codexToolExecRequest(&mcp.CodexToolCall{
		Prompt:                "hello",
		Model:                 &model,
		CWD:                   &cwd,
		Sandbox:               &sandbox,
		ApprovalPolicy:        &approval,
		BaseInstructions:      &baseInstructions,
		DeveloperInstructions: &developerInstructions,
		CompactPrompt:         &compactPrompt,
		Config: map[string]any{
			"model_provider": "openai",
			"features": map[string]any{
				"current_time_reminder": true,
			},
			"model_providers": map[string]any{
				"custom": map[string]any{
					"base_url": "https://example.test/v1",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("codexToolExecRequest() error = %v", err)
	}
	if request.Exec.Prompt != "hello" || request.Exec.Shared.Model != "gpt-test" || request.Exec.Shared.CWD != "." || request.Exec.Shared.Sandbox != "workspace-write" {
		t.Fatalf("request exec options = %#v", request.Exec)
	}
	want := []string{
		"approval_policy=\"on-request\"",
		"compact_prompt=\"compact\"",
		"developer_instructions=\"developer\"",
		"features.current_time_reminder=true",
		"instructions=\"base\"",
		"model_provider=\"openai\"",
		"model_providers.custom.base_url=\"https://example.test/v1\"",
	}
	if !reflect.DeepEqual(request.Exec.ConfigOverrides, want) {
		t.Fatalf("overrides = %#v, want %#v", request.Exec.ConfigOverrides, want)
	}
}

func TestCodexMCPRunnerAppliesRootOptionsToExecRequests(t *testing.T) {
	root := cli.RootOptions{
		ConfigOverrides: []string{`model="gpt-root"`},
		EnableFeatures:  []string{"unified_exec"},
		DisableFeatures: []string{"shell_tool"},
		StrictConfig:    true,
		Shared: cli.SharedOptions{
			CWD:    "root-cwd",
			Images: []string{"root.png"},
		},
	}
	runner := newCodexMCPRunner("codex-home", root)
	root.ConfigOverrides[0] = `model="mutated"`
	root.EnableFeatures[0] = "mutated"
	root.Shared.Images[0] = "mutated.png"

	request := &codexexec.Request{Exec: cli.ExecOptions{Prompt: "hello"}}
	runner.applyRootToRequest(request)

	if request.Root.ConfigOverrides[0] != `model="gpt-root"` ||
		request.Root.EnableFeatures[0] != "unified_exec" ||
		request.Root.DisableFeatures[0] != "shell_tool" ||
		!request.Root.StrictConfig ||
		request.Root.Shared.CWD != "root-cwd" ||
		request.Root.Shared.Images[0] != "root.png" {
		t.Fatalf("request root = %#v", request.Root)
	}

	request.Root.ConfigOverrides[0] = `model="changed"`
	if got := runner.rootOptions().ConfigOverrides[0]; got != `model="gpt-root"` {
		t.Fatalf("runner root leaked request mutation: %q", got)
	}
}

func TestCodexMCPRunnerInjectsHostSkillCatalogAndExplicitBody(t *testing.T) {
	home := t.TempDir()
	skillDir := filepath.Join(home, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n# Demo\n\nUse this skill.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newCodexMCPRunner(home, cli.RootOptions{})
	request := &codexexec.Request{Exec: cli.ExecOptions{Prompt: "$demo handle this", Shared: cli.SharedOptions{CWD: t.TempDir()}}}
	warnings, err := runner.applySkillsToRequest(request)
	if err != nil {
		t.Fatalf("applySkillsToRequest() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("applySkillsToRequest() warnings = %#v", warnings)
	}
	if !strings.Contains(request.AdditionalInstructions, "<skills_instructions>") || !strings.Contains(request.AdditionalInstructions, "- demo: Demo skill.") {
		t.Fatalf("skill catalog = %q", request.AdditionalInstructions)
	}
	encoded, err := json.Marshal(request.AdditionalInputItems)
	if err != nil {
		t.Fatal(err)
	}
	visible := fmt.Sprint(request.AdditionalInputItems)
	if !strings.Contains(visible, "<skill>") || !strings.Contains(visible, "Use this skill.") {
		t.Fatalf("explicit skill input = %s", encoded)
	}
}
