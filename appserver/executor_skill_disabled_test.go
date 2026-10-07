package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/skillprovider"
	"codex_go/turn"
	"codex_go/utils"
)

// Mirrors Rust #46015: callers may disable specific executor skills per
// environment, and only that environment's catalog drops them.
func TestRuntimeRouterDisabledExecutorSkillPathsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[skills]\ninclude_instructions = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	capabilityRoot := t.TempDir()
	skillDir := filepath.Join(capabilityRoot, "plugin", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	skillPath := filepath.Join(skillDir, SkillFilename)
	if err := os.WriteFile(skillPath, []byte("---\nname: deploy\ndescription: Deploy through the executor.\n---\n\nEXECUTOR_MAIN_MARKER\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	configService := config.NewConfigService(home)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       configService,
		Skills:       NewSkillsServiceWithOptions(&SkillsServiceOptions{Config: configService}),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                     t.TempDir(),
		SelectedCapabilityRoots: []SelectedCapabilityRoot{{ID: "demo-plugin@1", Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "local", Path: capabilityRoot}}},
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	listNames := func() []string {
		t.Helper()
		provider := router.executorSkillProviderForThread(threadID)
		if provider == nil {
			t.Fatal("executor skill provider is nil")
		}
		catalog := provider.ListKind(context.Background(), skillprovider.SourceExecutor, skillprovider.ListQuery{})
		names := make([]string, 0, len(catalog.Entries))
		for _, entry := range catalog.Entries {
			names = append(names, entry.Name)
		}
		return names
	}
	if names := listNames(); len(names) != 1 {
		t.Fatalf("baseline executor skills = %#v, want the discovered skill", names)
	}

	// Disabling the SKILL.md document for this environment hides the skill.
	router.services.DisabledExecutorSkillPaths = map[string][]string{"local": {skillPath}}
	if names := listNames(); len(names) != 0 {
		t.Fatalf("disabled executor skills = %#v, want none", names)
	}

	// Rust compares path URIs, so an equivalent spelling (forward slashes, or a
	// file:// locator) also matches.
	router.services.DisabledExecutorSkillPaths = map[string][]string{"local": {filepath.ToSlash(skillPath)}}
	if names := listNames(); len(names) != 0 {
		t.Fatalf("forward-slash disablement = %#v, want none", names)
	}
	if uri, err := utils.FromHostNativePath(skillPath); err == nil {
		router.services.DisabledExecutorSkillPaths = map[string][]string{"local": {uri.String()}}
		if names := listNames(); len(names) != 0 {
			t.Fatalf("file-locator disablement = %#v, want none", names)
		}
	}

	// A path configured for another environment has no effect.
	router.services.DisabledExecutorSkillPaths = map[string][]string{"other-executor": {skillPath}}
	if names := listNames(); len(names) != 1 {
		t.Fatalf("other-environment disablement affected the local catalog: %#v", names)
	}
}

// TestExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust mirrors the path
// identity rule from Rust #51482 (see
// `collect_explicit_skill_mentions_rejects_disabled_windows_path_identity` and
// `collect_explicit_skill_mentions_skips_when_linked_path_disabled`): a disabled
// path spelling blocks its skill whenever the two resolve to the same path URI,
// across Windows case and separator differences, while POSIX case and literal
// percent/fragment characters stay significant.
func TestExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust(t *testing.T) {
	equal := []struct{ left, right string }{
		{`C:\Skills\Demo\SKILL.md`, `C:\Skills\Demo\SKILL.md`},
		{`C:\Skills\Demo\SKILL.md`, `c:\skills\demo\skill.md`},
		{`C:\Skills\Demo\SKILL.md`, "C:/SKILLS/DEMO/skill.md"},
		{`C:\Skills\Demo\SKILL.md`, "file:///c:/skills/demo/skill.md"},
	}
	for _, tc := range equal {
		if !executorSkillPathEquals(tc.left, tc.right) {
			t.Fatalf("executorSkillPathEquals(%q, %q) = false, want true", tc.left, tc.right)
		}
	}

	distinct := []struct{ left, right string }{
		{"", "/tmp/demo/SKILL.md"},
		{"/tmp/Demo/SKILL.md", "/tmp/demo/SKILL.md"},
		{"file://SERVER/share/File.txt", "file://server/share/file.txt"},
		{`/tmp/demo skill%23/SKILL.md`, `/tmp/demo skill#/SKILL.md`},
		{"file:///C:/Skills/Demo/SKILL.md", "file:///D:/Skills/Demo/SKILL.md"},
	}
	for _, tc := range distinct {
		if executorSkillPathEquals(tc.left, tc.right) {
			t.Fatalf("executorSkillPathEquals(%q, %q) = true, want false", tc.left, tc.right)
		}
	}

	// A host path and its `file:` locator are one identity too.
	host := "/tmp/demo/SKILL.md"
	if uri, err := utils.FromHostNativePath(host); err == nil {
		if !executorSkillPathEquals(host, uri.String()) {
			t.Fatalf("executorSkillPathEquals(%q, %q) = false, want true", host, uri.String())
		}
	} else {
		t.Fatalf("FromHostNativePath(%q) error = %v", host, err)
	}

	if executorSkillPathURI("environment://local/skills/demo") != nil {
		t.Fatal("environment locator must not resolve to a path URI")
	}
}
