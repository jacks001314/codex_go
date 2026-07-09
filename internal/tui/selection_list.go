package tui

import "strings"

// Rust parity: codex-rs/tui/src/selection_list.rs.

type SelectionItem struct {
	ID          string
	Label       string
	Description string
	Dim         bool
	Disabled    bool
}

type SelectionList struct {
	Items    []SelectionItem
	Selected int
}

func NewSelectionList(items []SelectionItem) *SelectionList {
	list := &SelectionList{Items: append([]SelectionItem(nil), items...)}
	list.clamp()
	return list
}

func (l *SelectionList) Move(delta int) {
	if l == nil || len(l.Items) == 0 {
		return
	}
	index := l.Selected
	for steps := 0; steps < len(l.Items); steps++ {
		index = (index + delta) % len(l.Items)
		if index < 0 {
			index += len(l.Items)
		}
		if !l.Items[index].Disabled {
			l.Selected = index
			return
		}
	}
}

func (l *SelectionList) Select(index int) {
	if l == nil || index < 0 || index >= len(l.Items) || l.Items[index].Disabled {
		return
	}
	l.Selected = index
}

func (l *SelectionList) SelectedItem() (SelectionItem, bool) {
	if l == nil || len(l.Items) == 0 || l.Selected < 0 || l.Selected >= len(l.Items) {
		return SelectionItem{}, false
	}
	item := l.Items[l.Selected]
	return item, !item.Disabled
}

func (l *SelectionList) RenderRows(width int) []string {
	if l == nil || len(l.Items) == 0 {
		return []string{"No options"}
	}
	rows := make([]string, 0, len(l.Items))
	for i, item := range l.Items {
		selected := i == l.Selected
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = item.ID
		}
		row := NumberedSelectionPrefix(i, selected) + label
		if item.Description != "" {
			row += " - " + item.Description
		}
		if item.Disabled {
			row += " (disabled)"
		}
		if width > 0 {
			row = TruncateWithEllipsis(row, width)
		}
		if selected {
			row = RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	return rows
}

func (l *SelectionList) clamp() {
	if l == nil || len(l.Items) == 0 {
		return
	}
	if l.Selected < 0 || l.Selected >= len(l.Items) || l.Items[l.Selected].Disabled {
		for i := range l.Items {
			if !l.Items[i].Disabled {
				l.Selected = i
				return
			}
		}
		l.Selected = 0
	}
}

func intLabel(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
