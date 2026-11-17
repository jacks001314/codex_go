package session

import (
	"log/slog"
	"strings"

	"codex_go/attachmentstore"
	"codex_go/audioutil"
	"codex_go/utils"
)

const (
	toolAudioBudgetApplied = "tool_audio_budget_applied"
)

const (
	toolAudioRejectedPlaceholder    = "audio content omitted because the attachment store rejected the clip"
	toolAudioStoreFailedPlaceholder = "audio content omitted because it could not be stored"
)

// ItemHasInlineToolAudio reports whether item is a function or custom tool
// output carrying at least one inline audio content item. It mirrors the fresh
// result predicate in Rust `file_audio::prepare_new_items`.
func ItemHasInlineToolAudio(item *Item) bool {
	if item == nil || !isFunctionOrCustomToolOutput(item) {
		return false
	}
	parts, ok := historyContentItemsFromValue(item.Data["content_items"])
	if !ok {
		return false
	}
	for _, part := range parts {
		if part.Kind == utils.ContentInputAudio && strings.TrimSpace(part.FileID) == "" {
			return true
		}
	}
	return false
}

func isFunctionOrCustomToolOutput(item *Item) bool {
	if item == nil {
		return false
	}
	switch strings.TrimSpace(item.Type) {
	case "function_call_output", "custom_tool_call_output":
		return true
	case "tool_output":
		return stringValue(item.Metadata, "payloadKind") != "tool_search"
	default:
		return false
	}
}

// PrepareInlineToolAudio canonicalizes fresh inline tool audio and replaces
// clips that cannot be decoded with their placeholder. It mirrors Rust
// `codex_utils_audio::prepare_response_items` for function and custom tool
// outputs, which runs before history truncation whether or not the
// `file_backed_tool_audio` feature is enabled. An invalid clip therefore costs
// only its placeholder text in the history budget.
func PrepareInlineToolAudio(item *Item) {
	if item == nil || !isFunctionOrCustomToolOutput(item) {
		return
	}
	parts, ok := historyContentItemsFromValue(item.Data["content_items"])
	if !ok || len(parts) == 0 {
		return
	}
	changed := false
	for i := range parts {
		part := &parts[i]
		if part.Kind != utils.ContentInputAudio || strings.TrimSpace(part.FileID) != "" {
			continue
		}
		canonical, err := audioutil.PrepareAudioURL(strings.TrimSpace(part.AudioURL))
		if err != nil {
			parts[i] = utils.FunctionCallOutputContentItem{
				Kind: utils.ContentInputText,
				Text: audioutil.Placeholder(err),
			}
			changed = true
			continue
		}
		if canonical != part.AudioURL {
			part.AudioURL = canonical
			changed = true
		}
	}
	if changed {
		item.Data["content_items"] = historyContentItemsValue(parts)
	}
}

// PrepareFileBackedToolAudio uploads fresh inline tool audio through the
// attachment store before persistence, mirroring Rust #61394
// `file_audio::prepare_new_items`. Items are mutated in place. The returned
// file IDs are the clips now resident behind the store, for duration lookup.
func PrepareFileBackedToolAudio(items []Item, store attachmentstore.Store, threadID string, ephemeral bool, defaultTokenLimit int) []string {
	if len(items) == 0 || store == nil {
		return nil
	}
	fileIDs := make([]string, 0, 2)
	seen := map[string]bool{}
	for i := range items {
		item := &items[i]
		if !item.FileBackedToolAudioPending {
			continue
		}
		item.FileBackedToolAudioPending = false
		limit, explicit := historyTruncationTokenLimit(item)
		if !explicit {
			limit = defaultTokenLimit
			if itemPolicy := item.ToolAudioHistoryPolicy; itemPolicy != nil {
				allowance := itemPolicy.WithSerializationAllowance()
				if policyLimit := (&allowance).TokenBudget(); policyLimit > 0 {
					limit = policyLimit
				}
			}
			if limit < 0 {
				limit = 0
			}
		}
		if !ItemHasInlineToolAudio(item) {
			// Validation replaced every clip with its placeholder, so history
			// still bounds the text-only result with the recorded limit. Rust
			// marks the audio-aware budget as applied even here.
			applyPreparedToolAudioLimit(item, limit, explicit, true)
			ApplyHistoryTruncationLimit(item, limit)
			continue
		}
		parts, ok := historyContentItemsFromValue(item.Data["content_items"])
		if !ok {
			applyPreparedToolAudioLimit(item, limit, explicit, false)
			ApplyHistoryTruncationLimit(item, limit)
			continue
		}
		producerBounded := item.ProducerBoundedToolAudio
		measured := measureToolAudioParts(parts)
		admitted := utils.TruncateMeasuredFunctionOutputItems(measured, utils.TokensPolicy(limit))
		repairLimit := toolAudioRepairLimit(admitted, limit, producerBounded && !explicit)
		replacedWithText := false
		for j := range admitted {
			part := &admitted[j].Item
			if part.Kind != utils.ContentInputAudio || strings.TrimSpace(part.FileID) != "" || strings.TrimSpace(part.AudioURL) == "" {
				continue
			}
			*part = storeToolAudio(store, threadID, ephemeral, part.AudioURL)
			if part.Kind == utils.ContentInputText {
				admitted[j].AudioTokens = 0
				replacedWithText = true
				continue
			}
			if id := strings.TrimSpace(part.FileID); id != "" && !seen[id] {
				seen[id] = true
				fileIDs = append(fileIDs, id)
			}
		}
		if replacedWithText {
			admitted = utils.TruncateMeasuredFunctionOutputItems(admitted, utils.TokensPolicy(repairLimit))
		}
		output := make([]utils.FunctionCallOutputContentItem, len(admitted))
		for j := range admitted {
			output[j] = admitted[j].Item
		}
		item.Data["content_items"] = historyContentItemsValue(output)
		applyPreparedToolAudioLimit(item, limit, explicit, true)
	}
	return fileIDs
}

// applyPreparedToolAudioLimit records the resolved history limit Rust's record
// step would add and the fact that the audio-aware budget already ran. The
// second flag makes later history builds skip a redundant truncation.
func applyPreparedToolAudioLimit(item *Item, limit int, explicit bool, budgetApplied bool) {
	if item == nil {
		return
	}
	fields := map[string]any{}
	if !explicit && !budgetApplied {
		// Rust's record step only stamps a limit when it had to truncate; an
		// applied audio-aware budget already bounded the body.
		fields[historyTruncationTokenLimitKey] = limit
	}
	if budgetApplied {
		fields[toolAudioBudgetApplied] = true
	}
	if len(fields) > 0 {
		mergeHistoryHarnessMetadata(item, fields)
	}
	item.ToolAudioHistoryPolicy = nil
	item.ProducerBoundedToolAudio = false
}

func measureToolAudioParts(parts []utils.FunctionCallOutputContentItem) []utils.MeasuredFunctionCallOutputContentItem {
	measured := make([]utils.MeasuredFunctionCallOutputContentItem, len(parts))
	for i := range parts {
		charge := 0
		if parts[i].Kind == utils.ContentInputAudio {
			switch {
			case strings.TrimSpace(parts[i].FileID) != "":
				charge = audioutil.EstimateFileAudioTokens(nil)
			case strings.TrimSpace(parts[i].AudioURL) == "":
				parts[i] = utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: audioutil.Placeholder(audioutil.ErrInvalidDataURL)}
			default:
				canonical, tokens, err := audioutil.PrepareAndMeasureInlineAudio(parts[i].AudioURL)
				if err != nil {
					parts[i] = utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: audioutil.Placeholder(err)}
				} else {
					parts[i].AudioURL = canonical
					charge = tokens
				}
			}
		}
		measured[i] = utils.MeasuredFunctionCallOutputContentItem{Item: parts[i], AudioTokens: charge}
	}
	return measured
}

func toolAudioRepairLimit(admitted []utils.MeasuredFunctionCallOutputContentItem, limit int, preserveProducerLimit bool) int {
	if !preserveProducerLimit {
		return limit
	}
	total := 0
	for _, part := range admitted {
		cost := part.AudioTokens
		if part.Item.Kind == utils.ContentInputText {
			cost = utils.ApproxTokenCount(part.Item.Text)
		}
		total += cost
		if total >= limit {
			return limit
		}
	}
	return total
}

func storeToolAudio(store attachmentstore.Store, threadID string, ephemeral bool, inlineURL string) utils.FunctionCallOutputContentItem {
	mime, data, err := audioutil.DecodeAudioURL(inlineURL)
	if err != nil {
		return utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: audioutil.Placeholder(err)}
	}
	result, err := store.Upload(attachmentstore.UploadRequest{
		ThreadID:  threadID,
		Ephemeral: ephemeral,
		Content:   attachmentstore.ToolAudioUploadContent(mime, data),
	})
	if err == nil {
		if fileID := strings.TrimSpace(result.FileID); fileID != "" {
			return utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio, FileID: fileID}
		}
		return utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio, AudioURL: audioutil.AudioDataURL(mime, result.InlineBytes)}
	}
	kind, _ := attachmentstore.KindOf(err)
	switch kind {
	case attachmentstore.ErrorKindBackend:
		slog.Warn("tool audio upload failed; using inline audio", "kind", kind)
		return utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio, AudioURL: audioutil.AudioDataURL(mime, data)}
	case attachmentstore.ErrorKindInvalidAttachment:
		return utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: toolAudioRejectedPlaceholder}
	default:
		slog.Warn("could not store tool audio attachment")
		return utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: toolAudioStoreFailedPlaceholder}
	}
}

// ItemAudioFileIDs returns uploaded audio file IDs carried by session items,
// including file-backed tool audio in structured output content.
func ItemAudioFileIDs(items []Item) []string {
	ids := make([]string, 0, 2)
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for i := range items {
		item := &items[i]
		for _, part := range item.Content {
			kind := strings.ToLower(strings.TrimSpace(part.Type))
			if (kind == "input_audio" || kind == "audio") && strings.TrimSpace(part.AudioURL) == "" {
				add(part.FileID)
			}
		}
		parts, ok := historyContentItemsFromValue(item.Data["content_items"])
		if !ok {
			continue
		}
		for _, part := range parts {
			if part.Kind == utils.ContentInputAudio && strings.TrimSpace(part.AudioURL) == "" {
				add(part.FileID)
			}
		}
	}
	return ids
}
