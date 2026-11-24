package systemskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenAIDocsSkillIsNarrowedLikeRust pins Rust #59944: the bundled
// openai-docs sample focuses on Codex/ChatGPT Work behavior and first-time API
// setup, replaces the routing workflow with changelog and quickstart guidance,
// and reports the narrowed display metadata.
func TestOpenAIDocsSkillIsNarrowedLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := Install(home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	root := filepath.Join(CacheRoot(home), "openai-docs")
	skill, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		t.Fatalf("ReadFile(SKILL.md) error = %v", err)
	}
	text := string(skill)
	for _, want := range []string{
		"description: API first-timers, Codex/Work Q&A.",
		"If the user already has an OpenAI API integration, skip this skill",
		"Skip action requests, such as scheduling an automation, and tasks fully answered by supplied text or data.",
		"# Codex and ChatGPT Work",
		"developers.openai.com/codex",
		"learn.chatgpt.com/docs/changelog",
		"developers.openai.com/api/docs/quickstart",
		"migrations when the user explicitly invokes `$openai-docs`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("openai-docs SKILL.md is missing %q", want)
		}
	}
	for _, stale := range []string{
		"Use for Codex models/pricing, scheduled tasks, skills, settings, setup, troubleshooting",
		"**First substantive action:**",
		"## Choose one primary route",
		"## Source and execution boundaries",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("openai-docs SKILL.md still carries %q", stale)
		}
	}

	metadata, err := os.ReadFile(filepath.Join(root, "agents", "openai.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(openai.yaml) error = %v", err)
	}
	yaml := string(metadata)
	for _, want := range []string{
		`short_description: "Codex and ChatGPT Work Q&A, plus first-time API setup"`,
		`default_prompt: "Use $openai-docs to explain a Codex or ChatGPT Work feature, help with my first OpenAI API integration, or migrate a request to my chosen model."`,
	} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("openai-docs metadata is missing %q", want)
		}
	}
}
