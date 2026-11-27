package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

func perEnvironmentTestSelection(t *testing.T, id string, cwd string, profile sandbox.PermissionProfile, roots []string) map[string]any {
	t.Helper()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	return map[string]any{
		"environmentId":  id,
		"cwd":            cwd,
		"workspaceRoots": roots,
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"permission_profile": profileJSON,
		}},
	}
}

// TestPerEnvironmentContextGroupsRulesLikeRust pins Rust #61494: with
// per_environment_context enabled, even a single environment is grouped and
// carries its own network and filesystem rules, while managed network
// requirements are labelled as thread-wide.
func TestPerEnvironmentContextGroupsRulesLikeRust(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if err := manager.SetInfo("env-a", EnvironmentShellInfo{Name: "zsh", Path: "/bin/zsh"}, rootA); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetInfo("env-b", EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, rootB); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()
	workspaceWrite := sandbox.WorkspaceWritePermissionProfile()
	workspaceWrite.NetworkEnabled = true
	params := &turn.TurnStartParams{Environments: []map[string]any{
		perEnvironmentTestSelection(t, "env-a", rootA, sandbox.ReadOnlyPermissionProfile(), []string{rootA}),
		perEnvironmentTestSelection(t, "env-b", rootB, workspaceWrite, []string{rootB}),
	}}
	requirements := &config.NetworkRequirements{Domains: map[string]config.NetworkPermission{
		"blocked.example.com": config.NetworkDeny,
		"api.example.com":     config.NetworkAllow,
	}}
	text := router.turnEnvironmentContextTextWithOptions(context.Background(), params, nil, "UTC", "", false, false, environmentWorldStateRemovals{}, true, false, requirements)

	for _, want := range []string{
		"<environment_context>\n  <environments>\n",
		"<environment id=\"env-a\" primary=\"true\">",
		"<environment id=\"env-b\" primary=\"false\">",
		"<shell>zsh</shell>",
		"<network access=\"restricted\" />",
		"<network access=\"enabled\" />",
		"<filesystem><workspace_roots><root>" + rootA + "</root></workspace_roots>",
		"<filesystem><workspace_roots><root>" + rootB + "</root></workspace_roots>",
		"<permission_profile type=\"managed\">",
		"<network enabled=\"true\"><allowed>api.example.com</allowed><denied>blocked.example.com</denied></network>",
		"<thread_network_requirements>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("environment context missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\n  <cwd>") {
		t.Fatalf("per-environment context rendered a legacy top-level cwd:\n%s", text)
	}
}

// TestPerEnvironmentContextSingleEnvironmentOmitsPrimaryLikeRust pins that the
// primary attribute only appears when more than one environment is selected.
func TestPerEnvironmentContextSingleEnvironmentOmitsPrimaryLikeRust(t *testing.T) {
	cwd := t.TempDir()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if err := manager.SetInfo("env-a", EnvironmentShellInfo{Name: "zsh", Path: "/bin/zsh"}, cwd); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()
	params := &turn.TurnStartParams{Environments: []map[string]any{
		perEnvironmentTestSelection(t, "env-a", cwd, sandbox.FullAccessPermissionProfile(), []string{cwd}),
	}}
	text := router.turnEnvironmentContextTextWithOptions(context.Background(), params, nil, "UTC", "", false, false, environmentWorldStateRemovals{}, true, false, nil)
	if !strings.Contains(text, "<environment id=\"env-a\">") || strings.Contains(text, "primary=") {
		t.Fatalf("single per-environment context = %s", text)
	}
	if !strings.Contains(text, `<network access="enabled" />`) || !strings.Contains(text, `<permission_profile type="disabled"><file_system type="unrestricted" /></permission_profile>`) {
		t.Fatalf("single per-environment rules missing:\n%s", text)
	}
}

func TestRenderFileSystemContextMatchesRustFields(t *testing.T) {
	cwd := t.TempDir()
	profile := sandbox.WorkspaceWritePermissionProfile()
	rendered, err := sandbox.RenderFileSystemContext(&profile, cwd, []string{cwd})
	if err != nil {
		t.Fatalf("RenderFileSystemContext() error = %v", err)
	}
	for _, want := range []string{
		"<filesystem><workspace_roots><root>" + cwd + "</root></workspace_roots>",
		`<permission_profile type="managed">`,
		`<file_system type="restricted">`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("filesystem context missing %q:\n%s", want, rendered)
		}
	}
}

// TestScopedPermissionsEmitPerEnvironmentAndWithdrawStaleScopesLikeRust covers
// the scoped PermissionsSnapshot: each selected environment gets a labelled
// description, an unchanged turn is silent, and a deselected environment's
// description is withdrawn.
func TestScopedPermissionsEmitPerEnvironmentAndWithdrawStaleScopesLikeRust(t *testing.T) {
	router := newExecPolicySavedTestRouter(t)
	const threadID = "thread-scoped-permissions"
	now := time.Now().UTC()
	if err := router.services.ThreadRouter.store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), Extra: map[string]any{}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	rootA := t.TempDir()
	rootB := t.TempDir()
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"per_environment_context": true}}}
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: router.services.DefaultCWD, Environments: []map[string]any{
		perEnvironmentTestSelection(t, "env-a", rootA, sandbox.ReadOnlyPermissionProfile(), []string{rootA}),
		perEnvironmentTestSelection(t, "env-b", rootB, sandbox.WorkspaceWritePermissionProfile(), []string{rootB}),
	}}

	items, err := router.permissionsWorldStateInputItems(threadID, params, nil, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItems() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("initial scoped descriptions = %d, want 2", len(items))
	}
	if text := permissionsInputItemText(t, items[0]); !strings.HasPrefix(text, sandbox.PermissionInstructionsOpenTag+`<environment id="env-a">`) {
		t.Fatalf("first scoped description = %q", text)
	}
	if text := permissionsInputItemText(t, items[1]); !strings.HasPrefix(text, sandbox.PermissionInstructionsOpenTag+`<environment id="env-b">`) {
		t.Fatalf("second scoped description = %q", text)
	}

	repeat, err := router.permissionsWorldStateInputItems(threadID, params, nil, cfg)
	if err != nil {
		t.Fatalf("repeat permissionsWorldStateInputItems() error = %v", err)
	}
	if len(repeat) != 0 {
		t.Fatalf("unchanged scoped descriptions re-emitted: %#v", repeat)
	}

	deselected := *params
	deselected.Environments = params.Environments[:1]
	withdrawals, err := router.permissionsWorldStateInputItems(threadID, &deselected, nil, cfg)
	if err != nil {
		t.Fatalf("withdrawal permissionsWorldStateInputItems() error = %v", err)
	}
	if len(withdrawals) != 1 {
		t.Fatalf("withdrawals = %d, want 1: %#v", len(withdrawals), withdrawals)
	}
	text := permissionsInputItemText(t, withdrawals[0])
	if !strings.Contains(text, `<environment id="env-b">`) || !strings.Contains(text, "no longer current") {
		t.Fatalf("withdrawal text = %q", text)
	}
	if strings.Contains(text, "<environment id=\"env-a\">") {
		t.Fatalf("unchanged environment was restated: %q", text)
	}
}

// TestScopedPermissionsUseThreadDefaultsAndFormatTransitionsLikeRust pins the
// no-ready-environment fallback and the scoped -> unscoped transition.
func TestScopedPermissionsUseThreadDefaultsAndFormatTransitionsLikeRust(t *testing.T) {
	router := newExecPolicySavedTestRouter(t)
	const threadID = "thread-scoped-transitions"
	now := time.Now().UTC()
	if err := router.services.ThreadRouter.store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), Extra: map[string]any{}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	rootA := t.TempDir()
	scopedCfg := &config.Config{Values: map[string]any{"features": map[string]any{"per_environment_context": true}}}
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: router.services.DefaultCWD, Environments: []map[string]any{
		perEnvironmentTestSelection(t, "env-a", rootA, sandbox.ReadOnlyPermissionProfile(), []string{rootA}),
	}}
	if _, err := router.permissionsWorldStateInputItems(threadID, params, nil, scopedCfg); err != nil {
		t.Fatal(err)
	}

	// No selected environment and no configured default falls back to the
	// labelled thread defaults.
	noEnvironment := &turn.TurnStartParams{ThreadID: threadID, CWD: router.services.DefaultCWD}
	defaults, err := router.permissionsWorldStateInputItems(threadID, noEnvironment, nil, scopedCfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 2 {
		t.Fatalf("thread-default transition = %d items, want withdrawal + defaults: %#v", len(defaults), defaults)
	}
	if text := permissionsInputItemText(t, defaults[0]); !strings.Contains(text, `<environment id="env-a">`) || !strings.Contains(text, "no longer current") {
		t.Fatalf("scope withdrawal = %q", text)
	}
	if text := permissionsInputItemText(t, defaults[1]); !strings.HasPrefix(text, sandbox.PermissionInstructionsOpenTag+"<thread_defaults>") {
		t.Fatalf("thread defaults = %q", text)
	}

	// Turning the feature off returns to the unscoped format: withdraw the
	// thread defaults, then restate the unscoped instructions.
	unscopedCfg := &config.Config{Values: map[string]any{}}
	transitions, err := router.permissionsWorldStateInputItems(threadID, noEnvironment, nil, unscopedCfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 2 {
		t.Fatalf("scoped -> unscoped transition = %d items, want 2: %#v", len(transitions), transitions)
	}
	if text := permissionsInputItemText(t, transitions[0]); !strings.Contains(text, "<thread_defaults>") || !strings.Contains(text, "no longer current") {
		t.Fatalf("thread-default withdrawal = %q", text)
	}
	if text := permissionsInputItemText(t, transitions[1]); !strings.HasPrefix(text, sandbox.PermissionInstructionsOpenTag) || strings.Contains(text, "<thread_defaults>") {
		t.Fatalf("unscoped restatement = %q", text)
	}
}
