package context

import (
	"sort"
	"unicode/utf8"
)

// Rust parity: codex-rs/core/src/context/world_state/tools_budget.rs (#48574).
// Deferred tool namespace summaries reserve every namespace name before sharing
// the remaining byte budget across descriptions, so long descriptions can no
// longer hide later names from the model.

const deferredDescriptionTruncationSuffix = "..."

// deferredNamespaceEntry is one rendered namespace row. An omitted row keeps no
// name or description and is replaced by the group's omission notice.
type deferredNamespaceEntry struct {
	omitted bool
	text    string
}

// deferedNamespaceRow is a flattened (namespace, description) pair in group
// order, with each group's namespaces sorted like Rust's BTreeMap iteration.
type deferredNamespaceRow struct {
	namespace   string
	description string
}

// truncateDeferredNamespaceRows mirrors Rust's `truncate_namespace_rows`: it
// returns complete rows in group order with `omitted` set for names that do not
// fit. The budget excludes headings; omission space is charged only when names
// overflow.
func truncateDeferredNamespaceRows(groups []deferredNamespaceGroup, byteBudget int, omissionReserveBytes int) []deferredNamespaceEntry {
	rows := flattenDeferredNamespaceRows(groups)
	retained := calculateDeferredRowTruncation(rows, byteBudget, omissionReserveBytes)
	out := make([]deferredNamespaceEntry, len(rows))
	for i, row := range rows {
		retainedBytes := retained[i]
		switch {
		case retainedBytes < 0:
			out[i] = deferredNamespaceEntry{omitted: true}
		case retainedBytes == 0:
			out[i] = deferredNamespaceEntry{text: "- " + row.namespace + "\n"}
		case retainedBytes == len(row.description):
			out[i] = deferredNamespaceEntry{text: "- " + row.namespace + ": " + row.description + "\n"}
		case retainedBytes >= len(deferredDescriptionTruncationSuffix):
			prefixBytes := floorCharBoundary(row.description, retainedBytes-len(deferredDescriptionTruncationSuffix))
			out[i] = deferredNamespaceEntry{
				text: "- " + row.namespace + ": " + row.description[:prefixBytes] + deferredDescriptionTruncationSuffix + "\n",
			}
		default:
			out[i] = deferredNamespaceEntry{text: "- " + row.namespace + "\n"}
		}
	}
	return out
}

func flattenDeferredNamespaceRows(groups []deferredNamespaceGroup) []deferredNamespaceRow {
	rows := []deferredNamespaceRow{}
	for _, group := range groups {
		keys := make([]string, 0, len(group.values))
		for namespace := range group.values {
			keys = append(keys, namespace)
		}
		sort.Strings(keys)
		for _, namespace := range keys {
			rows = append(rows, deferredNamespaceRow{namespace: namespace, description: group.values[namespace]})
		}
	}
	return rows
}

// calculateDeferredRowTruncation reserves all names before sharing description
// space in character-round order. A negative result omits the row; 0 keeps only
// its name; n keeps n description bytes. Mirrors Rust
// `calculate_namespace_row_truncation`.
func calculateDeferredRowTruncation(rows []deferredNamespaceRow, byteBudget int, omissionReserveBytes int) []int {
	totalNameBytes := 0
	for _, row := range rows {
		totalNameBytes += 2 + len(row.namespace) + 1
	}
	if totalNameBytes > byteBudget {
		remaining := byteBudget - omissionReserveBytes
		if remaining < 0 {
			remaining = 0
		}
		retained := make([]int, len(rows))
		for i, row := range rows {
			nameOnlyBytes := 2 + len(row.namespace) + 1
			if nameOnlyBytes <= remaining {
				remaining -= nameOnlyBytes
				retained[i] = 0
			} else {
				retained[i] = -1
			}
		}
		return retained
	}

	remaining := byteBudget - totalNameBytes
	totalDescriptionBytes := 0
	for _, row := range rows {
		if row.description != "" {
			totalDescriptionBytes += 2 + len(row.description)
		}
	}
	retained := make([]int, len(rows))
	if totalDescriptionBytes <= remaining {
		for i, row := range rows {
			retained[i] = len(row.description)
		}
		return retained
	}
	if remaining == 0 {
		return retained
	}

	type cursor struct {
		rowIndex  int
		byteIndex int
	}
	queue := make([]cursor, 0, len(rows))
	for i, row := range rows {
		if row.description != "" {
			queue = append(queue, cursor{rowIndex: i})
		}
	}
	// Each visit considers one character. Exhausted rows and prefixes that cannot
	// fit leave the queue permanently, because the remaining budget only shrinks.
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		description := rows[current.rowIndex].description
		if current.byteIndex >= len(description) {
			continue
		}
		_, size := utf8.DecodeRuneInString(description[current.byteIndex:])
		extraBytes := size
		if current.byteIndex == 0 {
			extraBytes += 2
		}
		if extraBytes <= remaining {
			remaining -= extraBytes
			retained[current.rowIndex] = current.byteIndex + size
			queue = append(queue, cursor{rowIndex: current.rowIndex, byteIndex: current.byteIndex + size})
		}
	}
	return retained
}
