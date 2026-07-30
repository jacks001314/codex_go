package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/execserver"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
	"codex_go/utils"
)

type recordingEnvironmentFSCaller struct {
	params []map[string]any
}

func (c *recordingEnvironmentFSCaller) Call(_ context.Context, _ int, _ string, params any) (json.RawMessage, error) {
	encoded, _ := json.Marshal(params)
	var values map[string]any
	_ = json.Unmarshal(encoded, &values)
	c.params = append(c.params, values)
	return json.RawMessage(`{}`), nil
}

func TestSandboxedEnvironmentFSCallerInjectsContextOnEveryCall(t *testing.T) {
	inner := &recordingEnvironmentFSCaller{}
	sandboxContext := &execserver.FileSystemSandboxContext{
		Permissions:         json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]}}`),
		CWD:                 "file:///workspace",
		WindowsSandboxLevel: string(sandbox.WindowsSandboxDefault),
	}
	caller := sandboxedEnvironmentFSCaller{inner: inner, sandbox: sandboxContext}
	for id, method := range []string{execserver.MethodFSWalk, execserver.MethodFSGetMetadata, execserver.MethodFSReadFile} {
		if _, err := caller.Call(context.Background(), id+1, method, map[string]any{"path": "file:///workspace/skill"}); err != nil {
			t.Fatalf("Call(%s) error = %v", method, err)
		}
	}
	if len(inner.params) != 3 {
		t.Fatalf("calls = %d, want 3", len(inner.params))
	}
	for i, params := range inner.params {
		contextValue, ok := params["sandbox"].(map[string]any)
		if !ok || contextValue["cwd"] != sandboxContext.CWD {
			t.Fatalf("call %d sandbox = %#v", i, params["sandbox"])
		}
	}
}

func TestExecutorSkillSandboxContextsPreserveRestrictedProfileAndWorkspaceRoots(t *testing.T) {
	cwd := t.TempDir()
	denied := filepath.Join(cwd, "private", "**")
	cfg := &config.Config{Values: map[string]any{
		"default_permissions": "restricted",
		"permissions": map[string]any{
			"restricted": map[string]any{
				"filesystem": map[string]any{
					":minimal": "read",
					denied:     "deny",
				},
			},
		},
	}}
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: cwd})
	contexts, err := router.executorSkillSandboxContextsForTurn(cfg, cwd, &turn.TurnStartParams{RuntimeWorkspaceRoots: []string{cwd}})
	if err != nil {
		t.Fatalf("executorSkillSandboxContextsForTurn() error = %v", err)
	}
	contextValue := contexts["local"]
	if contextValue == nil {
		t.Fatal("local sandbox context is missing")
	}
	wantCWD, _ := utils.FromHostNativePath(cwd)
	if contextValue.CWD != wantCWD.String() || len(contextValue.WorkspaceRoots) != 1 || contextValue.WorkspaceRoots[0] != wantCWD.String() {
		t.Fatalf("sandbox paths = cwd %q roots %#v, want %q", contextValue.CWD, contextValue.WorkspaceRoots, wantCWD.String())
	}
	permissionText := string(contextValue.Permissions)
	if !strings.Contains(permissionText, `"kind":"minimal"`) || !strings.Contains(permissionText, `"access":"deny"`) {
		t.Fatalf("restricted permissions were not preserved: %s", permissionText)
	}

	fullRead, err := router.executorSkillSandboxContextsForTurn(&config.Config{Values: map[string]any{"sandbox_mode": "workspace-write"}}, cwd, &turn.TurnStartParams{})
	if err != nil || fullRead != nil {
		t.Fatalf("full-read workspace context = %#v, error = %v", fullRead, err)
	}
}

func TestSelectedExecutorSkillRootWithoutMatchingSandboxContextFailsClosed(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "secret")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: secret\ndescription: must remain hidden\n---\nSECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(),
		SelectedCapabilityRoots: []SelectedCapabilityRoot{{
			ID:       "restricted-root",
			Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "local", Path: root},
		}},
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	entries, warnings, err := router.selectedCapabilitySkillEntriesForRuntimeWithSandbox(context.Background(), threadID, map[string]*execserver.FileSystemSandboxContext{})
	if err != nil {
		t.Fatalf("selectedCapabilitySkillEntriesForRuntimeWithSandbox() error = %v", err)
	}
	if len(entries) != 0 || len(warnings) == 0 || !strings.Contains(warnings[0], "sandbox context is missing") {
		t.Fatalf("entries = %#v warnings = %#v", entries, warnings)
	}
}

func TestExecutorWindowsSkillReadFailsClosedWhenSandboxDisabled(t *testing.T) {
	contextValue := &execserver.FileSystemSandboxContext{WindowsSandboxLevel: string(sandbox.WindowsSandboxDisabled)}
	err := validateExecutorSkillSandboxAvailability(contextValue, `file:///C:/skills/demo/SKILL.md`)
	if err == nil || !strings.Contains(err.Error(), "unavailable filesystem sandbox") {
		t.Fatalf("error = %v", err)
	}
}
