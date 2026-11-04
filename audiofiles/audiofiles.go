// Package audiofiles mirrors the Rust uploaded-audio helpers added by #59234
// and #59268: it caches measured file durations for budget estimates, proves
// which recording a provider error rejected, and projects rejected or
// unavailable file references out of future requests without rewriting
// retained user history.
package audiofiles

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/attachmentstore"
	"codex_go/audioutil"
	"codex_go/codexapi"
)

const (
	// resolveTimeout bounds one metadata batch (Rust #59268).
	resolveTimeout = 250 * time.Millisecond
	// resolveConcurrency bounds in-flight metadata lookups.
	resolveConcurrency = 8
	// RemovalGuidance is appended to audio failures so the user knows how to
	// continue the conversation (Rust #59234).
	RemovalGuidance = " Please remove or replace the affected audio attachment and try again."
)

// unknownFileMessage is the provider body for an expired uploaded file.
const unknownFileMessage = "Invalid input: Unknown `file_id` in input content."

// Durations caches measured uploaded-file durations for one thread. Rust keeps
// the same map in SessionState::file_audio_durations; it is never serialized.
type Durations struct {
	mu   sync.RWMutex
	byID map[string]time.Duration
}

// NewDurations returns an empty cache.
func NewDurations() *Durations {
	return &Durations{byID: map[string]time.Duration{}}
}

// Seconds returns the measured duration for a file.
func (d *Durations) Seconds(fileID string) (float64, bool) {
	if d == nil {
		return 0, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	value, ok := d.byID[strings.TrimSpace(fileID)]
	if !ok {
		return 0, false
	}
	return value.Seconds(), true
}

// Has reports whether the file has a measured duration.
func (d *Durations) Has(fileID string) bool {
	_, ok := d.Seconds(fileID)
	return ok
}

// Tokens returns the budget charged for one uploaded recording.
func (d *Durations) Tokens(fileID string) int {
	var duration *float64
	if seconds, ok := d.Seconds(fileID); ok {
		duration = &seconds
	}
	return audioutil.EstimateFileAudioTokens(duration)
}

// Snapshot returns a copy for consumers that need a plain map (the compaction
// pipeline's compact.FileAudioDurations).
func (d *Durations) Snapshot() map[string]float64 {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.byID) == 0 {
		return nil
	}
	out := make(map[string]float64, len(d.byID))
	for id, value := range d.byID {
		out[id] = value.Seconds()
	}
	return out
}

func (d *Durations) record(fileID string, duration time.Duration) {
	if d == nil || duration <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byID == nil {
		d.byID = map[string]time.Duration{}
	}
	d.byID[fileID] = duration
}

// Resolve records measured durations for IDs the cache does not know yet.
// Metadata lookup is best effort and bounded to 250ms for the whole batch with
// up to eight concurrent resolves; completed lookups are kept (Rust #59268).
func Resolve(ctx context.Context, store attachmentstore.Store, durations *Durations, ids []string) {
	if durations == nil || store == nil || len(ids) == 0 {
		return
	}
	pending := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || durations.Has(id) {
			continue
		}
		seen[id] = true
		pending = append(pending, id)
	}
	if len(pending) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limits := ctx.Done()
	_ = limits
	type result struct {
		id       string
		duration time.Duration
		ok       bool
	}
	lookupCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	results := make(chan result, len(pending))
	semaphore := make(chan struct{}, resolveConcurrency)
	var wg sync.WaitGroup
	for _, id := range pending {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-lookupCtx.Done():
				return
			}
			defer func() { <-semaphore }()
			metadata, err := store.Resolve(attachmentstore.ResolveRequest{FileID: id})
			if err != nil {
				return
			}
			audio := metadata.FormatSpecific
			if audio == nil || audio.Kind != "audio" {
				return
			}
			value := audio.AudioOrZero()
			if value.DurationMs == nil || *value.DurationMs == 0 {
				return
			}
			results <- result{id: id, duration: time.Duration(*value.DurationMs) * time.Millisecond, ok: true}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	for {
		select {
		case <-lookupCtx.Done():
			// Keep completed lookups; a missing value stays retryable later.
			for {
				select {
				case res := <-results:
					if res.ok {
						durations.record(res.id, res.duration)
					}
				default:
					return
				}
			}
		case res, ok := <-results:
			if !ok {
				return
			}
			if res.ok {
				durations.record(res.id, res.duration)
			}
		}
	}
}

// Accepted is the evidence that a completed Responses request decoded these
// files for one model and provider (Rust AcceptedFileAudio).
type Accepted struct {
	TurnID   string   `json:"turn_id"`
	Model    string   `json:"model"`
	Provider string   `json:"provider"`
	FileIDs  []string `json:"file_ids"`
}

// Matches reports whether the evidence belongs to this model and provider.
func (a *Accepted) Matches(model, provider string) bool {
	return a != nil && a.Model == model && a.Provider == provider
}

// Contains reports whether the file was already accepted.
func (a *Accepted) Contains(fileID string) bool {
	if a == nil {
		return false
	}
	for _, id := range a.FileIDs {
		if id == fileID {
			return true
		}
	}
	return false
}

// Map renders the evidence for thread metadata.
func (a *Accepted) Map() map[string]any {
	if a == nil {
		return nil
	}
	ids := make([]any, 0, len(a.FileIDs))
	for _, id := range a.FileIDs {
		ids = append(ids, id)
	}
	return map[string]any{
		"turn_id":  a.TurnID,
		"model":    a.Model,
		"provider": a.Provider,
		"file_ids": ids,
	}
}

// ParseAccepted restores evidence persisted in thread metadata.
func ParseAccepted(value any) *Accepted {
	record, ok := value.(map[string]any)
	if !ok || record == nil {
		return nil
	}
	accepted := &Accepted{
		TurnID:   strings.TrimSpace(stringFromAny(record["turn_id"])),
		Model:    strings.TrimSpace(stringFromAny(record["model"])),
		Provider: strings.TrimSpace(stringFromAny(record["provider"])),
	}
	switch ids := record["file_ids"].(type) {
	case []any:
		for _, id := range ids {
			if value := strings.TrimSpace(stringFromAny(id)); value != "" {
				accepted.FileIDs = append(accepted.FileIDs, value)
			}
		}
	case []string:
		accepted.FileIDs = append(accepted.FileIDs, ids...)
	}
	if accepted.Model == "" {
		return nil
	}
	return accepted
}

// RejectedSet is the durable set of file IDs the provider proved rejected.
type RejectedSet map[string]struct{}

// ParseRejected restores the set persisted in thread metadata.
func ParseRejected(value any) RejectedSet {
	set := RejectedSet{}
	appendID := func(id string) {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = struct{}{}
		}
	}
	switch ids := value.(type) {
	case []any:
		for _, id := range ids {
			appendID(stringFromAny(id))
		}
	case []string:
		for _, id := range ids {
			appendID(id)
		}
	}
	return set
}

// List renders the set as a sorted slice for persistence.
func (s RejectedSet) List() []any {
	if len(s) == 0 {
		return nil
	}
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

// Has reports whether the file is in the set.
func (s RejectedSet) Has(fileID string) bool {
	_, ok := s[fileID]
	return ok
}

// PendingConfirmation mirrors Rust pending_audio_confirmation: the submitted
// request can confirm decoding only when it carries a file this model and
// provider have not accepted yet.
func PendingConfirmation(previous *Accepted, turnID, model, provider string, items []any) *Accepted {
	ids := FileAudioIDs(items)
	if len(ids) == 0 {
		return nil
	}
	known := previous
	if !known.Matches(model, provider) {
		known = nil
	}
	allKnown := true
	for _, id := range ids {
		if !known.Contains(id) {
			allKnown = false
			break
		}
	}
	if allKnown {
		return nil
	}
	return &Accepted{TurnID: turnID, Model: model, Provider: provider, FileIDs: ids}
}

// FileAudioIDs returns the uploaded audio file IDs carried by prompt items, in
// order and without duplicates.
func FileAudioIDs(items []any) []string {
	out := make([]string, 0, 2)
	seen := map[string]bool{}
	walkItemSources(items, func(source source) {
		if source.kind == sourceAudio && source.fileID != "" && !seen[source.fileID] {
			seen[source.fileID] = true
			out = append(out, source.fileID)
		}
	})
	return out
}

type sourceKind int

const (
	sourceAudio sourceKind = iota
	sourceInlineAudio
	sourceFileImage
	sourceOpaque
)

type source struct {
	kind   sourceKind
	fileID string
}

// walkItemSources visits the file-carrying content of one prompt history:
// uploaded audio, inline audio, file images, and opaque checkpoints.
func walkItemSources(items []any, visit func(source)) {
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringFromAny(item["type"])) {
		case "message":
			walkContentSources(item["content"], visit)
		case "function_call_output", "custom_tool_call_output":
			walkContentSources(item["output"], visit)
		case "compaction", "context_compaction":
			visit(source{kind: sourceOpaque})
		}
	}
}

func walkContentSources(value any, visit func(source)) {
	switch content := value.(type) {
	case []any:
		for _, raw := range content {
			walkContentSources(raw, visit)
		}
	case []map[string]any:
		for _, raw := range content {
			walkContentSources(raw, visit)
		}
	case map[string]any:
		switch strings.TrimSpace(stringFromAny(content["type"])) {
		case "input_audio", "inputAudio", "audio":
			fileID := firstNonEmptyString(content["file_id"], content["fileId"])
			if fileID != "" && firstNonEmptyString(content["audio_url"], content["audioUrl"], content["url"]) == "" {
				visit(source{kind: sourceAudio, fileID: fileID})
				return
			}
			visit(source{kind: sourceInlineAudio})
		case "input_image", "image":
			if firstNonEmptyString(content["file_id"], content["fileId"]) != "" &&
				firstNonEmptyString(content["image_url"], content["imageUrl"], content["url"]) == "" {
				visit(source{kind: sourceFileImage})
			}
		}
	}
}

// Submitted is the immutable identity of one submitted request (Rust
// SubmittedFileAudio). The prompt itself may be released while the provider
// streams, so attribution only looks at this snapshot.
type Submitted struct {
	FileIDs        []string
	candidate      int
	hasCandidate   bool
	hasInlineAudio bool
	hasFileImage   bool
}

// SubmittedFromRequest mirrors Rust SubmittedFileAudio::from_request. A single
// file is always a candidate; with several files only the single file the
// model/provider have not accepted yet is attributable. An unknown checkpoint
// that competes for the failure removes every candidate.
func SubmittedFromRequest(items []any, accepted *Accepted, clearCheckpoints []string) *Submitted {
	submitted := &Submitted{hasCandidate: false}
	unknownCheckpoint := false
	walkItemSourcesDetailed(items, func(kind sourceKind, fileID, ciphertext string) {
		switch kind {
		case sourceAudio:
			submitted.FileIDs = append(submitted.FileIDs, fileID)
		case sourceInlineAudio:
			submitted.hasInlineAudio = true
		case sourceFileImage:
			submitted.hasFileImage = true
		case sourceOpaque:
			known := false
			for _, value := range clearCheckpoints {
				if value == ciphertext {
					known = true
					break
				}
			}
			if !known {
				unknownCheckpoint = true
			}
		}
	})
	if len(submitted.FileIDs) == 0 {
		return nil
	}
	if !unknownCheckpoint {
		switch {
		case len(submitted.FileIDs) == 1:
			submitted.candidate = 0
			submitted.hasCandidate = true
		default:
			first := -1
			unique := true
			for index, id := range submitted.FileIDs {
				if accepted.Contains(id) {
					continue
				}
				if first >= 0 {
					unique = false
					break
				}
				first = index
			}
			if unique && first >= 0 {
				submitted.candidate = first
				submitted.hasCandidate = true
			}
		}
	}
	return submitted
}

func walkItemSourcesDetailed(items []any, visit func(kind sourceKind, fileID, ciphertext string)) {
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringFromAny(item["type"])) {
		case "message":
			walkContentSourcesDetailed(item["content"], visit)
		case "function_call_output", "custom_tool_call_output":
			walkContentSourcesDetailed(item["output"], visit)
		case "compaction", "context_compaction":
			visit(sourceOpaque, "", strings.TrimSpace(stringFromAny(item["encrypted_content"])))
		}
	}
}

func walkContentSourcesDetailed(value any, visit func(kind sourceKind, fileID, ciphertext string)) {
	switch content := value.(type) {
	case []any:
		for _, raw := range content {
			walkContentSourcesDetailed(raw, visit)
		}
	case []map[string]any:
		for _, raw := range content {
			walkContentSourcesDetailed(raw, visit)
		}
	case map[string]any:
		switch strings.TrimSpace(stringFromAny(content["type"])) {
		case "input_audio", "inputAudio", "audio":
			fileID := firstNonEmptyString(content["file_id"], content["fileId"])
			if fileID != "" && firstNonEmptyString(content["audio_url"], content["audioUrl"], content["url"]) == "" {
				visit(sourceAudio, fileID, "")
				return
			}
			visit(sourceInlineAudio, "", "")
		case "input_image", "image":
			if firstNonEmptyString(content["file_id"], content["fileId"]) != "" &&
				firstNonEmptyString(content["image_url"], content["imageUrl"], content["url"]) == "" {
				visit(sourceFileImage, "", "")
			}
		}
	}
}

// RejectedFileID returns the single recording the error proves rejected.
func (s *Submitted) RejectedFileID(err error) (string, bool) {
	if s == nil {
		return "", false
	}
	index, ok := s.rejectedIndex(err)
	if !ok {
		return "", false
	}
	return s.FileIDs[index], true
}

func (s *Submitted) rejectedIndex(err error) (int, bool) {
	if s == nil || !s.hasCandidate {
		return 0, false
	}
	uniqueFile := len(s.FileIDs) == 1
	if IsInvalidAudioInput(err) {
		// Rust: !has_inline_audio && (unique_file || per_recording).
		if !s.hasInlineAudio && (uniqueFile || PerRecordingAudioError(err)) {
			return s.candidate, true
		}
		return 0, false
	}
	if uniqueFile && !s.hasFileImage && IsUnknownFileError(err) {
		return s.candidate, true
	}
	return 0, false
}

// RejectedFileIDs resolves every file the failed request proves unusable. An
// unambiguous candidate is returned directly; otherwise an unknown-file error
// probes the submitted IDs and keeps only authoritative negatives.
func (s *Submitted) RejectedFileIDs(ctx context.Context, store attachmentstore.Store, err error) []string {
	if s == nil {
		return nil
	}
	if index, ok := s.rejectedIndex(err); ok {
		return []string{s.FileIDs[index]}
	}
	if !IsUnknownFileError(err) {
		return nil
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(s.FileIDs))
	for _, id := range s.FileIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return UnavailableFileAudio(ctx, store, ids)
}

// UnavailableFileAudio returns the subset of IDs the store cannot resolve.
// The whole preflight is bounded because Responses still validates unchecked
// IDs (Rust #59234).
func UnavailableFileAudio(ctx context.Context, store attachmentstore.Store, ids []string) []string {
	if store == nil || len(ids) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	type result struct {
		id      string
		missing bool
	}
	results := make(chan result, len(ids))
	semaphore := make(chan struct{}, resolveConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-checkCtx.Done():
				return
			}
			defer func() { <-semaphore }()
			results <- result{id: id, missing: !attachmentstore.IsResolvable(store, id)}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	missing := make([]string, 0, len(ids))
	for {
		select {
		case <-checkCtx.Done():
			return missing
		case res, ok := <-results:
			if !ok {
				return missing
			}
			if res.missing {
				missing = append(missing, res.id)
			}
		}
	}
}

// ProjectFileAudio returns prompt items with the rejected uploaded-audio parts
// removed, keeping every sibling part and the per-item metadata aligned. The
// input slice and its maps are not mutated.
func ProjectFileAudio(items []any, rejected RejectedSet) []any {
	if len(rejected) == 0 || len(items) == 0 {
		return items
	}
	out := make([]any, 0, len(items))
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			out = append(out, value)
			continue
		}
		if strings.TrimSpace(stringFromAny(item["type"])) != "message" {
			out = append(out, value)
			continue
		}
		content, ok := item["content"].([]any)
		if !ok {
			out = append(out, value)
			continue
		}
		kept := make([]any, 0, len(content))
		removed := false
		for _, raw := range content {
			if part, ok := raw.(map[string]any); ok && isRejectedAudioPart(part, rejected) {
				removed = true
				continue
			}
			kept = append(kept, raw)
		}
		if !removed {
			out = append(out, value)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(stringFromAny(item["role"])), "user") && len(kept) == 0 {
			continue
		}
		clone := make(map[string]any, len(item))
		for key, value := range item {
			clone[key] = value
		}
		clone["content"] = kept
		out = append(out, clone)
	}
	return out
}

func isRejectedAudioPart(part map[string]any, rejected RejectedSet) bool {
	switch strings.TrimSpace(stringFromAny(part["type"])) {
	case "input_audio", "inputAudio", "audio":
		fileID := firstNonEmptyString(part["file_id"], part["fileId"])
		return fileID != "" && rejected.Has(fileID)
	default:
		return false
	}
}

// IsInvalidAudioInput reports the terminal provider audio-validation failure.
func IsInvalidAudioInput(err error) bool {
	apiErr, ok := err.(*codexapi.APIError)
	return ok && apiErr != nil && apiErr.Kind == codexapi.ErrorInvalidAudioInput
}

// PerRecordingAudioError reports whether the audio validation applies to one
// clip rather than the whole request (Rust per_recording = code != "audio_too_large").
func PerRecordingAudioError(err error) bool {
	apiErr, ok := err.(*codexapi.APIError)
	if !ok || apiErr == nil {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(apiErr.AudioValidationCode), "audio_too_large")
}

// IsUnknownFileError reports the provider's 404 for an expired uploaded file.
func IsUnknownFileError(err error) bool {
	apiErr, ok := err.(*codexapi.APIError)
	if !ok || apiErr == nil || apiErr.Status != 404 {
		return false
	}
	return strings.TrimSpace(apiErr.Message) == unknownFileMessage
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringFromAny(value)); text != "" {
			return text
		}
	}
	return ""
}
