package prompt

import "strings"

// WithoutUpdatePlanInstructions omits checklist-tool guidance from Codex-owned
// prompt text (Rust #41744). Call it only for bundled prompt text; custom
// instructions must be preserved verbatim.
func WithoutUpdatePlanInstructions(instructions string) string {
	lines := splitInclusiveLines(instructions)
	var rendered strings.Builder
	rendered.Grow(len(instructions))
	for index := 0; index < len(lines); {
		line := strings.TrimRight(strings.TrimRight(lines[index], "\r\n"), " \t")
		switch line {
		case "## Planning", "## `update_plan`", "## Plan tool", "## Plan Mode vs update_plan tool":
			end := len(lines)
			for next := index + 1; next < len(lines); next++ {
				if strings.HasPrefix(lines[next], "# ") || strings.HasPrefix(lines[next], "## ") {
					end = next
					break
				}
			}
			isChecklistSection := line != "## Planning"
			if line == "## Planning" {
				for _, candidate := range lines[index:end] {
					if strings.HasPrefix(candidate, "You have access to an `update_plan` tool") ||
						strings.HasPrefix(candidate, "When `update_plan` is available, follow this section") {
						isChecklistSection = true
						break
					}
				}
			}
			if isChecklistSection {
				index = end
				continue
			}
		}
		if line == "Progress visibility:" && index+1 < len(lines) &&
			strings.HasPrefix(lines[index+1], "If update_plan is available") {
			index += 2
			if index < len(lines) && strings.TrimSpace(lines[index]) == "" {
				index++
			}
			continue
		}
		if strings.HasPrefix(line, "- Use the plan tool ") ||
			strings.HasPrefix(line, "- If you create a checklist or task list,") {
			index++
			for index < len(lines) && (strings.HasPrefix(lines[index], " ") || strings.HasPrefix(lines[index], "\t")) {
				index++
			}
			continue
		}
		rendered.WriteString(lines[index])
		index++
	}
	return rendered.String()
}

func splitInclusiveLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(value, "\n")+1)
	start := 0
	for index := 0; index < len(value); index++ {
		if value[index] != '\n' {
			continue
		}
		lines = append(lines, value[start:index+1])
		start = index + 1
	}
	if start < len(value) {
		lines = append(lines, value[start:])
	}
	return lines
}
