package appserver

import (
	"sort"
	"strings"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/features"
	"codex_go/turn"
	"codex_go/utils"
)

const (
	// additionalContextToolNamespace is the namespace Rust uses for the
	// `function_call_output` items that carry untrusted additional context.
	additionalContextToolNamespace = "agent"
	// additionalContextValueTokenBudget bounds an untrusted context value before
	// it is compared for deduplication or sent to the model.
	additionalContextValueTokenBudget = 1000
	// maxDerivedAdditionalContextNameChars bounds a tool name derived from an
	// opaque context key.
	maxDerivedAdditionalContextNameChars = 128
	// fallbackAdditionalContextToolName is used when a context key yields no
	// usable tool name.
	fallbackAdditionalContextToolName = "untrusted_content"
)

// storedAdditionalContext is the thread's last rendered representation of one
// additional-context key (Rust's StoredAdditionalContext). Equality decides
// whether a later turn re-emits the value.
type storedAdditionalContext struct {
	toolOutput bool
	name       string
	kind       turn.AdditionalContextKind
	value      string
}

func (s storedAdditionalContext) equal(other storedAdditionalContext) bool {
	if s.toolOutput != other.toolOutput {
		return false
	}
	if s.toolOutput {
		return s.name == other.name && s.value == other.value
	}
	return s.kind == other.kind && s.value == other.value
}

// mergeAdditionalContext deduplicates this request's additional context against
// the thread's previous values and returns the developer fragments and the user
// / tool-output items that still need to reach the model (Rust
// state::additional_context::AdditionalContextStore::merge).
func (r *RuntimeRouter) mergeAdditionalContext(threadID string, values map[string]turn.AdditionalContextEntry, toolOutputs bool) ([]string, []any) {
	next := make(map[string]storedAdditionalContext, len(values))
	for key, entry := range values {
		if toolOutputs && entry.Kind == turn.AdditionalContextUntrusted {
			name := derivedAdditionalContextToolName(key)
			if entry.Name != nil {
				name = *entry.Name
			}
			next[key] = storedAdditionalContext{
				toolOutput: true,
				name:       name,
				kind:       entry.Kind,
				value:      truncateAdditionalContextValue(entry.Value),
			}
			continue
		}
		next[key] = storedAdditionalContext{kind: entry.Kind, value: entry.Value}
	}

	threadID = strings.TrimSpace(threadID)
	var previous map[string]storedAdditionalContext
	if r != nil && threadID != "" {
		r.additionalContextMu.Lock()
		previous = r.additionalContext[threadID]
		if r.additionalContext == nil {
			r.additionalContext = map[string]map[string]storedAdditionalContext{}
		}
		r.additionalContext[threadID] = next
		r.additionalContextMu.Unlock()
	}

	developer := []string{}
	items := []any{}
	for _, key := range sortedStoredAdditionalContextKeys(next) {
		stored := next[key]
		if prior, ok := previous[key]; ok && prior.equal(stored) {
			continue
		}
		if stored.toolOutput {
			items = append(items, map[string]any{
				"type":      "function_call_output",
				"name":      stored.name,
				"namespace": additionalContextToolNamespace,
				"output":    stored.value,
			})
			continue
		}
		role := contextfrag.RoleUser
		if stored.kind == turn.AdditionalContextApplication {
			role = contextfrag.RoleDeveloper
		}
		rendered := contextfrag.Render(contextfrag.NewAdditionalContextFragment(role, key, stored.value))
		if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
			continue
		}
		if rendered.Role == contextfrag.RoleDeveloper {
			developer = append(developer, rendered.Content)
			continue
		}
		items = append(items, map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": rendered.Content,
			}},
		})
	}
	return developer, items
}

// forgetAdditionalContext drops a thread's deduplication state when its
// session is unloaded (Rust drops the session's AdditionalContextStore).
func (r *RuntimeRouter) forgetAdditionalContext(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.additionalContextMu.Lock()
	defer r.additionalContextMu.Unlock()
	delete(r.additionalContext, threadID)
}

// derivedAdditionalContextToolName turns an opaque source key into a bounded
// tool name, mirroring Rust's tool_name_from_key.
func derivedAdditionalContextToolName(key string) string {
	var builder strings.Builder
	written := 0
	for _, char := range key {
		if char == ' ' {
			char = '_'
		}
		if !isAdditionalContextToolNameChar(char) {
			continue
		}
		builder.WriteRune(char)
		written++
		if written == maxDerivedAdditionalContextNameChars {
			break
		}
	}
	if builder.Len() == 0 {
		return fallbackAdditionalContextToolName
	}
	return builder.String()
}

func isAdditionalContextToolNameChar(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z':
		return true
	case char >= 'A' && char <= 'Z':
		return true
	case char >= '0' && char <= '9':
		return true
	default:
		return char == '_'
	}
}

func truncateAdditionalContextValue(value string) string {
	truncated, _ := utils.TruncateMiddleWithTokenBudget(value, additionalContextValueTokenBudget)
	return truncated
}

func sortedStoredAdditionalContextKeys(values map[string]storedAdditionalContext) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// additionalContextToolOutputsEnabled reports whether untrusted additional
// context is delivered as agent-namespaced tool outputs (Rust
// features.untrusted_as_tool_output).
func additionalContextToolOutputsEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return features.Enabled(cfg.FeatureSettings(), "untrusted_as_tool_output")
}
