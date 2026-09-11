package model

import "strings"

// knownReasoningEfforts are the built-in Rust `ReasoningEffort` wire values
// (plus `disabled`, which is the resolved form of `persistent`). Any other
// value is a model-defined custom effort.
var knownReasoningEfforts = map[string]struct{}{
	"none":       {},
	"minimal":    {},
	"low":        {},
	"medium":     {},
	"high":       {},
	"xhigh":      {},
	"max":        {},
	"ultra":      {},
	"persistent": {},
	"disabled":   {},
}

// IsKnownReasoningEffort reports whether effort maps to a built-in
// ReasoningEffort variant rather than a model-defined custom value.
func IsKnownReasoningEffort(effort string) bool {
	_, ok := knownReasoningEfforts[strings.TrimSpace(effort)]
	return ok
}

// ResolveReasoningEffort mirrors Rust `ModelInfo::resolve_reasoning_effort`
// (#43110), the model-owned normalization shared by native requests and
// trusted configuration-update items: `ultra` resolves through the model's
// multi-agent effort / supported levels, and `persistent` becomes the wire
// value `disabled`. Other efforts pass through unchanged.
func ResolveReasoningEffort(info *ModelInfo, effort string) string {
	effort = strings.TrimSpace(effort)
	switch effort {
	case "ultra":
		return reasoningEffortForRequest(info, effort)
	case "persistent":
		return "disabled"
	default:
		return effort
	}
}
