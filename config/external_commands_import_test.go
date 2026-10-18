package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExternalCommandsMigrationImportsCommandsWithoutFrontmatter(t *testing.T) {
	for _, scope := range []string{"home", "repo"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			codexHome := filepath.Join(root, "codex-home")
			externalHome := filepath.Join(root, ".claude")
			repo := filepath.Join(root, "repo")
			sourceCommands := filepath.Join(externalHome, "commands")
			targetSkills := filepath.Join(root, ".agents", "skills")
			params := &ExternalAgentConfigDetectParams{IncludeHome: true}
			if scope == "repo" {
				if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
				sourceCommands = filepath.Join(repo, ".claude", "commands")
				targetSkills = filepath.Join(repo, ".agents", "skills")
				params = &ExternalAgentConfigDetectParams{CWDs: []string{repo}}
			}
			if err := os.MkdirAll(filepath.Join(sourceCommands, "pr"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceCommands, "pr", "review.md"), []byte("Review changes with Claude Code and CLAUDE.md.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceCommands, "deploy.md"), []byte("Deploy $ARGUMENTS.\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			service := NewConfigService(codexHome)
			service.SetExternalAgentHome(externalHome)
			detected := service.DetectExternalAgentConfig(params)
			if len(detected.Items) != 1 {
				t.Fatalf("detected items = %#v", detected.Items)
			}
			item := detected.Items[0]
			if item.ItemType != MigrationCommands || item.Details == nil || len(item.Details.Commands) != 1 || item.Details.Commands[0].Name != "source-command-pr-review" {
				t.Fatalf("detected command migration = %#v", item)
			}

			_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
			if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 1 || len(completed.ItemTypeResults[0].Failures) != 0 {
				t.Fatalf("completed = %#v", completed)
			}
			path := filepath.Join(targetSkills, "source-command-pr-review", "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "---\nname: \"source-command-pr-review\"\ndescription: \"Migrated source command `pr-review`\"\n---\n\n# source-command-pr-review\n\nUse this skill when the user asks to run the migrated source command `pr-review`.\n\n## Command Template\n\nReview changes with Codex and AGENTS.md.\n"
			if got := string(data); got != want {
				t.Fatalf("imported command = %q, want %q", got, want)
			}
			if pathExists(filepath.Join(targetSkills, "source-command-deploy")) {
				t.Fatal("unsupported command template was imported")
			}
			if detectedAgain := service.DetectExternalAgentConfig(params); len(detectedAgain.Items) != 0 {
				t.Fatalf("imported command detected again = %#v", detectedAgain.Items)
			}
		})
	}
}

func TestExternalCommandsMigrationResolvesNormalizedCollisions(t *testing.T) {
	for _, scope := range []string{"home", "repo"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			codexHome := filepath.Join(root, "codex-home")
			externalHome := filepath.Join(root, ".claude")
			repo := filepath.Join(root, "repo")
			sourceCommands := filepath.Join(externalHome, "commands")
			targetSkills := filepath.Join(root, ".agents", "skills")
			params := &ExternalAgentConfigDetectParams{IncludeHome: true}
			if scope == "repo" {
				if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
				sourceCommands = filepath.Join(repo, ".claude", "commands")
				targetSkills = filepath.Join(repo, ".agents", "skills")
				params = &ExternalAgentConfigDetectParams{CWDs: []string{repo}}
			}
			if err := os.MkdirAll(filepath.Join(sourceCommands, "foo"), 0o700); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"aaa-fallback.md":     "Run the standalone fallback.\n",
				"zzz-described.md":    "---\ndescription: Standalone described command\n---\nRun the standalone described command.\n",
				"foo-bar.md":          "---\ndescription: Original description\n---\nRun the described command.\n",
				"foo bar.md":          "Run the plain fallback.\n",
				"foo_bar.md":          "---\ndescription: '  '\n---\nRun the blank-description fallback.\n",
				"foo/bar.md":          "---\ndescription: [unusable]\n---\nRun the unusable-description fallback.\n",
				"ambiguous-name.md":   "---\ndescription: First\n---\nRun the first described command.\n",
				"ambiguous_name.md":   "---\ndescription: Second\n---\nRun the second described command.\n",
				"ambiguous name.md":   "Run the ambiguous fallback.\n",
				"fallback-name.md":    "Run the first fallback.\n",
				"fallback_name.md":    "Run the second fallback.\n",
				"unsupported-name.md": "---\ndescription: Deploy\n---\nDeploy $ARGUMENTS.\n",
				"unsupported_name.md": "Run the supported fallback.\n",
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(sourceCommands, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			service := NewConfigService(codexHome)
			service.SetExternalAgentHome(externalHome)
			detected := service.DetectExternalAgentConfig(params)
			if len(detected.Items) != 1 {
				t.Fatalf("detected items = %#v", detected.Items)
			}
			item := detected.Items[0]
			if item.ItemType != MigrationCommands || item.Details == nil {
				t.Fatalf("detected command migration = %#v", item)
			}
			wantNames := []string{
				"source-command-aaa-fallback",
				"source-command-foo-bar",
				"source-command-unsupported-name",
				"source-command-zzz-described",
			}
			if len(item.Details.Commands) != len(wantNames) {
				t.Fatalf("detected commands = %#v, want %v", item.Details.Commands, wantNames)
			}
			for index, want := range wantNames {
				if got := item.Details.Commands[index].Name; got != want {
					t.Fatalf("detected command[%d] = %q, want %q", index, got, want)
				}
			}

			_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
			if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != len(wantNames) || len(completed.ItemTypeResults[0].Failures) != 0 {
				t.Fatalf("completed = %#v", completed)
			}
			for name, want := range map[string]string{
				"source-command-foo-bar":          "---\nname: \"source-command-foo-bar\"\ndescription: \"Original description\"\n---\n\n# source-command-foo-bar\n\nUse this skill when the user asks to run the migrated source command `foo-bar`.\n\n## Command Template\n\nRun the described command.\n",
				"source-command-unsupported-name": "---\nname: \"source-command-unsupported-name\"\ndescription: \"Migrated source command `unsupported_name`\"\n---\n\n# source-command-unsupported-name\n\nUse this skill when the user asks to run the migrated source command `unsupported_name`.\n\n## Command Template\n\nRun the supported fallback.\n",
			} {
				data, err := os.ReadFile(filepath.Join(targetSkills, name, "SKILL.md"))
				if err != nil {
					t.Fatal(err)
				}
				if got := string(data); got != want {
					t.Fatalf("%s = %q, want %q", name, got, want)
				}
			}
			for _, name := range []string{"source-command-ambiguous-name", "source-command-fallback-name"} {
				if pathExists(filepath.Join(targetSkills, name)) {
					t.Fatalf("ambiguous command %s was imported", name)
				}
			}
			if detectedAgain := service.DetectExternalAgentConfig(params); len(detectedAgain.Items) != 0 {
				t.Fatalf("imported commands detected again = %#v", detectedAgain.Items)
			}
		})
	}
}

func TestExternalCommandsMigrationFallbackDescriptionRewritesProductTerms(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "commands")
	target := filepath.Join(root, "skills")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "claude review.md"), []byte("Review the change.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := discoverExternalCommands(source, target)
	if len(commands) != 1 {
		t.Fatalf("commands = %#v", commands)
	}
	if !commands[0].FallbackDescription {
		t.Fatal("fallback command was not marked as using a derived description")
	}
	if got, want := commands[0].Description, "Migrated source command `claude review`"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
	if got, want := rewriteClaudeTerms(commands[0].Description), "Migrated source command `Codex review`"; got != want {
		t.Fatalf("rewritten description = %q, want %q", got, want)
	}
}
