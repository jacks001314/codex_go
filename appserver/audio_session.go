package appserver

import (
	"context"
	"errors"
	"strings"

	"codex_go/attachmentstore"
	"codex_go/audiofiles"
	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
)

// Thread metadata keys for the durable uploaded-audio facts (Rust #59234).
const (
	rejectedAudioMetadataKey = "rejected_audio_file_ids"
	acceptedAudioMetadataKey = "accepted_file_audio"
)

// modelSupportsAudioInput reports whether the selected model declares audio
// input (Rust ModelInfo::input_modalities contains InputModality::Audio).
func modelSupportsAudioInput(info *model.ModelInfo) bool {
	if info == nil {
		return false
	}
	for _, modality := range info.InputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "audio") {
			return true
		}
	}
	return false
}

// audioInputSupportedForThread reports whether the thread's selected model
// declares audio input.
func (r *RuntimeRouter) audioInputSupportedForThread(threadID string) bool {
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return false
	}
	return modelSupportsAudioInput(r.modelInfoForRuntime(strings.TrimSpace(record.Metadata.Model)))
}

// audioDurationsFor returns the runtime-only duration cache for one thread. A
// cold resume starts empty and re-resolves measured metadata (Rust #59268).
func (r *RuntimeRouter) audioDurationsFor(threadID string) *audiofiles.Durations {
	if r == nil {
		return nil
	}
	key := strings.TrimSpace(threadID)
	r.audioStateMu.Lock()
	defer r.audioStateMu.Unlock()
	if r.audioDurations == nil {
		r.audioDurations = map[string]*audiofiles.Durations{}
	}
	cache := r.audioDurations[key]
	if cache == nil {
		cache = audiofiles.NewDurations()
		r.audioDurations[key] = cache
	}
	return cache
}

func (r *RuntimeRouter) attachmentStoreForAudio() attachmentstore.Store {
	if r == nil || r.threads == nil {
		return nil
	}
	return r.threads.AttachmentStore()
}

// resolveAudioDurations records measured durations for the uploaded audio in a
// prompt. Lookup is best effort and bounded; an unmeasured file keeps the
// conservative budget.
func (r *RuntimeRouter) resolveAudioDurations(threadID string, items []any) {
	ids := audiofiles.FileAudioIDs(items)
	if len(ids) == 0 {
		return
	}
	audiofiles.Resolve(context.Background(), r.attachmentStoreForAudio(), r.audioDurationsFor(threadID), ids)
}

// resolveAudioDurationsFromSessionItems covers replayed history on resume.
func (r *RuntimeRouter) resolveAudioDurationsFromSessionItems(threadID string, items []session.Item) {
	ids := sessionItemAudioFileIDs(items)
	if len(ids) == 0 {
		return
	}
	audiofiles.Resolve(context.Background(), r.attachmentStoreForAudio(), r.audioDurationsFor(threadID), ids)
}

func sessionItemAudioFileIDs(items []session.Item) []string {
	ids := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, item := range items {
		for _, part := range item.Content {
			kind := strings.ToLower(strings.TrimSpace(part.Type))
			if kind != "input_audio" && kind != "audio" {
				continue
			}
			id := strings.TrimSpace(part.FileID)
			if id == "" || strings.TrimSpace(part.AudioURL) != "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// compactFileAudioDurations renders the cache for the compaction budget.
func (r *RuntimeRouter) compactFileAudioDurations(threadID string) compact.FileAudioDurations {
	if r == nil {
		return nil
	}
	snapshot := r.audioDurationsFor(threadID).Snapshot()
	if len(snapshot) == 0 {
		return nil
	}
	return compact.FileAudioDurations(snapshot)
}

func (r *RuntimeRouter) threadExtraMetadata(threadID string) map[string]any {
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	return record.Metadata.Extra
}

// rejectedAudioFileIDs restores the durable rejection facts.
func (r *RuntimeRouter) rejectedAudioFileIDs(threadID string) audiofiles.RejectedSet {
	extra := r.threadExtraMetadata(threadID)
	if extra == nil {
		return nil
	}
	return audiofiles.ParseRejected(extra[rejectedAudioMetadataKey])
}

// recordRejectedAudioFileIDs persists newly proven rejections. File IDs are
// immutable, so a fact outlives compaction and cold resume.
func (r *RuntimeRouter) recordRejectedAudioFileIDs(threadID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	rejected := r.rejectedAudioFileIDs(threadID)
	changed := false
	if rejected == nil {
		rejected = audiofiles.RejectedSet{}
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || rejected.Has(id) {
			continue
		}
		rejected[id] = struct{}{}
		changed = true
	}
	if !changed {
		return
	}
	_, _ = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{
		Extra: map[string]any{rejectedAudioMetadataKey: rejected.List()},
	}, true)
}

// acceptedFileAudio restores the decoding evidence for this thread.
func (r *RuntimeRouter) acceptedFileAudio(threadID string) *audiofiles.Accepted {
	extra := r.threadExtraMetadata(threadID)
	if extra == nil {
		return nil
	}
	return audiofiles.ParseAccepted(extra[acceptedAudioMetadataKey])
}

func (r *RuntimeRouter) recordAcceptedFileAudio(threadID string, accepted *audiofiles.Accepted) {
	if accepted == nil {
		return
	}
	_, _ = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{
		Extra: map[string]any{acceptedAudioMetadataKey: accepted.Map()},
	}, true)
}

// projectRejectedAudioForThread omits rejected uploaded audio from a request
// without rewriting retained history (Rust project_file_audio).
func (r *RuntimeRouter) projectRejectedAudioForThread(threadID string, items []any) []any {
	rejected := r.rejectedAudioFileIDs(threadID)
	if len(rejected) == 0 {
		return items
	}
	return audiofiles.ProjectFileAudio(items, rejected)
}

// attributeRejectedAudio persists the exact file identities a terminal
// provider failure proves rejected and adds the removal guidance Rust appends
// to audio errors. Ambiguous failures keep every attachment.
func (r *RuntimeRouter) attributeRejectedAudio(ctx context.Context, threadID string, items []any, err error) error {
	if err == nil {
		return nil
	}
	if !audiofiles.IsInvalidAudioInput(err) && !audiofiles.IsUnknownFileError(err) {
		return err
	}
	submitted := audiofiles.SubmittedFromRequest(items, r.acceptedFileAudio(threadID), nil)
	ids := submitted.RejectedFileIDs(ctx, r.attachmentStoreForAudio(), err)
	if len(ids) > 0 {
		r.recordRejectedAudioFileIDs(threadID, ids)
	}
	if audiofiles.IsInvalidAudioInput(err) || len(ids) > 0 {
		return withAudioRemovalGuidance(err)
	}
	return err
}

// withAudioRemovalGuidance mirrors Rust #59234: audio failures tell the user
// how to continue the conversation.
func withAudioRemovalGuidance(err error) error {
	var apiErr *codexapi.APIError
	if errors.As(err, &apiErr) && apiErr != nil {
		clone := *apiErr
		if !strings.HasSuffix(clone.Message, audiofiles.RemovalGuidance) {
			clone.Message += audiofiles.RemovalGuidance
		}
		return &clone
	}
	return err
}

// compactHistoryPromptItems renders compacted history in the prompt wire shape
// so the audio attribution rules apply to a compaction request too.
func compactHistoryPromptItems(history []compact.Item) []any {
	out := make([]any, 0, len(history))
	for _, item := range history {
		if !strings.EqualFold(strings.TrimSpace(item.Type), "message") {
			out = append(out, map[string]any{"type": item.Type})
			continue
		}
		content := make([]any, 0, len(item.Content))
		for _, part := range item.Content {
			entry := map[string]any{"type": part.Type}
			switch strings.ToLower(strings.TrimSpace(part.Type)) {
			case "input_text", "output_text", "text":
				entry["text"] = part.Text
			case "input_image", "image":
				if part.FileID != "" {
					entry["file_id"] = part.FileID
				}
				if part.ImageURL != "" {
					entry["image_url"] = part.ImageURL
				}
			case "input_audio", "audio":
				if part.FileID != "" {
					entry["file_id"] = part.FileID
				}
				if part.AudioURL != "" {
					entry["audio_url"] = part.AudioURL
				}
			}
			content = append(content, entry)
		}
		out = append(out, map[string]any{"type": "message", "role": item.Role, "content": content})
	}
	return out
}

// projectUnavailableAudioForCompaction removes uploaded audio the store
// authoritatively cannot resolve (Rust #59234 unavailable_file_audio).
func (r *RuntimeRouter) projectUnavailableAudioForCompaction(history []compact.Item) []compact.Item {
	ids := compactItemAudioFileIDs(history)
	if len(ids) == 0 {
		return history
	}
	missing := audiofiles.UnavailableFileAudio(context.Background(), r.attachmentStoreForAudio(), ids)
	if len(missing) == 0 {
		return history
	}
	rejected := audiofiles.RejectedSet{}
	for _, id := range missing {
		rejected[id] = struct{}{}
	}
	return compactHistoryWithoutFileAudio(history, rejected)
}

func compactItemAudioFileIDs(history []compact.Item) []string {
	ids := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, item := range history {
		for _, part := range item.Content {
			kind := strings.ToLower(strings.TrimSpace(part.Type))
			if kind != "input_audio" && kind != "audio" {
				continue
			}
			id := strings.TrimSpace(part.FileID)
			if id == "" || strings.TrimSpace(part.AudioURL) != "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// compactHistoryWithoutFileAudio returns a copy of the history with the given
// uploaded audio parts removed; an emptied user message is dropped.
func compactHistoryWithoutFileAudio(history []compact.Item, rejected audiofiles.RejectedSet) []compact.Item {
	if len(rejected) == 0 {
		return history
	}
	out := make([]compact.Item, 0, len(history))
	for _, item := range history {
		if len(item.Content) == 0 {
			out = append(out, item)
			continue
		}
		content := make([]compact.ContentPart, 0, len(item.Content))
		removed := false
		for _, part := range item.Content {
			kind := strings.ToLower(strings.TrimSpace(part.Type))
			if (kind == "input_audio" || kind == "audio") && strings.TrimSpace(part.AudioURL) == "" &&
				rejected.Has(strings.TrimSpace(part.FileID)) {
				removed = true
				continue
			}
			content = append(content, part)
		}
		if !removed {
			out = append(out, item)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Role), "user") && len(content) == 0 {
			continue
		}
		clone := item
		clone.Content = content
		out = append(out, clone)
	}
	return out
}

// compactionRejectedAudioID mirrors Rust compact_remote_v2_attempt: retry only
// an unambiguously rejected recording from this exact submitted history.
func compactionRejectedAudioID(history []compact.Item, err error) (string, bool) {
	submitted := audiofiles.SubmittedFromRequest(compactHistoryPromptItems(history), nil, nil)
	return submitted.RejectedFileID(err)
}
