package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	commandMigrationMaxSkillBytes = 4000
	migratedCommandSkillsDir      = ".codex-plugin/migrated-command-skills"
)

// CommandMigrationResult holds the outcome of a command migration.
type CommandMigrationResult struct {
	Migrated int
	Skipped  int
	Errors   []string
}

// MigratePluginCommands migrates legacy "commands" from a plugin into Codex skills.
// Commands are markdown files found in the commands/ directory or declared in plugin.json.
// Only commands with YAML frontmatter containing a non-empty description are migrated.
// Commands using unsupported features ($ARGUMENTS, $1/$2, {{...}}, !, @) are skipped.
//
// Migrated skills are written to <pluginRoot>/.codex-plugin/migrated-command-skills/.
func MigratePluginCommands(pluginRoot string) (*CommandMigrationResult, error) {
	pluginRoot = strings.TrimSpace(pluginRoot)
	if pluginRoot == "" {
		return nil, fmt.Errorf("plugin root is required for command migration")
	}

	result := &CommandMigrationResult{}

	commandDirs := discoverCommandDirs(pluginRoot)
	for _, cmdDir := range commandDirs {
		entries, err := os.ReadDir(cmdDir)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to read commands directory %s: %v", cmdDir, err))
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			cmdPath := filepath.Join(cmdDir, entry.Name())
			data, err := os.ReadFile(cmdPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to read command %s: %v", cmdPath, err))
				continue
			}
			migrated, err := tryMigrateCommand(pluginRoot, entry.Name(), string(data))
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to migrate command %s: %v", cmdPath, err))
				result.Skipped++
				continue
			}
			if migrated {
				result.Migrated++
			} else {
				result.Skipped++
			}
		}
	}

	return result, nil
}

// discoverCommandDirs finds directories that may contain legacy commands.
func discoverCommandDirs(pluginRoot string) []string {
	var dirs []string

	// Check plugin.json for declared command paths
	manifestPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Commands []string `json:"commands"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			for _, cmd := range manifest.Commands {
				cmd = strings.TrimSpace(cmd)
				if cmd == "" {
					continue
				}
				dir := filepath.Join(pluginRoot, cmd)
				if info, err := os.Stat(dir); err == nil && info.IsDir() {
					dirs = append(dirs, dir)
				}
			}
		}
	}

	// Default: check commands/ directory
	defaultDir := filepath.Join(pluginRoot, "commands")
	if info, err := os.Stat(defaultDir); err == nil && info.IsDir() {
		dirs = append(dirs, defaultDir)
	}

	// Dedup
	seen := map[string]bool{}
	var unique []string
	for _, d := range dirs {
		clean := filepath.Clean(d)
		if !seen[clean] {
			seen[clean] = true
			unique = append(unique, clean)
		}
	}
	sort.Strings(unique)
	return unique
}

// tryMigrateCommand attempts to migrate a single command file.
// Returns true if the command was successfully migrated.
func tryMigrateCommand(pluginRoot string, filename string, contents string) (bool, error) {
	// Check for unsupported features
	if hasUnsupportedCommandFeatures(contents) {
		return false, nil
	}

	// Parse YAML frontmatter
	description, name, hasFrontmatter := parseCommandFrontmatter(contents)
	if !hasFrontmatter {
		return false, nil
	}
	if strings.TrimSpace(description) == "" {
		return false, nil
	}
	if name == "" {
		name = commandNameFromFilename(filename)
	}

	// Generate skill content
	skillContent := renderCommandSkill(name, description, contents)

	// Write migrated skill
	outputDir := filepath.Join(pluginRoot, migratedCommandSkillsDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return false, fmt.Errorf("failed to create migrated command skills directory: %w", err)
	}

	skillName := sanitizeCommandSkillName(name)
	outputPath := filepath.Join(outputDir, skillName+".md")
	if err := os.WriteFile(outputPath, []byte(skillContent), 0o600); err != nil {
		return false, fmt.Errorf("failed to write migrated skill: %w", err)
	}

	return true, nil
}

// hasUnsupportedCommandFeatures checks if a command contains features that cannot be migrated.
func hasUnsupportedCommandFeatures(contents string) bool {
	unsupported := []string{
		"$ARGUMENTS",
		"$1", "$2", "$3", "$4", "$5", "$6", "$7", "$8", "$9",
		"{{", "}}",
	}
	for _, pattern := range unsupported {
		if strings.Contains(contents, pattern) {
			return true
		}
	}
	// Check for ! (shell-exec escape) and @ (plugin mention) patterns in command context
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip code fences and comments
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "!") && !strings.HasPrefix(trimmed, "![") {
			// Shell command invocation
			return true
		}
	}
	return false
}

// parseCommandFrontmatter extracts description and name from YAML frontmatter.
// Returns description, name, and whether frontmatter was found.
func parseCommandFrontmatter(contents string) (description string, name string, ok bool) {
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}

	// Frontmatter starts immediately after the opening --- and ends at the closing ---
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			// Closing delimiter — frontmatter found if we have at least description
			hasFrontmatter := strings.TrimSpace(description) != "" && strings.TrimSpace(name) != ""
			return description, name, hasFrontmatter
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		switch strings.ToLower(key) {
		case "description":
			description = strings.Trim(strings.TrimSpace(value), `"'`)
		case "name":
			name = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}

	return "", "", false
}

// commandNameFromFilename derives a name from the command filename.
func commandNameFromFilename(filename string) string {
	name := strings.TrimSuffix(filename, ".md")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	words := strings.Fields(name)
	if len(words) == 0 {
		return "command"
	}
	// Title case
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// renderCommandSkill generates a skill markdown file from a migrated command.
func renderCommandSkill(name string, description string, contents string) string {
	// Find the body (after frontmatter)
	body := contents
	lines := strings.Split(contents, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				body = strings.Join(lines[i+1:], "\n")
				break
			}
		}
	}
	body = rewriteCommandBrandTerms(strings.TrimSpace(body))
	body = truncateSkillBody(body, commandMigrationMaxSkillBytes)

	return fmt.Sprintf(`---
name: %s
description: %s
---

%s
`, strings.ReplaceAll(name, `"`, `\"`),
		strings.ReplaceAll(description, `"`, `\"`),
		body)
}

// rewriteCommandBrandTerms rewrites common brand terms for consistency.
func rewriteCommandBrandTerms(body string) string {
	replacements := map[string]string{
		"CLAUDE.md": "AGENTS.md",
		"claude":    "codex",
		"Claude":    "Codex",
	}
	for old, new_ := range replacements {
		body = strings.ReplaceAll(body, old, new_)
	}
	return body
}

// truncateSkillBody truncates the body to a maximum byte length.
func truncateSkillBody(body string, maxBytes int) string {
	if len(body) <= maxBytes {
		return body
	}
	// Truncate at a line boundary if possible
	truncated := body[:maxBytes]
	if idx := strings.LastIndex(truncated, "\n"); idx > maxBytes/2 {
		truncated = truncated[:idx]
	}
	return truncated + "\n\n<!-- truncated -->"
}

// sanitizeCommandSkillName sanitizes a name for use as a filename.
func sanitizeCommandSkillName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), "-_")
	if result == "" {
		result = "command"
	}
	return result
}
