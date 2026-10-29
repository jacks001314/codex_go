package analytics

import "strings"

// AnalyticsToolName turns a plugin or skill key into a readable label: a
// namespace prefix that repeats the name is dropped, and well-known
// abbreviations keep their spelling (Rust analytics/tool_panel.rs tool_name).
func AnalyticsToolName(label string) string {
	name := label
	if namespace, rest, found := strings.Cut(label, ":"); found {
		if strings.EqualFold(strings.TrimSpace(namespace), strings.TrimSpace(rest)) {
			name = strings.TrimSpace(rest)
		}
	}
	words := strings.Fields(name)
	for index, word := range words {
		switch strings.ToLower(word) {
		case "pr":
			words[index] = "PR"
		case "cli":
			words[index] = "CLI"
		case "api":
			words[index] = "API"
		case "ide":
			words[index] = "IDE"
		case "mcp":
			words[index] = "MCP"
		case "sdk":
			words[index] = "SDK"
		case "ui":
			words[index] = "UI"
		case "tui":
			words[index] = "TUI"
		case "openai":
			words[index] = "OpenAI"
		}
	}
	return strings.Join(words, " ")
}
