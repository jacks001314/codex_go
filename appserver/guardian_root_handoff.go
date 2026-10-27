package appserver

import (
	"encoding/json"
	"sort"
	"strings"

	"codex_go/session"
)

// guardianRootHandoffSelection implements the recorded-boundary projection:
// recorded collaboration calls to the worker or an ancestor mark approximate
// relevance boundaries. Each boundary contributes the three preceding root
// messages, and the three latest messages are always kept so a recent
// cancellation stays visible. All user restrictions and unordered legacy
// evidence are preserved for the shared cap (#50026).
func guardianRootHandoffSelection(
	items []session.Item,
	candidates []guardianRootCandidate,
	workerPath string,
	toolNamespace string,
) []guardianRootCandidate {
	boundaries := guardianRootHandoffBoundaries(items, workerPath, toolNamespace)
	if len(boundaries) == 0 || len(candidates) == 0 {
		return nil
	}
	ordered := append([]guardianRootCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return guardianRootCandidateSortLess(ordered[i], ordered[j])
	})
	selected := guardianSelectRootHandoffIndices(ordered, boundaries)
	if selected == nil {
		return nil
	}
	indices := make([]int, 0, len(selected))
	for index := range selected {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	out := make([]guardianRootCandidate, 0, len(indices))
	for _, index := range indices {
		out = append(out, ordered[index])
	}
	return out
}

// guardianRootHandoffBoundaries records the acceptance order in effect at each
// recorded handoff call. Assistant items carry the same order sidecar as user
// inputs, so every annotated item advances the boundary.
func guardianRootHandoffBoundaries(items []session.Item, workerPath string, toolNamespace string) []uint64 {
	workerPath = runtimeCanonicalAgentPath("/root", workerPath)
	if workerPath == "" || workerPath == "/root" {
		return nil
	}
	var boundaries []uint64
	var precedingOrder uint64
	hasPrecedingOrder := false
	for index := range items {
		item := &items[index]
		if order, ok := guardianRootItemOrder(item); ok {
			precedingOrder = order
			hasPrecedingOrder = true
		}
		if !hasPrecedingOrder || !guardianRootItemIsHandoff(item, workerPath, toolNamespace) {
			continue
		}
		boundaries = append(boundaries, precedingOrder)
	}
	return boundaries
}

func guardianRootItemOrder(item *session.Item) (uint64, bool) {
	metadata := harnessMetadataFromSessionItem(item)
	if metadata == nil || metadata.InheritedUserMessage || metadata.UserInputOrder == nil {
		return 0, false
	}
	return *metadata.UserInputOrder, true
}

func guardianRootItemIsHandoff(item *session.Item, workerPath string, toolNamespace string) bool {
	if item == nil {
		return false
	}
	if guardianRootDirectHandoff(item, workerPath, toolNamespace) {
		return true
	}
	return guardianRootNestedHandoff(item, workerPath, toolNamespace)
}

func guardianRootDirectHandoff(item *session.Item, workerPath string, toolNamespace string) bool {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "function_call", "custom_tool_call":
	default:
		return false
	}
	name := firstNonEmpty(
		strings.TrimSpace(item.Name),
		stringValueFromMap(item.Data, "name"),
	)
	arguments := item.Data["arguments"]
	return guardianRootHandoffArguments(name, arguments, workerPath, toolNamespace)
}

func guardianRootNestedHandoff(item *session.Item, workerPath string, toolNamespace string) bool {
	if item == nil || item.Data == nil {
		return false
	}
	rawCalls, ok := item.Data["executed_tool_calls"]
	if !ok {
		return false
	}
	for _, raw := range anySlice(rawCalls) {
		call, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := firstNonEmpty(stringValueFromMap(call, "name"), stringValueFromMap(call, "tool_name"))
		arguments := call["arguments"]
		if arguments == nil {
			arguments = call["raw_arguments"]
		}
		if guardianRootHandoffArguments(name, arguments, workerPath, toolNamespace) {
			return true
		}
	}
	return false
}

func guardianRootHandoffArguments(name string, rawArguments any, workerPath string, toolNamespace string) bool {
	name = guardianRootHandoffToolName(name, toolNamespace)
	field := ""
	switch name {
	case "spawn_agent":
		field = "task_name"
	case "send_message", "followup_task":
		field = "target"
	default:
		return false
	}
	arguments := guardianRootArgumentsMap(rawArguments)
	recipient := runtimeCanonicalAgentPath("/root", stringValueFromMap(arguments, field))
	if recipient == "/root" || recipient == "" {
		return false
	}
	return workerPath == recipient || strings.HasPrefix(workerPath, recipient+"/")
}

func guardianRootHandoffToolName(name string, toolNamespace string) string {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{
		strings.TrimSpace(toolNamespace) + ".",
		strings.TrimSpace(toolNamespace) + "__",
		"collaboration.",
		"collaboration__",
	} {
		if prefix == "." || prefix == "__" {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

func guardianRootArgumentsMap(raw any) map[string]any {
	switch value := raw.(type) {
	case map[string]any:
		return value
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			return decoded
		}
	case json.RawMessage:
		var decoded map[string]any
		if json.Unmarshal(value, &decoded) == nil {
			return decoded
		}
	}
	return nil
}

func anySlice(value any) []any {
	switch slice := value.(type) {
	case []any:
		return slice
	case []map[string]any:
		out := make([]any, len(slice))
		for index := range slice {
			out[index] = slice[index]
		}
		return out
	}
	return nil
}
