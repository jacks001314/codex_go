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
	candidate := DetectImplicitSkillInvocationForCommand(skills, "please build", filepath.Join("repo", "skills", "build"))
	if candidate == nil || candidate.Name != "build" {
		t.Fatalf("DetectImplicitSkillInvocationForCommand() = %#v", candidate)
	}
	candidate = DetectImplicitSkillInvocationForCommand(skills, "please build", filepath.Join("repo", "skills", "build", "..draft"))
	if candidate == nil || candidate.Name != "build" {
		t.Fatalf("DetectImplicitSkillInvocationForCommand(inside ..draft) = %#v", candidate)
	}
	candidate = DetectImplicitSkillInvocationForCommand(skills, "please build", filepath.Join("repo", "skills", "build-other"))
	if candidate != nil {
		t.Fatalf("DetectImplicitSkillInvocationForCommand(outside sibling) = %#v, want nil", candidate)
	}
	disabled := false
	skills[0].AllowImplicitInvocation = &disabled
	candidate = DetectImplicitSkillInvocationForCommand(skills, "please build", filepath.Join("repo", "skills", "build"))
	if candidate != nil {
		t.Fatalf("DetectImplicitSkillInvocationForCommand(disabled) = %#v, want nil", candidate)
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
	if len(selected) != 1 || selected[0].Path != spacePath {
		t.Fatalf("CollectExplicitSkillMentions(encoded local path) = %#v", selected)
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

	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[deploy](skill://" + remotePath + ")"}},
		Skills: []InstructionsSkillMetadata{{Name: "deploy", Path: remotePath}},
	})
	if len(selected) != 1 || selected[0].Path != remotePath {
		t.Fatalf("CollectExplicitSkillMentions(remote URI without dollar) = %#v", selected)
	}

	encodedRemotePath := "environment://remote/workspace/skills/deploy%20app/SKILL.md"
	selected = CollectExplicitSkillMentions(&ExplicitSkillMentionOptions{
		Inputs: []SkillMentionInput{{Type: "text", Text: "[deploy](skill://environment://remote/workspace/skills/deploy app/SKILL.md)"}},
		Skills: []InstructionsSkillMetadata{{Name: "deploy", Path: encodedRemotePath}},
	})
	if len(selected) != 1 || selected[0].Path != encodedRemotePath {
		t.Fatalf("CollectExplicitSkillMentions(remote URI with spaces) = %#v", selected)
	}
}
