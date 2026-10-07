package prompt

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"codex_go/utils"
)

func TestInstructionsCandidateFilenames(t *testing.T) {
	got := InstructionsCandidateFilenames([]string{"README.md", "AGENTS.md", ""}, utils.ConventionPosix)
	want := []string{InstructionsLocalAgentsMDFilename, InstructionsDefaultAgentsMDFilename, "README.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InstructionsCandidateFilenames() = %v, want %v", got, want)
	}
}

// TestInstructionsCandidateFilenamesRejectPathSyntaxLikeRust mirrors Rust
// #45865's fallback_paths_are_rejected_before_filesystem_probes: entries
// containing path syntax never reach a filesystem probe, and backslashes/colons
// are rejected only under the Windows executor convention.
func TestInstructionsCandidateFilenamesRejectPathSyntaxLikeRust(t *testing.T) {
	alwaysInvalid := []string{"", ".", "..", "/AGENTS.md", "../AGENTS.md", "nested/AGENTS.md", "//server/share/AGENTS.md", "AGENTS\x00.md"}
	windowsOnly := []string{`..\AGENTS.md`, `nested\AGENTS.md`, `\AGENTS.md`, `C:\AGENTS.md`, "C:AGENTS.md", `\\server\share\AGENTS.md`, `\\?\UNC\server\share\AGENTS.md`, `\\.\pipe\instructions`, "AGENTS.md:stream"}
	base := []string{InstructionsLocalAgentsMDFilename, InstructionsDefaultAgentsMDFilename}

	posix := InstructionsCandidateFilenames(append(append([]string{}, alwaysInvalid...), append(windowsOnly, "WORKFLOW.md", "WORKFLOW.md", ".instructions.md")...), utils.ConventionPosix)
	wantPosix := append(append([]string{}, base...), append(windowsOnly, "WORKFLOW.md", ".instructions.md")...)
	if !reflect.DeepEqual(posix, wantPosix) {
		t.Fatalf("POSIX candidates = %v, want %v", posix, wantPosix)
	}

	windows := InstructionsCandidateFilenames(append(append([]string{}, alwaysInvalid...), append(windowsOnly, "WORKFLOW.md", "WORKFLOW.md", ".instructions.md")...), utils.ConventionWindows)
	wantWindows := append(append([]string{}, base...), "WORKFLOW.md", ".instructions.md")
	if !reflect.DeepEqual(windows, wantWindows) {
		t.Fatalf("Windows candidates = %v, want %v", windows, wantWindows)
	}
	if !reflect.DeepEqual(wantWindows, []string{InstructionsLocalAgentsMDFilename, InstructionsDefaultAgentsMDFilename, "WORKFLOW.md", ".instructions.md"}) {
		t.Fatalf("unexpected Windows expectations: %v", wantWindows)
	}
}

// TestInstructionsPathConventionInfersExecutorConventionLikeRust mirrors Rust's
// PathUri::infer_path_convention usage: a Windows cwd (a Windows path URI or a
// Windows-native path) uses the Windows convention on any host.
func TestInstructionsPathConventionInfersExecutorConventionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		cwd  string
		want utils.PathConvention
	}{
		{"file:///repo", utils.ConventionPosix},
		{"file:///C:/repo", utils.ConventionWindows},
		{`C:\repo`, utils.ConventionWindows},
		{`\\server\share\repo`, utils.ConventionWindows},
	} {
		if got := instructionsPathConvention(testCase.cwd); got != testCase.want {
			t.Fatalf("instructionsPathConvention(%q) = %q, want %q", testCase.cwd, got, testCase.want)
		}
	}
	// A host-native POSIX path uses the host convention.
	if runtime.GOOS != "windows" {
		if got := instructionsPathConvention("/repo"); got != utils.ConventionPosix {
			t.Fatalf("instructionsPathConvention(%q) = %q, want posix", "/repo", got)
		}
	}
}

func TestAgentsMDPathsFromRootToCWD(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll(child) error = %v", err)
	}
	rootDoc := filepath.Join(root, InstructionsDefaultAgentsMDFilename)
	localDoc := filepath.Join(child, InstructionsLocalAgentsMDFilename)
	for _, path := range []string{rootDoc, localDoc} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	got, err := InstructionsAgentsMDPaths(child, nil, nil)
	if err != nil {
		t.Fatalf("InstructionsAgentsMDPaths() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{rootDoc, localDoc}) {
		t.Fatalf("InstructionsAgentsMDPaths() = %v, want root then child", got)
	}
}

func TestLoadProjectConcatenatesAndTruncates(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, InstructionsDefaultAgentsMDFilename), []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	loaded, err := LoadProjectInstructions(InstructionsLoadConfig{
		CWD:              root,
		MaxBytes:         7,
		EnvironmentID:    "local",
		UserInstructions: "user",
	})
	if err != nil {
		t.Fatalf("LoadProjectInstructions() error = %v", err)
	}
	text := loaded.Text()
	if !strings.Contains(text, "user") || !strings.Contains(text, "project") || strings.Contains(text, "instructions") {
		t.Fatalf("Text() = %q", text)
	}
}

func TestLoadProjectInstructionsDeniesUnreadablePathLikeRust(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	doc := filepath.Join(root, InstructionsDefaultAgentsMDFilename)
	if err := os.WriteFile(doc, []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := LoadProjectInstructions(InstructionsLoadConfig{
		CWD:      root,
		MaxBytes: 1 << 20,
		DenyRead: func(path string) bool { return path == doc },
	})
	if err == nil || !strings.Contains(err.Error(), "not readable under the active filesystem permissions") {
		t.Fatalf("LoadProjectInstructions(denied) error = %v", err)
	}
}

func TestLoadProjectInstructionsSkipsUntrustedProjectLikeRust(t *testing.T) {
	// Rust #39837: project-scoped AGENTS.md discovery is skipped for
	// untrusted projects while user-level instructions are preserved.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, InstructionsDefaultAgentsMDFilename), []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	loaded, err := LoadProjectInstructions(InstructionsLoadConfig{
		CWD:              root,
		MaxBytes:         1 << 20,
		UserInstructions: "user instructions",
		UntrustedProject: true,
	})
	if err != nil {
		t.Fatalf("LoadProjectInstructions() error = %v", err)
	}
	if loaded == nil || loaded.Text() != "user instructions" {
		t.Fatalf("loaded = %#v, want only user instructions", loaded)
	}
	for _, entry := range loaded.Entries {
		if entry.Provenance == InstructionsProvenanceProject {
			t.Fatalf("untrusted project leaked project instructions: %#v", entry)
		}
	}
}

func TestInstructionsManagerCacheKeyIncludesTrustLevelLikeRust(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, InstructionsDefaultAgentsMDFilename), []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := NewInstructionsManager("user")
	if err := manager.Refresh(InstructionsLoadConfig{CWD: root, MaxBytes: -1, EnvironmentID: "local"}); err != nil {
		t.Fatalf("Refresh(trusted) error = %v", err)
	}
	trusted := manager.Loaded()
	if trusted == nil || !strings.Contains(trusted.Text(), "project instructions") {
		t.Fatalf("trusted loaded = %#v", trusted)
	}
	if err := manager.Refresh(InstructionsLoadConfig{CWD: root, MaxBytes: -1, EnvironmentID: "local", UntrustedProject: true}); err != nil {
		t.Fatalf("Refresh(untrusted) error = %v", err)
	}
	untrusted := manager.Loaded()
	if untrusted == nil || untrusted.Text() != "user" {
		t.Fatalf("untrusted loaded = %#v, want only user instructions", untrusted)
	}
}

func TestLoadedEnvironmentLabeledText(t *testing.T) {
	loaded := &LoadedInstructions{
		UserInstructions: "user",
		Entries: []InstructionsEntry{
			{Contents: "one", Provenance: InstructionsProvenanceProject, EnvironmentID: "env-1", CWD: "/a"},
			{Contents: "two", Provenance: InstructionsProvenanceProject, EnvironmentID: "env-2", CWD: "/b"},
		},
	}
	text := loaded.Text()
	if !strings.Contains(text, "Environment: env-1") || !strings.Contains(text, "Environment: env-2") {
		t.Fatalf("environment labels missing in:\n%s", text)
	}
}

func TestManagerCachesBySelectionKey(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, InstructionsDefaultAgentsMDFilename), []byte("one"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := NewInstructionsManager("user")
	if err := manager.Refresh(InstructionsLoadConfig{CWD: root, MaxBytes: -1, EnvironmentID: "local"}); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	first := manager.Loaded()
	if first == nil {
		t.Fatalf("Loaded() = nil")
	}
	if err := os.WriteFile(filepath.Join(root, InstructionsDefaultAgentsMDFilename), []byte("two"), 0o600); err != nil {
		t.Fatalf("WriteFile(two) error = %v", err)
	}
	if err := manager.Refresh(InstructionsLoadConfig{CWD: root, MaxBytes: -1, EnvironmentID: "local"}); err != nil {
		t.Fatalf("Refresh(second) error = %v", err)
	}
	second := manager.Loaded()
	if second.Text() != first.Text() {
		t.Fatalf("cached text changed: %q != %q", second.Text(), first.Text())
	}
}

func TestSkillHelpers(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "build", Path: filepath.Join("repo", "skills", "build", "SKILL.md")},
		{Name: "build", Path: filepath.Join("repo", "skills", "build2", "SKILL.md")},
		{Name: "test", Path: filepath.Join("repo", "skills", "test", "SKILL.md")},
	}
	counts := BuildInstructionsSkillNameCounts(skills)
	if counts["build"] != 2 || counts["test"] != 1 {
		t.Fatalf("BuildInstructionsSkillNameCounts() = %v", counts)
	}
}

func TestDetectImplicitSkillInvocationForCommandMatchesRustFixtures(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-test")
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(filepath.Join(scriptsDir, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("skill"), 0o600); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	scriptPath := filepath.Join(scriptsDir, "nested", "fetch_comments.py")
	if err := os.WriteFile(scriptPath, []byte("print(1)"), 0o600); err != nil {
		t.Fatalf("WriteFile(script) error = %v", err)
	}
	disabled := false
	skills := []InstructionsSkillMetadata{{
		Name:                    "test-skill",
		Path:                    skillPath,
		AllowImplicitInvocation: &disabled,
	}}

	for _, test := range []struct {
		name    string
		command string
		workdir string
		want    bool
	}{
		{name: "relative script", command: "python3 -u scripts/nested/fetch_comments.py", workdir: skillDir, want: true},
		{name: "absolute script", command: "python3 " + filepath.ToSlash(scriptPath), workdir: root, want: true},
		{name: "python inline", command: `python3 -c "print(1)"`, workdir: skillDir},
		{name: "absolute doc read", command: "cat " + filepath.ToSlash(skillPath) + " | head", workdir: root, want: true},
		{name: "shared nl parser", command: "nl -ba SKILL.md", workdir: skillDir, want: true},
		{name: "name text is not evidence", command: "echo test-skill", workdir: skillDir},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := DetectImplicitSkillInvocationForCommand(skills, test.command, test.workdir)
			if (candidate != nil) != test.want {
				t.Fatalf("DetectImplicitSkillInvocationForCommand() = %#v, want match %v", candidate, test.want)
			}
		})
	}
}

func TestDetectImplicitSkillInvocationForCommandMatchesPowerShellGetContent(t *testing.T) {
	root := t.TempDir()
	plainSkillDir := filepath.Join(root, "skill-test")
	spacedSkillDir := filepath.Join(root, "skill test")
	if err := os.MkdirAll(plainSkillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(spacedSkillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	plainSkillPath := filepath.Join(plainSkillDir, "SKILL.md")
	spacedSkillPath := filepath.Join(spacedSkillDir, "SKILL.md")
	if err := os.WriteFile(plainSkillPath, []byte("skill"), 0o600); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	if err := os.WriteFile(spacedSkillPath, []byte("skill"), 0o600); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	skills := []InstructionsSkillMetadata{
		{Name: "test-skill", Path: plainSkillPath},
		{Name: "spaced-skill", Path: spacedSkillPath},
	}
	plainPath := filepath.ToSlash(plainSkillPath)
	quotedSpacedPath := `"` + filepath.ToSlash(spacedSkillPath) + `"`
	for _, test := range []struct {
		command string
		want    string
	}{
		{command: "Get-Content " + plainPath, want: "test-skill"},
		{command: "Get-Content -Raw " + plainPath, want: "test-skill"},
		{command: "get-content   -raw '" + plainPath + "'", want: "test-skill"},
		{command: "Get-Content -Path " + plainPath, want: "test-skill"},
		{command: "Get-Content -LiteralPath " + plainPath, want: "test-skill"},
		{command: "Get-Content " + plainPath + " -Raw", want: "test-skill"},
		{command: "gc " + plainPath, want: "test-skill"},
		{command: "type " + plainPath, want: "test-skill"},
		{command: "Get-Content " + quotedSpacedPath, want: "spaced-skill"},
		{command: "Get-Content -Raw " + quotedSpacedPath, want: "spaced-skill"},
	} {
		got := DetectImplicitSkillInvocationForCommand(skills, test.command, root)
		if got == nil || got.Name != test.want {
			t.Fatalf("DetectImplicitSkillInvocationForCommand(%q) = %#v, want %s", test.command, got, test.want)
		}
	}
	if got := powershellGetContentSkillPath(`Get-Content C:\skills\example\SKILL.md`); got != `C:\skills\example\SKILL.md` {
		t.Fatalf("powershellGetContentSkillPath(Windows) = %q", got)
	}
}

func TestCollectExplicitSkillMentions(t *testing.T) {
	root := t.TempDir()
	buildPath := filepath.Join(root, "skills", "build", "SKILL.md")
	testPath := filepath.Join(root, "skills", "test", "SKILL.md")
	duplicatePath := filepath.Join(root, "skills", "build2", "SKILL.md")
	skills := []InstructionsSkillMetadata{
		{Name: "build", Path: buildPath},
		{Name: "test", Path: testPath},
		{Name: "build", Path: duplicatePath},
	}
	selected := CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{
			{Type: "skill", Name: "build", Path: buildPath},
			{Type: "text", Text: "run $test and maybe $build"},
		},
		Skills: skills,
	})
	if len(selected) != 2 || selected[0].Path != buildPath || selected[1].Path != testPath {
		t.Fatalf("CollectExplicitSkillMentions() = %#v", selected)
	}

	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[$build](skill://" + duplicatePath + ") $PATH"}},
		Skills: skills,
	})
	if len(selected) != 1 || selected[0].Path != duplicatePath {
		t.Fatalf("CollectExplicitSkillMentions(linked) = %#v", selected)
	}

	encodedPath := strings.ReplaceAll(testPath, "test", "test%20space")
	spacePath := strings.ReplaceAll(testPath, "test", "test space")
	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[$test](skill://" + encodedPath + ")"}},
		Skills: []InstructionsSkillMetadata{{Name: "test", Path: spacePath}},
	})
	if len(selected) != 0 {
		t.Fatalf("CollectExplicitSkillMentions(encoded local path) = %#v, want none", selected)
	}

	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs:              []SkillMentionInput{{Type: "text", Text: "$test"}},
		Skills:              skills,
		ConnectorSlugCounts: map[string]int{"test": 1},
	})
	if len(selected) != 0 {
		t.Fatalf("CollectExplicitSkillMentions(connector conflict) = %#v, want none", selected)
	}

	remotePath := "environment://remote/workspace/skills/deploy/SKILL.md"
	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[$deploy](skill://" + remotePath + ")"}},
		Skills: []InstructionsSkillMetadata{{Name: "deploy", Path: remotePath}},
	})
	if len(selected) != 1 || selected[0].Path != remotePath {
		t.Fatalf("CollectExplicitSkillMentions(remote URI) = %#v", selected)
	}

	remoteLocatorPath := "skill://remote-root/workspace/skills/deploy/SKILL.md"
	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[$deploy](" + remoteLocatorPath + ") and $deploy"}},
		Skills: []InstructionsSkillMetadata{{Name: "deploy", Path: remotePath, LocatorPath: remoteLocatorPath}},
	})
	if len(selected) != 1 || selected[0].Path != remotePath {
		t.Fatalf("CollectExplicitSkillMentions(remote locator path) = %#v", selected)
	}

	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[deploy](skill://" + remotePath + ")"}},
		Skills: []InstructionsSkillMetadata{{Name: "deploy", Path: remotePath}},
	})
	if len(selected) != 0 {
		t.Fatalf("CollectExplicitSkillMentions(remote URI without dollar) = %#v, want none", selected)
	}

	encodedRemotePath := "environment://remote/workspace/skills/deploy%20app/SKILL.md"
	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[deploy](skill://environment://remote/workspace/skills/deploy app/SKILL.md)"}},
		Skills: []InstructionsSkillMetadata{{Name: "deploy", Path: encodedRemotePath}},
	})
	if len(selected) != 0 {
		t.Fatalf("CollectExplicitSkillMentions(remote URI with spaces) = %#v, want none", selected)
	}
}

// TestCollectExplicitSkillMentionsMatchesWindowsPathIdentityLikeRust mirrors
// Rust #51482 `collect_explicit_skill_mentions_matches_windows_path_identity`:
// a linked mention resolves the same skill across Windows case and separator
// spellings, including the discovery path the catalog exposes as its locator.
func TestCollectExplicitSkillMentionsMatchesWindowsPathIdentityLikeRust(t *testing.T) {
	skill := InstructionsSkillMetadata{
		Name:        "demo-skill",
		Path:        "file:///C:/Skills/Demo/SKILL.md",
		LocatorPath: "file:///C:/Project/.agents/skills/Demo/SKILL.md",
	}
	for _, path := range []string{
		`c:\skills\demo\skill.md`,
		"C:/SKILLS/DEMO/skill.md",
		"skill://c:/project/.agents/skills/demo/skill.md",
	} {
		selected := CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
			Inputs: []SkillMentionInput{{Type: "text", Text: "use [$demo-skill](" + path + ")"}},
			Skills: []InstructionsSkillMetadata{skill},
		})
		if len(selected) != 1 || selected[0].Name != "demo-skill" {
			t.Fatalf("CollectExplicitSkillMentions(%q) = %#v, want the demo skill", path, selected)
		}
	}
}

// TestCollectExplicitSkillMentionsKeepsNativePathCharactersLikeRust mirrors
// Rust #51482 `collect_explicit_skill_mentions_preserves_native_uri_characters`:
// literal spaces, percent escapes, and fragments in native filenames stay
// distinct instead of being folded together.
func TestCollectExplicitSkillMentionsKeepsNativePathCharactersLikeRust(t *testing.T) {
	percent := InstructionsSkillMetadata{Name: "demo-skill", Path: "/tmp/demo skill%23/SKILL.md"}
	fragment := InstructionsSkillMetadata{Name: "demo-skill", Path: "/tmp/demo skill#/SKILL.md"}
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/tmp/demo skill%23/SKILL.md", want: percent.Path},
		{path: "/tmp/demo skill#/SKILL.md", want: fragment.Path},
	} {
		selected := CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
			Inputs: []SkillMentionInput{{Type: "text", Text: "[$demo-skill](" + tc.path + ")"}},
			Skills: []InstructionsSkillMetadata{percent, fragment},
		})
		if len(selected) != 1 || selected[0].Path != tc.want {
			t.Fatalf("CollectExplicitSkillMentions(%q) = %#v, want path %q", tc.path, selected, tc.want)
		}
	}
}

// TestCollectExplicitSkillMentionsResolvesStructuredPathsLikeRust covers the
// Rust #37177 rule in `codex-rs/skills/src/selection.rs`: a structured skill
// mention (`UserInput::Skill`) runs its path through
// `AbsolutePathBuf::relative_to_current_dir` before the identity comparison, so
// relative and `~`-prefixed spellings select the same skill as the loaded
// absolute path.
func TestCollectExplicitSkillMentionsResolvesStructuredPathsLikeRust(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	t.Setenv("HOME", directory)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	skill := InstructionsSkillMetadata{Name: "demo-skill", Path: filepath.Join(cwd, ".agents", "skills", "demo", "SKILL.md")}
	for _, mention := range []string{
		filepath.Join(".agents", "skills", "demo", "SKILL.md"),
		filepath.Join(".", ".agents", "skills", "demo", "SKILL.md"),
		"~/.agents/skills/demo/SKILL.md",
	} {
		selected := CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
			Inputs: []SkillMentionInput{{Type: "skill", Name: "demo-skill", Path: mention}},
			Skills: []InstructionsSkillMetadata{skill},
		})
		if len(selected) != 1 || selected[0].Path != skill.Path {
			t.Fatalf("CollectExplicitSkillMentions(%q) = %#v, want the loaded skill", mention, selected)
		}
	}
}

// TestCollectExplicitSkillMentionsKeepsLocatorSpellingsLikeRust guards the other
// half of the same rule: a locator that carries a URI scheme (the
// `environment://…` spellings the executor catalog exposes) is not resolved
// against the working directory, so those spellings keep matching exactly.
func TestCollectExplicitSkillMentionsKeepsLocatorSpellingsLikeRust(t *testing.T) {
	t.Chdir(t.TempDir())
	skill := InstructionsSkillMetadata{Name: "demo-skill", Path: "environment://remote/demo/SKILL.md"}
	selected := CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "skill", Name: "demo-skill", Path: "environment://remote/demo/SKILL.md"}},
		Skills: []InstructionsSkillMetadata{skill},
	})
	if len(selected) != 1 || selected[0].Path != skill.Path {
		t.Fatalf("CollectExplicitSkillMentions = %#v, want the locator skill", selected)
	}
}

// TestCollectExplicitSkillMentionsKeepsPosixCaseSensitivityLikeRust pins the
// other half of the identity rule: POSIX paths stay case-sensitive, so a
// differently cased linked mention does not select the skill.
func TestCollectExplicitSkillMentionsKeepsPosixCaseSensitivityLikeRust(t *testing.T) {
	skill := InstructionsSkillMetadata{Name: "demo-skill", Path: "/tmp/Demo/SKILL.md"}
	for _, path := range []string{"/tmp/demo/SKILL.md", "/tmp/DEMO/skill.md"} {
		selected := CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
			Inputs: []SkillMentionInput{{Type: "text", Text: "[$demo-skill](" + path + ")"}},
			Skills: []InstructionsSkillMetadata{skill},
		})
		if len(selected) != 0 {
			t.Fatalf("CollectExplicitSkillMentions(%q) = %#v, want none", path, selected)
		}
	}
}

// TestDetectImplicitSkillInvocationForCommandMatchesPathIdentityLikeRust mirrors
// Rust #51482's implicit-skill maps (`SkillLoadOutcome::implicit_skills_by_doc_path`
// / `implicit_skills_by_scripts_dir` in `ext/skills/src/host_outcome.rs`, consulted
// through `implicit_skill_for_doc_path` / `implicit_skill_for_scripts_dir`): they are
// `HashMap<PathUri, SkillMetadata>` keyed by `PathUri::from_abs_path` over the
// host-resolvable spelling (`to_abs_path()` gates out paths with no host-absolute
// form), host paths resolve through symlinks first, and equality is path identity
// rather than raw text.
func TestDetectImplicitSkillInvocationForCommandMatchesPathIdentityLikeRust(t *testing.T) {
	root := t.TempDir()
	percentDir := filepath.Join(root, "demo%23skill")
	fragmentDir := filepath.Join(root, "demo#skill")
	for _, dir := range []string{percentDir, fragmentDir} {
		if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0o600); err != nil {
			t.Fatalf("WriteFile(SKILL.md) error = %v", err)
		}
	}
	skills := []InstructionsSkillMetadata{
		{Name: "percent-skill", Path: filepath.Join(percentDir, "SKILL.md")},
		{Name: "fragment-skill", Path: filepath.Join(fragmentDir, "SKILL.md")},
	}

	// Literal `%23` and `#` in native filenames stay distinct documents.
	for _, tc := range []struct{ command, want string }{
		{command: "cat " + filepath.Join(percentDir, "SKILL.md"), want: "percent-skill"},
		{command: "cat " + filepath.Join(fragmentDir, "SKILL.md"), want: "fragment-skill"},
		// A dot-segment spelling of a host path resolves the same document.
		{command: "cat " + filepath.Join(root, ".", "demo%23skill", "SKILL.md"), want: "percent-skill"},
	} {
		got := DetectImplicitSkillInvocationForCommand(skills, tc.command, root)
		if got == nil || got.Name != tc.want {
			t.Fatalf("DetectImplicitSkillInvocationForCommand(%q) = %#v, want %s", tc.command, got, tc.want)
		}
	}

	// A symlinked spelling of the host path resolves the same document and its
	// scripts directory.
	linkDir := filepath.Join(root, "linked-skill")
	if err := os.Symlink(percentDir, linkDir); err != nil {
		t.Skipf("Symlink unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(percentDir, "scripts", "run.py"), []byte("print(1)"), 0o600); err != nil {
		t.Fatalf("WriteFile(script) error = %v", err)
	}
	for _, tc := range []struct{ name, command string }{
		{name: "symlinked document", command: "cat " + filepath.Join(linkDir, "SKILL.md")},
		{name: "symlinked scripts dir", command: "python3 " + filepath.Join(linkDir, "scripts", "run.py")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectImplicitSkillInvocationForCommand(skills, tc.command, root)
			if got == nil || got.Name != "percent-skill" {
				t.Fatalf("DetectImplicitSkillInvocationForCommand(%q) = %#v, want percent-skill", tc.command, got)
			}
		})
	}

	// POSIX paths stay case-sensitive.
	cased := []InstructionsSkillMetadata{{Name: "demo-skill", Path: filepath.Join(root, "Demo", "SKILL.md")}}
	if got := DetectImplicitSkillInvocationForCommand(cased, "cat "+filepath.Join(root, "demo", "SKILL.md"), root); got != nil {
		t.Fatalf("case-insensitive POSIX match = %#v, want none", got)
	}

	// A foreign spelling has no host-absolute form on a POSIX host, so Rust's
	// `to_abs_path()` gate keeps it out of the implicit maps entirely.
	if runtime.GOOS != "windows" {
		foreign := []InstructionsSkillMetadata{{Name: "windows-skill", Path: `C:\Skills\Demo\SKILL.md`}}
		if got := DetectImplicitSkillInvocationForCommand(foreign, `cat C:\Skills\Demo\SKILL.md`, `C:\Project`); got != nil {
			t.Fatalf("foreign spelling matched on a %s host: %#v", runtime.GOOS, got)
		}
	}
}
