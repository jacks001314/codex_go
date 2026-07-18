package bottompane

import "strings"

const (
	ActionRequiredPreviewPrefix = "[ ! ] Action Required"
	actionRequiredSpinnerID     = "activity"
	actionRequiredSpinnerAlias  = "spinner"
)

type ActionRequiredTitle struct {
	Title string
	Count int
}

func (t ActionRequiredTitle) Text() string {
	if t.Title != "" {
		return t.Title
	}
	if t.Count > 1 {
		return "Action required"
	}
	return "Action required"
}

func BuildActionRequiredTitleText(prefix string, items []string, excludedItems []string, valueFor func(string) (string, bool)) string {
	parts := []string{prefix}
	excluded := map[string]bool{}
	for _, item := range excludedItems {
		excluded[item] = true
	}
	for _, item := range items {
		if item == actionRequiredSpinnerID || item == actionRequiredSpinnerAlias || excluded[item] {
			continue
		}
		if valueFor == nil {
			continue
		}
		value, ok := valueFor(item)
		if ok {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " | ")
}

func BuildActionRequiredTitleTextFromValues(prefix string, items []string, excludedItems []string, values map[string]string) string {
	return BuildActionRequiredTitleText(prefix, items, excludedItems, func(item string) (string, bool) {
		value, ok := values[item]
		return value, ok
	})
}
