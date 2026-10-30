package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex_go/compact"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// compactionReplacementContext is the current step's full model-visible context
// and the comparison baselines persisted with the replacement history. The
// compactor may run with a different model; this context always belongs to the
// selected replacement model (Rust #51117).
type compactionReplacementContext struct {
	Items              []compact.Item
	RunConfig          *appTurnRunConfig
	TurnContext        rollout.TurnContextRecord
	TurnContextPayload json.RawMessage
	WorldState         json.RawMessage
	Record             *session.Record
}

func (r *RuntimeRouter) buildCompactionReplacementContext(ctx context.Context, request *runtimeCompactRequest, record *session.Record) (*compactionReplacementContext, error) {
	if r == nil || request == nil {
		return nil, nil
	}
	params := cloneTurnStartParams(request.ReplacementParams)
	if params == nil {
		params = &turn.TurnStartParams{}
	}
	params.ThreadID = firstNonEmpty(strings.TrimSpace(params.ThreadID), strings.TrimSpace(request.ThreadID))
	if strings.TrimSpace(params.CWD) == "" && record != nil {
		params.CWD = record.Metadata.CWD
	}
	if params.Originator == "" {
		params.Originator = "compact"
	}
	replacementModel := firstNonEmpty(
		strings.TrimSpace(request.ReplacementModel),
		strings.TrimSpace(request.WindowModel),
		strings.TrimSpace(params.Model),
	)
	if record != nil {
		replacementModel = firstNonEmpty(replacementModel, record.Metadata.Model)
	}
	if replacementModel != "" {
		params.Model = replacementModel
	}
	// The replacement baseline renders the thread's recorded base instructions,
	// the same effective value a regular turn start resolves (Rust #44675).
	if params.BaseInstructions == nil && record != nil &&
		strings.TrimSpace(record.Metadata.BaseInstructions) != "" &&
		!r.baseInstructionsAreModelGenerated(record) {
		params.BaseInstructions = cloneString(&record.Metadata.BaseInstructions)
	}
	if request.ReplacementCyberAccessProgram != nil {
		program := strings.TrimSpace(*request.ReplacementCyberAccessProgram)
		params.CoreCyberAccessProgram = program
		if program == "" {
			params.CyberAccessProgram = nil
		} else {
			value := turn.CyberAccessProgram(program)
			params.CyberAccessProgram = &value
		}
	}
	startedAt := time.Now().UTC()
	runConfig, err := r.appTurnConfigWithOptions(ctx, request.ThreadID, request.TurnID, "", params, startedAt.UnixMilli(), nil, false)
	if err != nil {
		return nil, err
	}
	// Context providers persist their comparison snapshots on the thread. Reload
	// so the baseline written with the replacement matches the context just
	// rendered from the same step.
	latest, err := r.threadRecord(session.ThreadID(request.ThreadID), true, true)
	if err != nil {
		return nil, err
	}
	// Rust #51117 installs the full initial context in the replacement history:
	// the window's tool catalog opens it, the refreshed base-instruction
	// developer message follows, and the rest of the model-visible context
	// trails behind. An existing window's incremental diff has nothing to
	// prepend, so its recorded declaration prefix supplies the catalog.
	contextRecord := latest
	if contextRecord == nil {
		contextRecord = record
	}
	initialContextItems := orderCompactionInitialContextItems(runConfig.InitialContextItems, runConfig.Instructions, contextRecord)
	items, err := compactItemsFromInputItems(initialContextItems, startedAt)
	if err != nil {
		return nil, err
	}
	items = stampCompactionDeclarationKinds(items, runConfig.Instructions)
	turnContext, ok := r.turnContextRecordForRunConfig(request.TurnID, request.TurnID, cwdForReplacementRecord(latest, record), runConfig)
	if !ok {
		return nil, fmt.Errorf("compaction replacement context has no model")
	}
	payload, err := json.Marshal(turnContext)
	if err != nil {
		return nil, err
	}
	worldState := json.RawMessage(nil)
	if latest != nil {
		worldState = append(json.RawMessage(nil), latest.Metadata.WorldState...)
	}
	return &compactionReplacementContext{
		Items:              items,
		RunConfig:          runConfig,
		TurnContext:        turnContext,
		TurnContextPayload: payload,
		WorldState:         worldState,
		Record:             latest,
	}, nil
}

func cwdForReplacementRecord(latest *session.Record, fallback *session.Record) string {
	if latest != nil && strings.TrimSpace(latest.Metadata.CWD) != "" {
		return latest.Metadata.CWD
	}
	if fallback != nil {
		return fallback.Metadata.CWD
	}
	return ""
}

func compactItemsFromInputItems(items []any, now time.Time) ([]compact.Item, error) {
	out := make([]compact.Item, 0, len(items))
	for index, input := range items {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		itemType := firstNonEmpty(stringFromMap(payload, "type"), "raw_response_item")
		itemID := firstNonEmpty(stringFromMap(payload, "id"), fmt.Sprintf("compaction-context-%d", index+1))
		text := firstNonEmpty(stringFromMap(payload, "text"), contentText(payload["content"]))
		out = append(out, compact.Item{
			ID:      itemID,
			Type:    itemType,
			Role:    stringFromMap(payload, "role"),
			Text:    text,
			Kind:    stringFromMap(payload, "kind"),
			Created: now,
			Data:    payload,
			Raw:     append(json.RawMessage(nil), raw...),
		})
	}
	return out, nil
}

func ensureInitialContextInstructions(items []any, instructions string) []any {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" || inputItemsContainText(items, instructions) {
		return items
	}
	item := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": instructions},
		},
	}
	insertAt := 0
	for insertAt < len(items) && inputItemType(items[insertAt]) == "additional_tools" {
		insertAt++
	}
	out := make([]any, 0, len(items)+1)
	out = append(out, items[:insertAt]...)
	out = append(out, item)
	out = append(out, items[insertAt:]...)
	return out
}

// orderCompactionInitialContextItems places the replacement window's
// declarations first: the tool catalog, then the base-instruction developer
// message carrying the replacement step's refreshed instructions, then the
// remaining model-visible context (world state, skills, environment, hook
// context). Rust `insert_initial_context_before_last_real_user_or_summary`
// keeps this block ahead of the compaction summary (#51117).
func orderCompactionInitialContextItems(items []any, instructions string, record *session.Record) []any {
	instructions = strings.TrimSpace(instructions)
	catalogIndex := -1
	if len(items) > 0 && inputItemType(items[0]) == "additional_tools" {
		catalogIndex = 0
	}
	if catalogIndex < 0 && record != nil {
		if catalog := compactionPrefixCatalogInputItem(record); catalog != nil {
			items = append([]any{catalog}, items...)
			catalogIndex = 0
		}
	}
	if catalogIndex < 0 {
		for i := range items {
			if inputItemType(items[i]) == "additional_tools" {
				catalogIndex = i
				break
			}
		}
	}
	instructionIndex := -1
	if instructions != "" {
		for i := range items {
			if i == catalogIndex {
				continue
			}
			if inputItemDeveloperText(items[i], instructions) {
				instructionIndex = i
				break
			}
		}
	}
	out := make([]any, 0, len(items)+1)
	if catalogIndex >= 0 {
		out = append(out, items[catalogIndex])
	}
	if instructions != "" {
		if instructionIndex >= 0 {
			out = append(out, items[instructionIndex])
		} else {
			out = append(out, compactionInstructionsInputItem(instructions))
		}
	}
	for i := range items {
		if i == catalogIndex || i == instructionIndex {
			continue
		}
		if instructions != "" && inputItemDeveloperText(items[i], instructions) {
			continue
		}
		out = append(out, items[i])
	}
	return out
}

// compactionPrefixCatalogInputItem recovers the tool catalog the replaced
// window recorded, so an unchanged incremental window still opens its
// replacement history with the declarations it already sent.
func compactionPrefixCatalogInputItem(record *session.Record) any {
	for _, item := range incrementalDeclarationPrefixItems(record.Items) {
		if strings.TrimSpace(item.Type) != "additional_tools" {
			continue
		}
		if object := incrementalDeclarationItemObject(&item); object != nil {
			return object
		}
	}
	return nil
}

func compactionInstructionsInputItem(instructions string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": instructions},
		},
	}
}

func inputItemType(value any) string {
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return ""
	}
	return strings.TrimSpace(stringFromAny(object["type"]))
}

func inputItemDeveloperText(value any, want string) bool {
	object, ok := value.(map[string]any)
	if !ok || object == nil || want == "" {
		return false
	}
	if strings.TrimSpace(stringFromAny(object["role"])) != "developer" {
		return false
	}
	text := firstNonEmpty(stringFromMap(object, "text"), contentText(object["content"]))
	return strings.TrimSpace(text) == want
}

// stampCompactionDeclarationKinds marks the catalog and base-instruction
// entries as window declarations so persisting the replacement history keeps
// the same harness metadata a live window records (Rust #51188).
func stampCompactionDeclarationKinds(items []compact.Item, instructions string) []compact.Item {
	instructions = strings.TrimSpace(instructions)
	for i := range items {
		switch strings.TrimSpace(items[i].Type) {
		case "additional_tools":
			items[i].Kind = incrementalToolCatalogKind
		case "message":
			if instructions != "" &&
				strings.TrimSpace(items[i].Role) == "developer" &&
				strings.TrimSpace(items[i].Text) == instructions {
				items[i].Kind = incrementalToolCatalogKind
			}
		}
	}
	return items
}

func inputItemsContainText(items []any, want string) bool {
	for _, item := range items {
		if inputItemContainsText(item, want) {
			return true
		}
	}
	return false
}

func inputItemContainsText(value any, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if text, ok := typed["text"].(string); ok && strings.TrimSpace(text) == want {
			return true
		}
		if content, ok := typed["content"].([]any); ok {
			return inputItemContainsText(content, want)
		}
	case []any:
		for _, item := range typed {
			if inputItemContainsText(item, want) {
				return true
			}
		}
	}
	return false
}
