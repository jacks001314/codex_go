package prompt

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstructionsCandidateFilenames(t *testing.T) {
	got := InstructionsCandidateFilenames([]string{"README.md", "AGENTS.md", ""})
	want := []string{InstructionsLocalAgentsMDFilename, InstructionsDefaultAgentsMDFilename, "README.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InstructionsCandidateFilenames() = %v, want %v", got, want)
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
