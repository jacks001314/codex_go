package appserver

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/compact"
	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/features"
	"codex_go/metrics"
	"codex_go/session"
	"codex_go/telemetry"
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
	// itemID is the response-item identity assigned when this value was last
	// rendered into history, so compaction can restore it (#61390).
	itemID string
}

// additionalContextToolOutput is a rendered untrusted context item that must
// also be recorded as reviewable conversation history.
type additionalContextToolOutput struct {
	key        string
	name       string
	value      string
	kind       turn.AdditionalContextKind
	itemID     string
	toolOutput bool
	rendered   string
}

// retainedAdditionalContext copies the thread's stored context when a
// submission omits its context update (Rust #62602). Stored tool-output names
// are preserved so re-merging the retained set stays a no-op.
func (r *RuntimeRouter) retainedAdditionalContext(threadID string) map[string]storedAdditionalContext {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return map[string]storedAdditionalContext{}
	}
	r.additionalContextMu.Lock()
	stored := r.additionalContext[threadID]
	retained := make(map[string]storedAdditionalContext, len(stored))
	for key, value := range stored {
		retained[key] = value
	}
	r.additionalContextMu.Unlock()
	return retained
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
func (r *RuntimeRouter) mergeAdditionalContext(threadID string, values map[string]turn.AdditionalContextEntry, toolOutputs bool) ([]string, []any, []additionalContextToolOutput) {
	var next map[string]storedAdditionalContext
	if values == nil {
		// Rust #62602: omitting the context update or sending `null` keeps the
		// thread's current set. A supplied map replaces it, so `{}` clears the
		// set while earlier rendered messages stay in conversation history.
		next = r.retainedAdditionalContext(threadID)
	} else {
		next = make(map[string]storedAdditionalContext, len(values))
		for key, entry := range values {
			// Rust #58144: every submitted entry is measured, including entries
			// that deduplication leaves unchanged and entries whose rendered
			// fragment is dropped.
			recordAdditionalContextMetrics(entry)
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
	persisted := []additionalContextToolOutput{}
	for _, key := range sortedStoredAdditionalContextKeys(next) {
		stored := next[key]
		if prior, ok := previous[key]; ok && prior.equal(stored) {
			// Keep the response-item identity so compaction can find the
			// history item this value already produced (#61390).
			stored.itemID = prior.itemID
			next[key] = stored
			continue
		}
		stored.itemID = r.nextAdditionalContextItemID(key)
		next[key] = stored
		if stored.toolOutput {
			items = append(items, additionalContextToolOutputModelItem(stored.name, stored.value))
			persisted = append(persisted, additionalContextToolOutput{
				key: key, name: stored.name, value: stored.value,
				kind: stored.kind, itemID: stored.itemID, toolOutput: true,
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
		persisted = append(persisted, additionalContextToolOutput{
			key: key, value: stored.value, kind: stored.kind,
			itemID: stored.itemID, rendered: rendered.Content,
		})
	}
	return developer, items, persisted
}

// hookEncryptedContextInputItems renders the ciphertext a synchronous MCP
// SessionStart hook returned as standalone function_call_output items carrying
// the originating tool name and namespace (Rust #63474). The payload stays in
// the encrypted_content content item so it never becomes model-visible text.
func hookEncryptedContextInputItems(contexts []turn.HookEncryptedContext) []any {
	if len(contexts) == 0 {
		return nil
	}
	items := make([]any, 0, len(contexts))
	for _, context := range contexts {
		if strings.TrimSpace(context.Content) == "" {
			continue
		}
		item := map[string]any{
			"type": "function_call_output",
			"name": strings.TrimSpace(context.Name),
			"output": []map[string]any{{
				"type":              "encrypted_content",
				"encrypted_content": context.Content,
			}},
		}
		if namespace := strings.TrimSpace(context.Namespace); namespace != "" {
			item["namespace"] = namespace
		}
		items = append(items, item)
	}
	return items
}

// hookEncryptedContextSessionItems records the ciphertext a synchronous MCP
// SessionStart hook returned so a resumed thread replays the same encrypted
// content item once. The payload lives in `content_items`, which
// `toolOutputInputItem` replays verbatim, and Text stays empty so no replay path
// can turn the ciphertext into plaintext tool output (Rust #63474).
func hookEncryptedContextSessionItems(turnID string, contexts []turn.HookEncryptedContext, createdAt time.Time) []session.Item {
	if len(contexts) == 0 {
		return nil
	}
	items := make([]session.Item, 0, len(contexts))
	for index, context := range contexts {
		if strings.TrimSpace(context.Content) == "" {
			continue
		}
		name := strings.TrimSpace(context.Name)
		namespace := strings.TrimSpace(context.Namespace)
		contentItems := []map[string]any{{
			"type":              "encrypted_content",
			"encrypted_content": context.Content,
		}}
		data := map[string]any{
			"name":          name,
			"output":        "",
			"content_items": contentItems,
			"harness_metadata": map[string]any{
				"hook_encrypted_context": true,
			},
		}
		if namespace != "" {
			data["namespace"] = namespace
		}
		payload, _ := json.Marshal(hookEncryptedContextModelItem(name, namespace, contentItems))
		items = append(items, session.Item{
			ID:        "hook-encrypted-context-" + safeIdentifier(turnID) + "-" + strconv.Itoa(index+1),
			Type:      "function_call_output",
			Name:      name,
			Namespace: namespace,
			CreatedAt: createdAt,
			Data:      data,
			Metadata:  appTurnMetadata(turnID, nil),
			Raw:       payload,
		})
	}
	return items
}

// hookEncryptedContextModelItem is the request item the persisted session item
// replays as: a standalone tool output whose payload is an encrypted content
// item.
func hookEncryptedContextModelItem(name string, namespace string, contentItems []map[string]any) map[string]any {
	item := map[string]any{
		"type":   "function_call_output",
		"name":   name,
		"output": contentItems,
	}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return item
}

func additionalContextToolOutputModelItem(name string, value string) map[string]any {
	return map[string]any{
		"type":      "function_call_output",
		"name":      name,
		"namespace": additionalContextToolNamespace,
		"output":    value,
	}
}

// additionalContextSessionItems records client-supplied context in
// conversation history. Tool outputs keep harness provenance so the item
// replayed to the acting model keeps exactly the payload it saw; untrusted
// message context is recorded as a plain user message (Rust #61390 keeps both
// forms addressable by response-item ID across compaction).
func additionalContextSessionItems(turnID string, outputs []additionalContextToolOutput, createdAt time.Time) []session.Item {
	if len(outputs) == 0 {
		return nil
	}
	harnessMetadata, _ := json.Marshal(map[string]any{"additional_context_tool_output": true})
	items := make([]session.Item, 0, len(outputs))
	for index, output := range outputs {
		id := firstNonEmpty(output.itemID, "additional-context-"+safeIdentifier(turnID)+"-"+safeIdentifier(output.key)+"-"+strconv.Itoa(index+1))
		if !output.toolOutput {
			if strings.TrimSpace(output.rendered) == "" {
				continue
			}
			content := []map[string]any{{"type": "input_text", "text": output.rendered}}
			payload, _ := json.Marshal(map[string]any{
				"type":    "message",
				"role":    "user",
				"content": content,
			})
			items = append(items, session.Item{
				ID:        id,
				Type:      "message",
				Role:      "user",
				Text:      output.rendered,
				CreatedAt: createdAt,
				Data: map[string]any{
					"role":    "user",
					"content": content,
				},
				Metadata: appTurnMetadata(turnID, nil),
				Raw:      payload,
			})
			continue
		}
		payload, _ := json.Marshal(additionalContextToolOutputModelItem(output.name, output.value))
		items = append(items, session.Item{
			ID:        id,
			Type:      "function_call_output",
			Name:      output.name,
			Namespace: additionalContextToolNamespace,
			Text:      output.value,
			CreatedAt: createdAt,
			Data: map[string]any{
				"name":             output.name,
				"namespace":        additionalContextToolNamespace,
				"output":           output.value,
				"harness_metadata": json.RawMessage(harnessMetadata),
			},
			Metadata: appTurnMetadata(turnID, nil),
			Raw:      payload,
		})
	}
	return items
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

// nextAdditionalContextItemID returns a unique response-item ID for one emitted
// additional-context entry (Rust ItemIdGenerator).
func (r *RuntimeRouter) nextAdditionalContextItemID(key string) string {
	if r == nil {
		return "additional-context-" + safeIdentifier(key)
	}
	sequence := r.additionalContextItemSeq.Add(1)
	return "additional-context-" + safeIdentifier(key) + "-" + strconv.FormatUint(sequence, 10)
}

// restoreCompactedAdditionalContext reinstalls active additional-context items
// after a compaction replacement (Rust #61390
// AdditionalContextStore::restore_compacted_items). Items that the replaced
// prefix carried are refreshed in place when the replacement kept them,
// otherwise they are returned for insertion before the preserved user prompt.
// Instruction-delivered fragments never enter history, so they are reported for
// re-emission on the next turn.
func (r *RuntimeRouter) restoreCompactedAdditionalContext(threadID string, replaced []compact.Item, suffix []compact.Item, retained []compact.Item) ([]compact.Item, []string) {
	if r == nil {
		return nil, nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, nil
	}
	r.additionalContextMu.Lock()
	stored := make(map[string]storedAdditionalContext, len(r.additionalContext[threadID]))
	for key, value := range r.additionalContext[threadID] {
		stored[key] = value
	}
	r.additionalContextMu.Unlock()
	if len(stored) == 0 {
		return nil, nil
	}
	active := make(map[string]string, len(stored))
	for key, value := range stored {
		if value.itemID != "" {
			active[value.itemID] = key
		}
	}
	retainedAt := make(map[string]int, len(retained))
	for index := range retained {
		if id := strings.TrimSpace(retained[index].ID); id != "" {
			if _, ok := active[id]; ok {
				retainedAt[id] = index
			}
		}
	}
	restored := []compact.Item{}
	for index := range replaced {
		id := strings.TrimSpace(replaced[index].ID)
		if _, ok := active[id]; !ok {
			continue
		}
		delete(active, id)
		if position, found := retainedAt[id]; found {
			// Generic retention may have shortened the active value. Keep its
			// position and checkpoint metadata, with the original bounded
			// payload.
			retained[position] = replaceAdditionalContextPayload(retained[position], replaced[index])
			continue
		}
		restored = append(restored, cloneAdditionalContextCompactItem(replaced[index]))
	}
	surviving := make(map[string]struct{}, len(suffix)+len(retained))
	for index := range suffix {
		surviving[strings.TrimSpace(suffix[index].ID)] = struct{}{}
	}
	for index := range retained {
		surviving[strings.TrimSpace(retained[index].ID)] = struct{}{}
	}
	for index := range restored {
		surviving[strings.TrimSpace(restored[index].ID)] = struct{}{}
	}
	replay := []string{}
	for _, key := range sortedStoredAdditionalContextKeys(stored) {
		value := stored[key]
		// Only instruction-delivered fragments need re-emission: untrusted
		// message context is recorded as a session item, and a queued value
		// arrives with its input.
		if value.toolOutput || value.kind != turn.AdditionalContextApplication || value.itemID == "" {
			continue
		}
		if _, ok := surviving[value.itemID]; ok {
			continue
		}
		replay = append(replay, key)
	}
	return restored, replay
}

func replaceAdditionalContextPayload(target compact.Item, source compact.Item) compact.Item {
	replacement := cloneAdditionalContextCompactItem(source)
	replacement.ID = target.ID
	replacement.Created = target.Created
	replacement.HistorySequence = target.HistorySequence
	replacement.SampledThroughSequence = target.SampledThroughSequence
	return replacement
}

func cloneAdditionalContextCompactItem(item compact.Item) compact.Item {
	cloned := item
	cloned.Content = append([]compact.ContentPart(nil), item.Content...)
	cloned.Data = cloneAnyMapForRouter(item.Data)
	cloned.Raw = append(json.RawMessage(nil), item.Raw...)
	return cloned
}

// forgetAdditionalContextKeys drops the deduplication markers for values whose
// instruction-delivered fragments must reach the model again after compaction.
func (r *RuntimeRouter) forgetAdditionalContextKeys(threadID string, keys []string) {
	if r == nil || len(keys) == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.additionalContextMu.Lock()
	defer r.additionalContextMu.Unlock()
	values := r.additionalContext[threadID]
	for _, key := range keys {
		delete(values, key)
	}
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

// recordAdditionalContextMetrics mirrors Rust AdditionalContextStore::record_metrics
// (#58144): one count sample and before/after byte histograms per submitted
// entry, tagged by kind, truncation status, and an explicitly supplied name.
func recordAdditionalContextMetrics(entry turn.AdditionalContextEntry) {
	bounded, originalTokens := utils.TruncateMiddleWithTokenBudget(entry.Value, additionalContextValueTokenBudget)
	kind := "application"
	if entry.Kind == turn.AdditionalContextUntrusted {
		kind = "untrusted"
	}
	truncated := "false"
	if originalTokens != nil {
		truncated = "true"
	}
	tags := map[string]string{"kind": kind, "truncated": truncated}
	if entry.Name != nil {
		tags["name"] = *entry.Name
	}
	metrics.Counter(telemetry.AdditionalContextCountMetric, 1, tags)
	for _, stage := range []struct {
		name  string
		bytes int
	}{
		{"before", len(entry.Value)},
		{"after", len(bounded)},
	} {
		sizeTags := make(map[string]string, len(tags)+1)
		for key, value := range tags {
			sizeTags[key] = value
		}
		sizeTags["stage"] = stage.name
		metrics.HistogramWithBounds(
			telemetry.AdditionalContextValueBytesMetric,
			stage.bytes,
			telemetry.AdditionalContextValueBytesBuckets,
			sizeTags,
		)
	}
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
