package context

import (
	"fmt"
	"sort"
	"strings"
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
		runes := []rune(firstLine)
		if len(runes) > maxDeferredNamespaceDescriptionRunes {
			// Rust #48574 truncates at a UTF-8 boundary and appends an ellipsis
			// so a truncated description remains visibly incomplete.
			prefixLen := maxDeferredNamespaceDescriptionRunes - len(deferredDescriptionTruncationSuffix)
			firstLine = string(runes[:prefixLen]) + deferredDescriptionTruncationSuffix
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
	emptyState := ""
	if currentEmpty {
		emptyState = "No deferred tool namespaces remain.\n"
	}
	fixedBytes := 1
	for _, group := range groups {
		if len(group.values) > 0 {
			fixedBytes += len(group.label) + len(":\n")
		}
	}
	fixedBytes += len(emptyState)
	entryBudget := bodyBudget - fixedBytes
	if entryBudget < 0 {
		entryBudget = 0
	}
	omissionReserve := 0
	for _, group := range groups {
		if len(group.values) > 0 {
			omissionReserve += deferredToolsOmittedLineReserveBytes
		}
	}
	entries := truncateDeferredNamespaceRows(groups, entryBudget, omissionReserve)
	entryIndex := 0
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
		for range keys {
			entry := entries[entryIndex]
			entryIndex++
			if entry.omitted {
				omitted++
				continue
			}
			rendered.WriteString(entry.text)
		}
		if omitted > 0 {
			fmt.Fprintf(&rendered, "... %d additional namespaces omitted.\n", omitted)
		}
	}
	if currentEmpty {
		rendered.WriteString(emptyState)
	}
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
