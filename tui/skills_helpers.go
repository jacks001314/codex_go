package tui

import "strings"

func NormalizeSkillName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
