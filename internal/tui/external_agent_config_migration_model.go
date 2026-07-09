package tui

import (
	"fmt"
	"strings"

	"codex_go/internal/config"
)

// Rust parity: codex-rs/tui/src/external_agent_config_migration_model.rs.

type ExternalAgentConfigMigrationModel struct {
	Source string
	Target string
}

type ExternalAgentConfigMigrationGroupModel struct {
	Label       string
	Description string
	ItemIndices []int
}

func ExternalAgentConfigMigrationGroups(items []config.ExternalAgentConfigMigrationItem) []ExternalAgentConfigMigrationGroupModel {
	toolsAndSetup := []int{}
	projects := []int{}
	chatSessions := []int{}
	projectCWDs := map[string]bool{}
	sessionCount := 0
	for idx, item := range items {
		switch {
		case item.ItemType == config.MigrationSessions:
			chatSessions = append(chatSessions, idx)
			if item.Details != nil {
				sessionCount += len(item.Details.Sessions)
			} else {
				sessionCount++
			}
		case item.CWD == nil:
			toolsAndSetup = append(toolsAndSetup, idx)
		default:
			projects = append(projects, idx)
			projectCWDs[*item.CWD] = true
		}
	}

	groups := []ExternalAgentConfigMigrationGroupModel{}
	if len(toolsAndSetup) > 0 {
		groups = append(groups, ExternalAgentConfigMigrationGroupModel{
			Label:       "Tools & setup",
			Description: "Settings, instructions, integrations, agents, commands, and skills",
			ItemIndices: toolsAndSetup,
		})
	}
	if len(projects) > 0 {
		label := "Current project"
		if len(projectCWDs) != 1 {
			label = fmt.Sprintf("Projects (%d)", len(projectCWDs))
		}
		groups = append(groups, ExternalAgentConfigMigrationGroupModel{
			Label:       label,
			Description: "Add Codex files alongside your existing project files",
			ItemIndices: projects,
		})
	}
	if len(chatSessions) > 0 {
		groups = append(groups, ExternalAgentConfigMigrationGroupModel{
			Label:       fmt.Sprintf("Chat sessions (%d)", sessionCount),
			Description: "Last 30 days of chats",
			ItemIndices: chatSessions,
		})
	}
	return groups
}

func ExternalAgentConfigMigrationItemLabel(item config.ExternalAgentConfigMigrationItem) string {
	switch item.ItemType {
	case config.MigrationAgentsMD:
		return "Instructions (CLAUDE.md -> AGENTS.md)"
	case config.MigrationConfig:
		return "Settings (settings.json -> config.toml)"
	case config.MigrationSkills:
		return "Skills"
	case config.MigrationPlugins:
		return "Plugins"
	case config.MigrationMCPServerConfig:
		return "MCP servers"
	case config.MigrationSubagents:
		return "Agents"
	case config.MigrationHooks:
		return "Hooks"
	case config.MigrationCommands:
		return "Slash commands"
	case config.MigrationSessions:
		return "Recent chat sessions"
	default:
		return string(item.ItemType)
	}
}

func ExternalAgentConfigMigrationTypeLabel(itemType config.MigrationItemType) string {
	switch itemType {
	case config.MigrationAgentsMD:
		return "Instructions"
	case config.MigrationConfig:
		return "Settings"
	case config.MigrationSkills:
		return "Skills"
	case config.MigrationPlugins:
		return "Plugins"
	case config.MigrationMCPServerConfig:
		return "MCP servers"
	case config.MigrationSubagents:
		return "Agents"
	case config.MigrationHooks:
		return "Hooks"
	case config.MigrationCommands:
		return "Slash commands"
	case config.MigrationSessions:
		return "Chat sessions"
	default:
		return string(itemType)
	}
}

func ExternalAgentConfigMigrationCountSummary(items []config.ExternalAgentConfigMigrationItem) string {
	type countEntry struct {
		itemType config.MigrationItemType
		count    int
	}
	counts := []countEntry{}
	for _, item := range items {
		count := ExternalAgentConfigMigrationItemCount(item)
		found := false
		for idx := range counts {
			if counts[idx].itemType == item.ItemType {
				counts[idx].count += count
				found = true
				break
			}
		}
		if !found {
			counts = append(counts, countEntry{itemType: item.ItemType, count: count})
		}
	}
	parts := make([]string, 0, len(counts))
	for _, entry := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", ExternalAgentConfigMigrationTypeLabel(entry.itemType), entry.count))
	}
	return strings.Join(parts, ", ")
}

func ExternalAgentConfigMigrationItemCount(item config.ExternalAgentConfigMigrationItem) int {
	if item.Details == nil {
		return 1
	}
	switch item.ItemType {
	case config.MigrationPlugins:
		count := 0
		for _, group := range item.Details.Plugins {
			count += len(group.PluginNames)
		}
		return defaultOne(count)
	case config.MigrationMCPServerConfig:
		return defaultOne(len(item.Details.MCPServers))
	case config.MigrationSubagents:
		return defaultOne(len(item.Details.Subagents))
	case config.MigrationHooks:
		return defaultOne(len(item.Details.Hooks))
	case config.MigrationCommands:
		return defaultOne(len(item.Details.Commands))
	case config.MigrationSessions:
		return defaultOne(len(item.Details.Sessions))
	case config.MigrationSkills:
		return defaultOne(len(item.Details.Skills))
	default:
		return 1
	}
}

func ExternalAgentConfigMigrationItemDetail(item config.ExternalAgentConfigMigrationItem) (string, bool) {
	if item.Details == nil {
		return "", false
	}
	switch item.ItemType {
	case config.MigrationPlugins, config.MigrationAgentsMD, config.MigrationConfig:
		return "", false
	case config.MigrationSkills:
		return formatCountedMigrationDetails("skill", len(item.Details.Skills), namedMigrationNames(item.Details.Skills)), true
	case config.MigrationMCPServerConfig:
		return formatCountedMigrationDetails("MCP server", len(item.Details.MCPServers), namedMigrationNames(item.Details.MCPServers)), true
	case config.MigrationSubagents:
		return formatCountedMigrationDetails("agent", len(item.Details.Subagents), namedMigrationNames(item.Details.Subagents)), true
	case config.MigrationHooks:
		return formatCountedMigrationDetails("hook", len(item.Details.Hooks), namedMigrationNames(item.Details.Hooks)), true
	case config.MigrationCommands:
		return formatCountedMigrationDetails("slash command", len(item.Details.Commands), namedMigrationNames(item.Details.Commands)), true
	case config.MigrationSessions:
		names := []string{}
		for _, session := range item.Details.Sessions {
			if session.Title != nil && strings.TrimSpace(*session.Title) != "" {
				names = append(names, strings.TrimSpace(*session.Title))
			}
		}
		return formatCountedMigrationDetails("chat session", len(item.Details.Sessions), names), true
	default:
		return "", false
	}
}

func defaultOne(count int) int {
	if count <= 0 {
		return 1
	}
	return count
}

func namedMigrationNames(values []config.NamedMigration) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return names
}

func formatCountedMigrationDetails(noun string, count int, names []string) string {
	suffix := ""
	if count != 1 {
		suffix = "s"
	}
	visible := []string{}
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			visible = append(visible, strings.TrimSpace(name))
		}
		if len(visible) == 4 {
			break
		}
	}
	if len(visible) == 0 {
		return fmt.Sprintf("%d %s%s", count, noun, suffix)
	}
	return fmt.Sprintf("%d %s%s: %s", count, noun, suffix, strings.Join(visible, ", "))
}
