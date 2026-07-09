package chatwidget

import (
	"strings"

	codextui "codex_go/internal/tui"
)

func SelectionViewRows(view SelectionView, selectedIndex int, width int) []string {
	rows := make([]string, 0, len(view.HeaderLines)+len(view.Items)+3)
	if strings.TrimSpace(view.Title) != "" {
		rows = append(rows, strings.TrimSpace(view.Title))
	}
	if strings.TrimSpace(view.Subtitle) != "" {
		rows = append(rows, strings.TrimSpace(view.Subtitle))
	}
	rows = append(rows, trimmedNonEmptyLines(view.HeaderLines)...)
	if selectedIndex < 0 {
		selectedIndex = view.InitialSelectedIndex
	}
	selectedIndex = clampSelectionIndex(view.Items, selectedIndex)
	for index, item := range view.Items {
		row := selectionItemRow(index, item, width)
		if index == selectedIndex && !item.Disabled {
			row = codextui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	if strings.TrimSpace(view.FooterHint) != "" {
		rows = append(rows, strings.TrimSpace(view.FooterHint))
	}
	return rows
}

func PermissionMenuViewRows(view PermissionMenuView, selectedIndex int, width int) []string {
	rows := make([]string, 0, len(view.HeaderLines)+len(view.Items)+3)
	if strings.TrimSpace(view.Title) != "" {
		rows = append(rows, strings.TrimSpace(view.Title))
	}
	rows = append(rows, trimmedNonEmptyLines(view.HeaderLines)...)
	if selectedIndex < 0 {
		selectedIndex = firstEnabledPermissionItemIndex(view.Items)
	}
	selectedIndex = clampPermissionItemIndex(view.Items, selectedIndex)
	for index, item := range view.Items {
		row := permissionItemRow(index, item, width)
		if index == selectedIndex && item.DisabledReason == "" {
			row = codextui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	if strings.TrimSpace(view.FooterNote) != "" {
		rows = append(rows, strings.TrimSpace(view.FooterNote))
	}
	if strings.TrimSpace(view.FooterHint) != "" {
		rows = append(rows, strings.TrimSpace(view.FooterHint))
	}
	return rows
}

func selectionItemRow(index int, item SelectionItem, width int) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = strings.TrimSpace(item.ID)
	}
	row := intString(index+1) + ". " + name
	if desc := strings.TrimSpace(item.Description); desc != "" {
		row += " - " + desc
	}
	if item.IsCurrent {
		row += " Currently selected"
	}
	if item.IsDefault {
		row += " Default"
	}
	if item.Disabled {
		reason := strings.TrimSpace(item.DisabledReason)
		if reason == "" {
			reason = "disabled"
		}
		row += " (" + reason + ")"
	}
	return truncateSelectionRow(row, width)
}

func permissionItemRow(index int, item PermissionMenuItem, width int) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = strings.TrimSpace(item.ID)
	}
	row := intString(index+1) + ". " + name
	if desc := strings.TrimSpace(item.Description); desc != "" {
		row += " - " + desc
	}
	if item.Current {
		row += " Currently selected"
	}
	if reason := strings.TrimSpace(item.DisabledReason); reason != "" {
		row += " (" + reason + ")"
	}
	return truncateSelectionRow(row, width)
}

func firstEnabledSelectionIndex(items []SelectionItem) int {
	for index, item := range items {
		if !item.Disabled {
			return index
		}
	}
	return 0
}

func firstEnabledPermissionItemIndex(items []PermissionMenuItem) int {
	for index, item := range items {
		if item.DisabledReason == "" {
			return index
		}
	}
	return 0
}

func clampSelectionIndex(items []SelectionItem, index int) int {
	if len(items) == 0 {
		return 0
	}
	if index < 0 || index >= len(items) || items[index].Disabled {
		return firstEnabledSelectionIndex(items)
	}
	return index
}

func clampPermissionItemIndex(items []PermissionMenuItem, index int) int {
	if len(items) == 0 {
		return 0
	}
	if index < 0 || index >= len(items) || items[index].DisabledReason != "" {
		return firstEnabledPermissionItemIndex(items)
	}
	return index
}

func trimmedNonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func truncateSelectionRow(row string, width int) string {
	if width <= 0 {
		return row
	}
	return codextui.TruncateWithEllipsis(row, width)
}
