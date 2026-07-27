package context

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxDeferredToolsFragmentBytes        = 4 * 1024
	maxDeferredNamespaceDescriptionRunes = 250
	deferredToolsOmittedLineReserveBytes = 64
)

func NormalizeDeferredToolNamespaces(namespaces map[string]string) map[string]string {
	if len(namespaces) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(namespaces))
	for namespace, description := range namespaces {
		firstLine := strings.TrimSpace(strings.SplitN(description, "\n", 2)[0])
		if utf8.RuneCountInString(firstLine) > maxDeferredNamespaceDescriptionRunes {
			firstLine = string([]rune(firstLine)[:maxDeferredNamespaceDescriptionRunes])
		}
		out[namespace] = firstLine
	}
	return out
}

func DeferredToolsStateFragment(current map[string]string, previous map[string]string, previousKnown bool) Fragment {
	current = NormalizeDeferredToolNamespaces(current)
	if previousKnown && equalStringMaps(current, previous) {
		return nil
	}
	if len(current) == 0 && !previousKnown {
		return nil
	}
	groups := []deferredNamespaceGroup{}
	if !previousKnown {
		groups = append(groups, deferredNamespaceGroup{label: "Deferred tool namespaces", values: current})
	} else {
		added := map[string]string{}
		removed := map[string]string{}
		for namespace, description := range current {
			if prior, ok := previous[namespace]; !ok || prior != description {
				added[namespace] = description
			}
		}
		for namespace, description := range previous {
			if _, ok := current[namespace]; !ok {
				removed[namespace] = description
			}
		}
		groups = append(groups,
			deferredNamespaceGroup{label: "Added deferred tool namespaces", values: added},
			deferredNamespaceGroup{label: "Removed deferred tool namespaces", values: removed},
		)
	}
	body := renderDeferredNamespaceGroups(groups, len(current) == 0)
	return NewSimpleFragment(RoleDeveloper, "<tools>", "</tools>", body)
}

type deferredNamespaceGroup struct {
	label  string
	values map[string]string
}

func renderDeferredNamespaceGroups(groups []deferredNamespaceGroup, currentEmpty bool) string {
	bodyBudget := maxDeferredToolsFragmentBytes - len("<tools>") - len("</tools>")
	fixedBytes := 1
	for _, group := range groups {
		if len(group.values) > 0 {
			fixedBytes += len(group.label) + len(":\n") + deferredToolsOmittedLineReserveBytes
		}
	}
	if currentEmpty {
		fixedBytes += len("No deferred tool namespaces remain.\n")
	}
	remaining := bodyBudget - fixedBytes
	if remaining < 0 {
		remaining = 0
	}
	var rendered strings.Builder
	rendered.WriteByte('\n')
	for _, group := range groups {
		if len(group.values) == 0 {
			continue
		}
		rendered.WriteString(group.label)
		rendered.WriteString(":\n")
		keys := make([]string, 0, len(group.values))
		for namespace := range group.values {
			keys = append(keys, namespace)
		}
		sort.Strings(keys)
		omitted := 0
		for _, namespace := range keys {
			entry := renderDeferredNamespace(namespace, group.values[namespace])
			if len(entry) <= remaining {
				remaining -= len(entry)
				rendered.WriteString(entry)
			} else {
				omitted++
			}
		}
		if omitted > 0 {
			fmt.Fprintf(&rendered, "... %d additional namespaces omitted.\n", omitted)
		}
	}
	if currentEmpty {
		rendered.WriteString("No deferred tool namespaces remain.\n")
	}
	return rendered.String()
}

func renderDeferredNamespace(namespace string, description string) string {
	var rendered strings.Builder
	rendered.WriteString("- ")
	_ = xml.EscapeText(&rendered, []byte(namespace))
	if description != "" {
		rendered.WriteString(": ")
		_ = xml.EscapeText(&rendered, []byte(description))
	}
	rendered.WriteByte('\n')
	return rendered.String()
}

func equalStringMaps(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
