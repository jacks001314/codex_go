package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
	"unicode"

	"codex_go/audioutil"
	contextfrag "codex_go/context"
	"codex_go/eventmap"
	"codex_go/features"
	"codex_go/retainedctx"
	"codex_go/utils"
)

const (
	// Mirrors Rust codex_prompts::templates::compact::summary_prefix.md so the
	// persisted compaction summary message matches the Rust rollout contract.
	SummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

	// SummarizationPrompt mirrors Rust codex_prompts::SUMMARIZATION_PROMPT
	// (codex-rs/prompts/templates/compact/prompt.md): the built-in compaction
	// prompt used when no `compact_prompt` is configured.
	SummarizationPrompt = `You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.

Include:
- Current progress and key decisions made
- Important context, constraints, or user preferences
- What remains to be done (clear next steps)
- Any critical data, examples, or references needed to continue

Be concise, structured, and focused on helping the next LLM seamlessly continue the work.`

	TriggerAuto   Trigger = "auto"
	TriggerManual Trigger = "manual"

	ReasonContextWindowExceeded Reason = "contextWindowExceeded"
	ReasonTokenLimit            Reason = "tokenLimit"
	ReasonModelSwitch           Reason = "modelSwitch"
	// ReasonCompHashChanged is the pre-turn compaction triggered when the
	// model's compaction compatibility hash changed (Rust
	// CompactionReason::CompHashChanged, #46324).
	ReasonCompHashChanged Reason = "compHashChanged"
	ReasonUserRequested   Reason = "userRequested"

	PhasePreTurn Phase = "preTurn"
	PhaseMidTurn Phase = "midTurn"
	// PhasePostTurn is the opt-in compaction that runs after a turn's final
	// response (#46541).
	PhasePostTurn       Phase = "postTurn"
	PhaseStandaloneTurn Phase = "standaloneTurn"

	StatusSkipped     Status = "skipped"
	StatusNeeded      Status = "needed"
	StatusCompleted   Status = "completed"
	StatusInterrupted Status = "interrupted"
	StatusFailed      Status = "failed"

	SourceLocal  Source = "local"
	SourceRemote Source = "remote"

	RemoteRetainedMessageTokenBudget = 64_000
	MaxRetainedAgentMessageTokens    = 10_000
	// CompactionUserMessageBudget mirrors Rust's COMPACT_USER_MESSAGE_MAX_TOKENS:
	// the shared budget for the user messages a local compaction keeps.
	CompactionUserMessageBudget = 20_000
)

// RetainClientToolOutputsKey and RetainClientDeveloperMessagesKey are the
// registered feature keys the compaction pipeline resolves through
// features.Enabled, mirroring Rust `Feature::RetainClientToolOutputs` and
// `Feature::RetainClientDeveloperMessages` (codex-rs/features/src/lib.rs).
const (
	RetainClientToolOutputsKey       = "retain_client_tool_outputs"
	RetainClientDeveloperMessagesKey = "retain_client_developer_messages"
	// CompactionImageBudgetKey is the opt-in feature that charges retained
	// images against the remote-compaction message budget (Rust #40280).
	CompactionImageBudgetKey = "compaction_image_budget"
)

var ErrInvalidCompaction = errors.New("invalid compaction")

type Trigger string

type Reason string

type Phase string

type Status string

type Source string

type Scope string

const (
	ScopeTotal           Scope = "total"
	ScopeBodyAfterPrefix Scope = "bodyAfterPrefix"
)

type Policy struct {
	Enabled              bool
	TokenLimit           int
	WindowTokens         int
	PrefillTokens        int
	Scope                Scope
	MinMessages          int
	FallbackBufferTokens int
}

type TokenStatus struct {
	ActiveContextTokens       int
	AutoCompactScopeTokens    int
	AutoCompactScopeLimit     int
	TokensUntilCompaction     *int
	BaseWindowTokensRemaining *int
	ShouldCompact             bool
	Reason                    Reason
	NewContextWindowRequired  bool
}

func Evaluate(policy Policy, activeContextTokens int) TokenStatus {
	if policy.Scope == "" {
		policy.Scope = ScopeTotal
	}
	status := TokenStatus{ActiveContextTokens: activeContextTokens}
	if !policy.Enabled {
		status.TokensUntilCompaction = nil
		return status
	}
	limit := policy.TokenLimit
	scopeTokens := activeContextTokens
	if policy.Scope == ScopeBodyAfterPrefix {
		scopeTokens = activeContextTokens - policy.PrefillTokens
		if scopeTokens < 0 {
			scopeTokens = 0
		}
		if limit == 0 {
			limit = policy.WindowTokens
		}
	} else if limit == 0 {
		limit = policy.WindowTokens
	}
	status.AutoCompactScopeTokens = scopeTokens
	status.AutoCompactScopeLimit = limit
	if limit <= 0 {
		status.TokensUntilCompaction = nil
		return status
	}
	// Mirrors Rust Session::context_window_token_status: the base window
	// remaining is the minimum of the auto-compact scope remaining and the
	// full context window remaining, whichever is tighter. Callers use it for
	// the token-budget reminder and auto-compact fallback prompt.
	baseRemaining := max(0, limit-scopeTokens)
	if policy.WindowTokens > 0 {
		baseRemaining = min(baseRemaining, max(0, policy.WindowTokens-activeContextTokens))
	}
	status.BaseWindowTokensRemaining = &baseRemaining
	bufferedLimit := limit + max(0, policy.FallbackBufferTokens)
	remaining := bufferedLimit - scopeTokens
	status.TokensUntilCompaction = &remaining
	if scopeTokens >= bufferedLimit {
		status.ShouldCompact = true
		status.Reason = ReasonTokenLimit
	}
	if policy.WindowTokens > 0 && activeContextTokens >= policy.WindowTokens {
		status.ShouldCompact = true
		status.Reason = ReasonContextWindowExceeded
		status.NewContextWindowRequired = true
	}
	return status
}

type Item struct {
	ID        string
	Type      string
	Role      string
	Name      string
	Namespace string
	CallID    string
	Text      string
	Kind      string
	Content   []ContentPart
	Data      map[string]any
	Raw       json.RawMessage
	Created   time.Time
}

// CompactionPrefix identifies the history snapshot a speculative compaction
// will replace. Later items survive when the candidate is installed.
type CompactionPrefix struct {
	// Generation is the HistoryVersion captured with the request.
	Generation          uint64
	Len                 int
	AuthOwnerGeneration *uint64
}

type ContentPart struct {
	Type     string
	Text     string
	ImageURL string
	// FileID marks an uploaded-file image reference instead of an inline URL
	// (Rust ImageReference::File, #46072).
	FileID   string
	AudioURL string
	Detail   *string
}

type Request struct {
	ThreadID         string
	TurnID           string
	Trigger          Trigger
	Reason           Reason
	Phase            Phase
	Prompt           string
	History          []Item
	StartedAt        time.Time
	WindowNumber     uint64
	WindowIDs        WindowIDs
	MaxHistoryTokens int
}

func (r *Request) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidCompaction)
	}
	if strings.TrimSpace(r.ThreadID) == "" {
		return fmt.Errorf("%w: thread id is required", ErrInvalidCompaction)
	}
	if r.Trigger == "" {
		return fmt.Errorf("%w: trigger is required", ErrInvalidCompaction)
	}
	if r.Reason == "" {
		return fmt.Errorf("%w: reason is required", ErrInvalidCompaction)
	}
	if r.Phase == "" {
		return fmt.Errorf("%w: phase is required", ErrInvalidCompaction)
	}
	return nil
}

type Result struct {
	Status      Status
	Request     Request
	Summary     string
	NewHistory  []Item
	CompletedAt time.Time
	Error       string
	Source      Source
	ResponseID  string
	Model       string
	ProviderID  string
	// CompactionModelHash records the producing model's comp_hash for strict
	// Guardian checkpoint compatibility (Rust #42852).
	CompactionModelHash string
	Usage               *Usage
}

type Usage struct {
	InputTokens           int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningOutputTokens int64
}

func (r *Result) Succeeded() bool {
	return r != nil && r.Status == StatusCompleted
}

func BuildRequest(threadID string, turnID string, trigger Trigger, reason Reason, phase Phase, prompt string, history []Item, window *Window) (*Request, error) {
	if prompt == "" {
		prompt = "Summarize the conversation so far, preserving user intent, decisions, file changes, commands, and unresolved work."
	}
	var windowNumber uint64
	var windowIDs WindowIDs
	if window != nil {
		windowNumber = window.Number()
		windowIDs = window.IDs()
	}
	request := &Request{
		ThreadID:     threadID,
		TurnID:       turnID,
		Trigger:      trigger,
		Reason:       reason,
		Phase:        phase,
		Prompt:       prompt,
		History:      cloneItems(history),
		StartedAt:    time.Now().UTC(),
		WindowNumber: windowNumber,
		WindowIDs:    windowIDs,
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return request, nil
}

func BuildCompactedHistory(prefix []Item, userMessages []Item, summary string) []Item {
	history := make([]Item, 0, len(prefix)+len(userMessages)+1)
	history = append(history, cloneItems(prefix)...)
	history = append(history, cloneItems(userMessages)...)
	history = append(history, Item{
		ID:   "compaction-summary",
		Type: "message",
		Role: "user",
		Kind: "compaction_summary",
		Text: strings.TrimSpace(SummaryPrefix + "\n" + summary),
		Data: map[string]any{
			"internal_chat_message_metadata_passthrough": map[string]any{
				"content_item_kinds": []any{contextfrag.CompactionSummaryKind},
			},
		},
	})
	return history
}

func SummarizeLocally(history []Item, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 4000
	}
	var builder strings.Builder
	for _, item := range history {
		text := ItemText(&item)
		if text == "" {
			continue
		}
		label := item.Role
		if label == "" {
			label = item.Type
		}
		if label == "" {
			label = "item"
		}
		line := label + ": " + strings.TrimSpace(text)
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line)
		if builder.Len() >= maxChars {
			break
		}
	}
	text := builder.String()
	if len(text) > maxChars {
		text = text[:maxChars]
	}
	return strings.TrimSpace(text)
}

func CompactLocally(request *Request, maxSummaryChars int, initialContext []Item, injectBeforeLastUser bool) (*Result, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	history := requestHistoryForCompaction(request)
	summary := SummarizeLocally(history, maxSummaryChars)
	compacted := BuildCompactedHistory(nil, retainedUserMessagesForLocalCompaction(history, CompactionUserMessageBudget), summary)
	if injectBeforeLastUser {
		compacted = InsertInitialContextBeforeLastUserOrSummary(compacted, initialContext)
	}
	return &Result{
		Status:      StatusCompleted,
		Request:     *request,
		Summary:     summary,
		NewHistory:  compacted,
		CompletedAt: time.Now().UTC(),
		Source:      SourceLocal,
	}, nil
}

type RemoteRunner interface {
	Compact(ctx context.Context, request *Request) (*Result, error)
}

type RemoteOptions struct {
	Runner                        RemoteRunner
	MaxSummaryChars               int
	InitialContext                []Item
	InjectBeforeLastUser          bool
	FallbackToLocal               bool
	RetainClientDeveloperMessages bool
	// RetainClientToolOutputs honors `retain_client_tool_outputs`, the registered
	// feature that preserves explicitly client-authored, named tool outputs
	// without a matching call across compaction (#52937). FeatureSettings, when
	// set, resolves the flag from the registry instead of this field.
	RetainClientToolOutputs bool
	// FeatureSettings is the turn's resolved feature enablement (Rust `Features`).
	// The compaction pipeline reads `retain_client_tool_outputs` and
	// `retain_client_developer_messages` from it.
	FeatureSettings map[string]bool
}

func CompactRemotely(ctx context.Context, request *Request, options *RemoteOptions) (*Result, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if options == nil {
		options = &RemoteOptions{}
	}
	if options.Runner != nil {
		result, err := options.Runner.Compact(ctx, request)
		if err == nil && result != nil && result.Succeeded() {
			result.Request = *request
			if result.Source == "" {
				result.Source = SourceRemote
			}
			if len(result.NewHistory) == 0 && strings.TrimSpace(result.Summary) != "" {
				result.NewHistory = BuildCompactedHistory(nil, lastUserMessages(requestHistoryForCompaction(request), 1), result.Summary)
			}
			result.NewHistory = processRemoteHistoryWithRetained(
				result.NewHistory,
				requestHistoryForCompaction(request),
				options.InitialContext,
				options.InjectBeforeLastUser,
				resolveRetentionPolicy(
					options.FeatureSettings,
					options.RetainClientDeveloperMessages,
					options.RetainClientToolOutputs,
				),
			)
			if result.CompletedAt.IsZero() {
				result.CompletedAt = time.Now().UTC()
			}
			return result, nil
		}
		if err != nil && !options.FallbackToLocal {
			return nil, err
		}
		if result != nil && !result.Succeeded() && !options.FallbackToLocal {
			return result, nil
		}
	}
	if !options.FallbackToLocal {
		return nil, fmt.Errorf("%w: remote runner is required", ErrInvalidCompaction)
	}
	return CompactLocally(request, options.MaxSummaryChars, options.InitialContext, options.InjectBeforeLastUser)
}

func ShouldKeepCompactedHistoryItem(item Item) bool {
	switch item.Type {
	case "agent_message":
		return !isAgentCompletionMessage(&item) && !isDescendantProgressMessage(&item)
	case "compaction", "context_compaction":
		return true
	case "compaction_trigger":
		return false
	}
	if item.Type != "message" && item.Type != "user_message" && item.Type != "assistant_message" {
		return false
	}
	switch item.Role {
	case "developer", "system":
		return false
	case "assistant":
		return true
	case "user":
		return item.Kind == "user_message" || item.Kind == "hook_prompt" || item.Kind == "compaction_summary"
	default:
		return false
	}
}

func FilterCompactedHistory(items []Item, retention ...bool) []Item {
	policy := retentionPolicyFromFlags(retention)
	filtered := make([]Item, 0, len(items))
	for _, item := range items {
		if ShouldKeepCompactedHistoryItem(item) ||
			(policy.clientDeveloperMessages && IsClientAuthoredDeveloperMessage(item)) ||
			(policy.clientToolOutputs && IsRetainedToolOutput(item)) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func InsertInitialContextBeforeLastUserOrSummary(history []Item, initialContext []Item) []Item {
	if len(initialContext) == 0 {
		return cloneItems(history)
	}
	insertAt := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		item := history[i]
		// Rust compact.rs:657-663 (#52937): a retained standalone output forms the
		// same insertion boundary as the last real user or agent message.
		if IsRetainedToolOutput(item) || IsRealUserOrAgentMessage(item) {
			insertAt = i
			break
		}
		if item.Role == "user" || item.Type == "user_message" {
			// Compaction summaries are encoded as user messages, so retain a
			// fallback insertion point when no real user or agent message exists.
			continue
		}
	}
	if insertAt == len(history) {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Kind == "compaction_summary" {
				insertAt = i
				break
			}
		}
	}
	result := make([]Item, 0, len(history)+len(initialContext))
	result = append(result, history[:insertAt]...)
	result = append(result, cloneItems(initialContext)...)
	result = append(result, history[insertAt:]...)
	return result
}

// IsRealUserOrAgentMessage reports whether an item is a real user turn or a
// non-final agent message (Rust `compact::is_real_user_or_agent_message`).
func IsRealUserOrAgentMessage(item Item) bool {
	if item.Type == "agent_message" {
		return !isAgentCompletionMessage(&item)
	}
	if item.Role != "user" && item.Type != "user_message" {
		return false
	}
	return item.Kind != "compaction_summary" &&
		!strings.HasPrefix(strings.TrimSpace(item.Text), SummaryPrefix+"\n")
}

// InstallCompactedHistoryPrefix replaces only the captured history prefix and
// preserves items appended while speculative compaction was running. Initial
// context follows real later user/agent messages; otherwise it is inserted at
// the compacted history's user/summary boundary.
func InstallCompactedHistoryPrefix(history []Item, replacement []Item, prefix CompactionPrefix, prefixContext []Item, initialContext []Item) []Item {
	prefixLen := prefix.Len
	if prefixLen < 0 {
		prefixLen = 0
	}
	if prefixLen > len(history) {
		prefixLen = len(history)
	}
	suffix := cloneItems(history[prefixLen:])
	installed := cloneItems(replacement)
	if historyContainsRealUserOrAgentMessage(suffix) {
		installed = append(installed, cloneItems(initialContext)...)
	} else {
		installed = InsertInitialContextBeforeLastUserOrSummary(installed, initialContext)
	}
	out := make([]Item, 0, len(prefixContext)+len(installed)+len(suffix))
	out = append(out, cloneItems(prefixContext)...)
	out = append(out, installed...)
	out = append(out, suffix...)
	return out
}

func historyContainsRealUserOrAgentMessage(items []Item) bool {
	for _, item := range items {
		if IsRealUserOrAgentMessage(item) {
			return true
		}
	}
	return false
}

// TrimHistoryToContextWindow applies an explicit context window rather than
// reading configuration at request time, so speculative requests keep the
// window captured by their model step.
func TrimHistoryToContextWindow(items []Item, contextWindow int, baseInstructions string) []Item {
	if contextWindow <= 0 {
		return cloneItems(items)
	}
	used := EstimateTextTokens(baseInstructions)
	for i := range items {
		used += max(1, EstimateItemTokens(&items[i]))
	}
	if used <= contextWindow {
		return cloneItems(items)
	}
	return TrimHistoryToTokenBudget(items, max(0, contextWindow-EstimateTextTokens(baseInstructions)))
}

// HistoryVersion returns a stable fingerprint used to deduplicate speculative
// compaction attempts for the same captured history.
func HistoryVersion(items []Item) uint64 {
	hasher := fnv.New64a()
	for i := range items {
		encoded, err := json.Marshal(items[i])
		if err != nil {
			encoded = []byte(ItemText(&items[i]))
		}
		_, _ = hasher.Write([]byte(fmt.Sprintf("%d:", len(encoded))))
		_, _ = hasher.Write(encoded)
	}
	return hasher.Sum64()
}

func ProcessRemoteHistory(remote []Item, initialContext []Item, injectBeforeLastUser bool) []Item {
	filtered := FilterCompactedHistory(remote)
	if injectBeforeLastUser {
		return InsertInitialContextBeforeLastUserOrSummary(filtered, initialContext)
	}
	return filtered
}

func processRemoteHistoryWithRetained(remote []Item, original []Item, initialContext []Item, injectBeforeLastUser bool, policy retentionPolicy) []Item {
	retained := retainedMessagesForRemoteCompaction(original, RemoteRetainedMessageTokenBudget, policy)
	filtered := FilterCompactedHistory(remote, policy.clientDeveloperMessages, policy.clientToolOutputs)
	history := append([]Item(nil), retained...)
	for _, item := range filtered {
		if compactHistoryContainsItem(history, item) {
			continue
		}
		history = append(history, item)
	}
	if injectBeforeLastUser {
		return InsertInitialContextBeforeLastUserOrSummary(history, initialContext)
	}
	return cloneItems(history)
}

func retainedMessagesForRemoteCompaction(items []Item, maxTokens int, policy retentionPolicy) []Item {
	if maxTokens <= 0 {
		return nil
	}
	candidates := make([]Item, 0, len(items))
	for _, item := range items {
		keep := (item.Type == "message" || item.Type == "user_message") && item.Role == "user" && ShouldKeepCompactedHistoryItem(item)
		if policy.clientDeveloperMessages && IsClientAuthoredDeveloperMessage(item) {
			keep = true
		}
		// Rust session/mod.rs:4666-4678 (#52937): retained standalone outputs share
		// the same retention budget as the retained client messages.
		if policy.clientToolOutputs && IsRetainedToolOutput(item) {
			keep = true
		}
		if isStructuredAgentMessage(&item) {
			cost := EstimateItemTokens(&item)
			keep = !isAgentCompletionMessage(&item) && cost <= MaxRetainedAgentMessageTokens
		}
		if keep {
			candidates = append(candidates, item)
		}
	}

	remaining := maxTokens
	retainedReversed := make([]Item, 0, len(candidates))
	for i := len(candidates) - 1; i >= 0 && remaining > 0; i-- {
		item := candidates[i]
		clientDeveloper := IsClientAuthoredDeveloperMessage(item)
		chargeImages := policy.imageBudget && !clientDeveloper
		textCost := EstimateTextTokens(ItemText(&item))
		contentCost := EstimateItemTokens(&item)
		cost := max(1, contentCost)
		itemType := strings.ToLower(strings.TrimSpace(item.Type))
		isMessage := itemType == "message" || itemType == "user_message" || itemType == "assistant_message"
		if isMessage && !clientDeveloper && !chargeImages {
			// Without the image budget, retained messages charge their text only
			// (Rust #40280); outputs and other items keep the serialized estimate.
			cost = max(1, textCost)
		}
		if cost <= remaining {
			retainedReversed = append(retainedReversed, item)
			remaining -= cost
			continue
		}
		// Rust compact_remote_v2.rs:641-649 (#52937): a retained output is kept
		// whole or dropped, and the scan stops so newer-first ordering is
		// preserved. Only retained output candidates reach this point because the
		// filter above gates on the feature.
		if IsRetainedToolOutput(item) {
			break
		}
		if isStructuredAgentMessage(&item) {
			continue
		}
		if chargeImages && retainedMessageImageCount(item) > 0 {
			// Rust #40280: an image at the truncation boundary is kept or dropped
			// whole with its adjacent labels. Once it does not fit, stop
			// backfilling the remaining budget with older messages.
			budget := remaining
			remaining = 0
			if truncated, ok := truncateRetainedMessageWithImages(item, budget); ok {
				retainedReversed = append(retainedReversed, truncated)
			}
			continue
		}
		item.Text = truncateTextToTokens(ItemText(&item), remaining)
		item.Content = nil
		item.Data = nil
		item.Raw = nil
		if strings.TrimSpace(item.Text) != "" {
			retainedReversed = append(retainedReversed, item)
		}
		remaining = 0
	}
	for left, right := 0, len(retainedReversed)-1; left < right; left, right = left+1, right-1 {
		retainedReversed[left], retainedReversed[right] = retainedReversed[right], retainedReversed[left]
	}
	return cloneItems(retainedReversed)
}

// retainedMessageImageCount counts input images in a retained message.
func retainedMessageImageCount(item Item) int {
	count := 0
	for _, part := range item.Content {
		if strings.EqualFold(strings.TrimSpace(part.Type), "input_image") {
			count++
		}
	}
	return count
}

// truncateRetainedMessageWithImages keeps later content within a token budget,
// treating each image (and its immediate `<image>` / `</image>` labels) as an
// atomic group. Text follows the normal token truncation policy; audio and
// other media stay uncharged, mirroring Rust #40280.
func truncateRetainedMessageWithImages(item Item, maxTokens int) (Item, bool) {
	if maxTokens <= 0 || len(item.Content) == 0 {
		return Item{}, false
	}
	parts := cloneContentParts(item.Content)
	remaining := maxTokens
	keptReversed := make([]ContentPart, 0, len(parts))
	for len(parts) > 0 && remaining > 0 {
		last := len(parts) - 1
		imageIndex := -1
		switch {
		case isRetainedImagePart(parts[last]):
			imageIndex = last
		case isRetainedImageCloseLabel(parts[last]) && last > 0 && isRetainedImagePart(parts[last-1]):
			imageIndex = last - 1
		}
		if imageIndex >= 0 {
			start := imageIndex
			if start > 0 && isRetainedImageOpenLabel(parts[start-1]) {
				start--
			}
			groupTokens := 0
			for _, part := range parts[start:] {
				groupTokens += retainedContentPartTokens(part)
			}
			fits := groupTokens <= remaining
			if fits {
				remaining -= groupTokens
			} else {
				remaining = 0
			}
			if fits {
				for index := len(parts) - 1; index >= start; index-- {
					keptReversed = append(keptReversed, parts[index])
				}
			}
			parts = parts[:start]
			continue
		}
		part := parts[last]
		parts = parts[:last]
		switch retainedContentPartKind(part) {
		case "input_text":
			if remaining == 0 {
				continue
			}
			tokens := EstimateTextTokens(part.Text)
			if tokens <= remaining {
				remaining -= tokens
			} else {
				part.Text = truncateTextToTokens(part.Text, remaining)
				remaining = 0
			}
			if part.Text != "" {
				keptReversed = append(keptReversed, part)
			}
		case "input_audio":
			keptReversed = append(keptReversed, part)
		default:
			keptReversed = append(keptReversed, part)
		}
	}
	if len(keptReversed) == 0 {
		return Item{}, false
	}
	retained := make([]ContentPart, 0, len(keptReversed))
	for index := len(keptReversed) - 1; index >= 0; index-- {
		retained = append(retained, keptReversed[index])
	}
	item.Content = retained
	item.Text = retainedMessageText(retained)
	item.Raw = nil
	if item.Data != nil {
		if _, ok := item.Data["content_item_kinds"]; ok {
			item.Data["content_item_kinds"] = retainedContentItemKinds(retained)
		}
	}
	return item, true
}

func retainedContentPartKind(part ContentPart) string {
	return strings.ToLower(strings.TrimSpace(part.Type))
}

func retainedContentPartTokens(part ContentPart) int {
	switch retainedContentPartKind(part) {
	case "input_text", "output_text":
		return EstimateTextTokens(part.Text)
	case "input_image":
		return compactTokensFromBytes(compactImageReferenceBytes(part))
	default:
		// Audio and other media remain uncharged, matching Rust.
		return 0
	}
}

func isRetainedImagePart(part ContentPart) bool {
	return retainedContentPartKind(part) == "input_image"
}

func isRetainedImageOpenLabel(part ContentPart) bool {
	if retainedContentPartKind(part) != "input_text" {
		return false
	}
	text := part.Text
	return text == "<image>" || (strings.HasPrefix(text, "<image name=") && strings.HasSuffix(text, ">"))
}

func isRetainedImageCloseLabel(part ContentPart) bool {
	return retainedContentPartKind(part) == "input_text" && part.Text == "</image>"
}

func retainedMessageText(parts []ContentPart) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if retainedContentPartKind(part) == "input_text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func retainedContentItemKinds(parts []ContentPart) []string {
	kinds := make([]string, 0, len(parts))
	for _, part := range parts {
		switch retainedContentPartKind(part) {
		case "input_image":
			kinds = append(kinds, "user.image")
		case "input_audio":
			kinds = append(kinds, "user.audio")
		default:
			kinds = append(kinds, "user.text")
		}
	}
	return kinds
}

// IsClientAuthoredDeveloperMessage reports whether a compact item is a
// client-authored developer message and should be retained across remote
// compaction when the corresponding feature is enabled.
func IsClientAuthoredDeveloperMessage(item Item) bool {
	if !strings.EqualFold(strings.TrimSpace(item.Role), "developer") {
		return false
	}
	return IsClientAuthoredItem(item)
}

// IsClientAuthoredItem reports whether the item carries the harness-metadata
// `client_authored` annotation, without regard to its role.
func IsClientAuthoredItem(item Item) bool {
	metadata := itemHarnessMetadata(item)
	if metadata == nil {
		return false
	}
	authored, _ := metadata["client_authored"].(bool)
	return authored
}

func itemHarnessMetadata(item Item) map[string]any {
	raw, ok := item.Data["harness_metadata"]
	if !ok {
		return nil
	}
	var metadata map[string]any
	switch value := raw.(type) {
	case json.RawMessage:
		_ = json.Unmarshal(value, &metadata)
	case string:
		if strings.TrimSpace(value) != "" {
			_ = json.Unmarshal([]byte(value), &metadata)
		}
	case map[string]any:
		metadata = value
	}
	return metadata
}

// IsRetainedToolOutput mirrors Rust
// `compact_retained_tool_outputs::is_retained_tool_output` (#52937,
// core/src/compact_retained_tool_outputs.rs:7-20): an explicitly client-authored,
// named `function_call_output` with no matching call. Such an output carries
// instructions of its own instead of being a tool result of this session, so it
// must survive compaction. `custom_tool_call_output` and unnamed outputs are
// excluded, like the Rust pattern
// `ResponseItem::FunctionCallOutput { call_id: None, name: Some(_), .. }`.
func IsRetainedToolOutput(item Item) bool {
	if !IsClientAuthoredItem(item) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(item.Type), "function_call_output") {
		return false
	}
	return toolOutputCallID(item) == "" && strings.TrimSpace(item.Name) != ""
}

// toolOutputCallID reports the item's call id, treating an absent value (Rust
// `call_id: None`) as empty whether it is carried by the field or the payload.
func toolOutputCallID(item Item) string {
	if id := strings.TrimSpace(item.CallID); id != "" {
		return id
	}
	if value, ok := item.Data["call_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// retentionPolicy is the resolved retention state threaded through the
// compaction pipeline. Rust threads `&Features` through
// `build_v2_compacted_history` and reads both flags there
// (core/src/compact_remote_v2.rs:529-578).
type retentionPolicy struct {
	clientDeveloperMessages bool
	clientToolOutputs       bool
	imageBudget             bool
}

// retentionPolicyFromFlags maps the positional `...bool` retention arguments of
// the exported helpers onto a policy: element 0 retains client-authored developer
// messages, element 1 retains client-authored standalone tool outputs.
func retentionPolicyFromFlags(flags []bool) retentionPolicy {
	var policy retentionPolicy
	if len(flags) > 0 {
		policy.clientDeveloperMessages = flags[0]
	}
	if len(flags) > 1 {
		policy.clientToolOutputs = flags[1]
	}
	return policy
}

// resolveRetentionPolicy resolves the retention flags of one compaction run.
// FeatureSettings, when non-nil, is the turn's resolved feature enablement (Rust
// `Features`), so the registered keys are read here; the explicit fields stay as
// an override and compatibility path.
func resolveRetentionPolicy(settings map[string]bool, retainClientDeveloperMessages bool, retainClientToolOutputs bool) retentionPolicy {
	return retentionPolicy{
		clientDeveloperMessages: retainClientDeveloperMessages ||
			features.Enabled(settings, RetainClientDeveloperMessagesKey),
		clientToolOutputs: retainClientToolOutputs ||
			features.Enabled(settings, RetainClientToolOutputsKey),
		imageBudget: features.Enabled(settings, CompactionImageBudgetKey),
	}
}

func EstimateItemTokens(item *Item) int {
	if item == nil {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "compaction", "context_compaction":
		if tokens, ok := estimateCompactionItemTokens(item); ok {
			return tokens
		}
	case "function_call", "custom_tool_call":
		return compactTokensFromBytes(functionCallModelVisibleBytes(item))
	case "function_call_output", "custom_tool_call_output", "tool_output":
		return compactTokensFromBytes(functionCallOutputModelVisibleBytes(item))
	}
	if isStructuredAgentMessage(item) {
		return (agentMessageModelVisibleBytes(item) + 3) / 4
	}
	if strings.EqualFold(strings.TrimSpace(item.Type), "message") {
		// Rust estimate_response_item_model_visible_bytes counts a message's
		// content blocks, so an image (inline or file-backed) is charged instead
		// of contributing zero (#46072).
		return compactTokensFromBytes(compactItemContentBytes(item))
	}
	return EstimateTextTokens(ItemText(item))
}

// EstimateCompactionContentLength mirrors Rust #53691's replay-cache-aware
// content estimate: encrypted payloads grow roughly threefold, so twelve
// ciphertext bytes represent about one token.
func EstimateCompactionContentLength(encodedLen int) int {
	if encodedLen <= 0 {
		return 0
	}
	return (encodedLen + 2) / 3
}

// CompactionItemWireBytes returns the serialized bytes of an encrypted
// compaction item and whether the item carries encrypted content.
func CompactionItemWireBytes(item *Item) (int, bool) {
	if item == nil || len(item.Raw) == 0 {
		return 0, false
	}
	var raw map[string]any
	if json.Unmarshal(item.Raw, &raw) != nil {
		return 0, false
	}
	if _, ok := raw["encrypted_content"].(string); !ok {
		return 0, false
	}
	return len(item.Raw), true
}

func estimateCompactionItemTokens(item *Item) (int, bool) {
	if item == nil || len(item.Raw) == 0 {
		return 0, false
	}
	var raw map[string]any
	if json.Unmarshal(item.Raw, &raw) != nil {
		return 0, false
	}
	encrypted, ok := raw["encrypted_content"].(string)
	if !ok {
		return 0, false
	}
	framingBytes := len(item.Raw) - len(encrypted)
	if framingBytes < 0 {
		framingBytes = 0
	}
	framingTokens := compactTokensFromBytes(framingBytes)
	contentTokens := compactTokensFromBytes(EstimateCompactionContentLength(len(encrypted)))
	return framingTokens + contentTokens, true
}

// functionNamespaceDefault mirrors Rust DEFAULT_FUNCTION_NAMESPACE.
const functionNamespaceDefault = "functions"

// compactResizedImageBytes mirrors Rust RESIZED_IMAGE_BYTES_ESTIMATE.
const compactResizedImageBytes = 7373

// compactOriginalImageMaxPatches mirrors Rust ORIGINAL_IMAGE_MAX_PATCHES: a
// file-backed image reference does not expose dimensions, so an `original`
// detail reference is charged the maximum patch count (#46072).
const compactOriginalImageMaxPatches = 10_000

// compactImageReferenceBytes mirrors Rust estimate_image_reference_bytes
// (#46072): an inline image keeps the resized-image estimate, while a file
// reference is charged the fixed resized cost or the maximum patch count when
// the request asks for `original` detail.
func compactImageReferenceBytes(part ContentPart) int {
	if strings.TrimSpace(part.FileID) == "" {
		return compactResizedImageBytes
	}
	if part.Detail != nil && strings.EqualFold(strings.TrimSpace(*part.Detail), "original") {
		// Rust approx_bytes_for_tokens(ORIGINAL_IMAGE_MAX_PATCHES): 4 bytes/token.
		return compactOriginalImageMaxPatches * 4
	}
	return compactResizedImageBytes
}

// compactTokensFromBytes mirrors Rust approx_tokens_from_byte_count_i64.
func compactTokensFromBytes(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

func functionCallModelVisibleBytes(item *Item) int {
	namespace := strings.TrimSpace(item.Namespace)
	if namespace == "" {
		namespace = functionNamespaceDefault
	}
	return len(item.Name) + len(namespace) + compactItemContentBytes(item)
}

func functionCallOutputModelVisibleBytes(item *Item) int {
	return compactItemContentBytes(item) +
		len(item.CallID) +
		len(item.Name) +
		len(strings.TrimSpace(item.Namespace))
}

// compactItemContentBytes estimates the model-visible payload bytes: the joined
// text, or per-block content when structured blocks are present (Rust
// estimate_function_output_bytes for the shapes Go models).
func compactItemContentBytes(item *Item) int {
	if item == nil {
		return 0
	}
	if len(item.Content) == 0 {
		return len(strings.TrimSpace(ItemText(item)))
	}
	bytes := 0
	for _, part := range item.Content {
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "input_text", "output_text", "text":
			bytes += len(part.Text)
		case "input_image", "image":
			bytes += compactImageReferenceBytes(part)
		case "input_audio", "audio":
			// Rust estimate_audio_bytes: approx_bytes_for_tokens over the
			// duration-derived token count.
			bytes += audioutil.EstimateAudioTokenCount(part.AudioURL) * 4
		}
	}
	if bytes == 0 {
		return len(strings.TrimSpace(ItemText(item)))
	}
	return bytes
}

// agentMessageModelVisibleBytes mirrors Rust's content-based estimate for an
// agent message (core/src/context_manager/history.rs after #45094): the author
// and recipient plus each content block, rather than the serialized JSON
// envelope (which would add keys, quoting, and escaping). Encrypted content is
// charged through its plaintext-length approximation.
func agentMessageModelVisibleBytes(item *Item) int {
	author, recipient := agentMessageAuthorRecipient(item)
	bytes := len(author) + len(recipient)
	for _, content := range rawAgentMessageContent(item) {
		block, ok := content.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(fmt.Sprint(block["type"])) {
		case "input_text":
			if text, ok := block["text"].(string); ok {
				bytes += len(text)
			}
		case "encrypted_content":
			if encrypted, ok := block["encrypted_content"].(string); ok {
				bytes += encryptedFunctionOutputByteEstimate(len(encrypted))
			}
		}
	}
	if bytes < 0 {
		bytes = 0
	}
	return bytes
}

// encryptedFunctionOutputByteEstimate mirrors Rust's
// estimate_encrypted_function_output_length (encoded_len * 9 / 16, rounded up).
func encryptedFunctionOutputByteEstimate(encodedLen int) int {
	return (encodedLen*9 + 15) / 16
}

func isStructuredAgentMessage(item *Item) bool {
	if item == nil || item.Type != "agent_message" || len(item.Raw) == 0 {
		return false
	}
	var raw map[string]any
	if json.Unmarshal(item.Raw, &raw) != nil || strings.TrimSpace(fmt.Sprint(raw["type"])) != "agent_message" {
		return false
	}
	_, hasAuthor := raw["author"]
	_, hasRecipient := raw["recipient"]
	return hasAuthor || hasRecipient
}

func isAgentCompletionMessage(item *Item) bool {
	text, ok := firstAgentMessageInputText(item)
	return ok && strings.HasPrefix(text, "Message Type: FINAL_ANSWER\n")
}

// isDescendantProgressMessage reports whether an agent message is a
// descendant-authored progress update ("Message Type: MESSAGE") that remote
// compaction v2 should not retain (Rust #39176). Descendant-authored tasks
// (encrypted NEW_TASK payloads) are still retained.
func isDescendantProgressMessage(item *Item) bool {
	author, recipient := agentMessageAuthorRecipient(item)
	if author == "" || recipient == "" || !strings.HasPrefix(author, recipient+"/") {
		return false
	}
	text, ok := firstAgentMessageInputText(item)
	return ok && strings.HasPrefix(text, "Message Type: MESSAGE\n")
}

func agentMessageAuthorRecipient(item *Item) (string, string) {
	if item == nil || len(item.Raw) == 0 {
		return "", ""
	}
	var raw map[string]any
	if json.Unmarshal(item.Raw, &raw) != nil {
		return "", ""
	}
	author, _ := raw["author"].(string)
	recipient, _ := raw["recipient"].(string)
	return author, recipient
}

func firstAgentMessageInputText(item *Item) (string, bool) {
	for index, content := range rawAgentMessageContent(item) {
		if index > 0 {
			break
		}
		block, ok := content.(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(block["type"])) != "input_text" {
			return "", false
		}
		text, ok := block["text"].(string)
		return text, ok
	}
	return "", false
}

func rawAgentMessageContent(item *Item) []any {
	if item == nil || len(item.Raw) == 0 {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(item.Raw, &raw) != nil {
		return nil
	}
	content, _ := raw["content"].([]any)
	return content
}

func compactHistoryContainsItem(items []Item, candidate Item) bool {
	for _, item := range items {
		if candidate.ID != "" && candidate.ID == item.ID && candidate.Type == item.Type {
			return true
		}
		if candidate.ID == "" && item.ID == "" && candidate.Type == item.Type && candidate.Role == item.Role && candidate.Kind == item.Kind && ItemText(&candidate) == ItemText(&item) && string(candidate.Raw) == string(item.Raw) {
			return true
		}
	}
	return false
}

func requestHistoryForCompaction(request *Request) []Item {
	if request == nil {
		return nil
	}
	history := cloneItems(request.History)
	if request.MaxHistoryTokens > 0 {
		history = TrimHistoryToTokenBudget(history, request.MaxHistoryTokens)
	}
	return history
}

func ItemText(item *Item) string {
	if item == nil {
		return ""
	}
	if strings.TrimSpace(item.Text) != "" {
		return item.Text
	}
	parts := make([]string, 0, len(item.Content))
	for i := range item.Content {
		if strings.TrimSpace(item.Content[i].Text) != "" {
			parts = append(parts, item.Content[i].Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	for _, key := range []string{"text", "output", "input", "arguments", "summary"} {
		if text, ok := item.Data[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func EstimateTokens(items []Item) int {
	total := 0
	for i := range items {
		total += EstimateItemTokens(&items[i])
	}
	return total
}

// IsModelGeneratedItem mirrors Rust history::is_model_generated_item. It
// reports whether the item was produced by the model rather than injected
// locally (for example a persisted user prompt from an interrupted turn).
func IsModelGeneratedItem(item *Item) bool {
	if item == nil {
		return false
	}
	if item.Role == "assistant" {
		return true
	}
	switch item.Type {
	case "reasoning", "function_call", "tool_search_call", "web_search_call",
		"image_generation_call", "custom_tool_call", "local_shell_call",
		"compaction", "context_compaction":
		return true
	}
	return false
}

// ItemsAfterLastModelGeneratedItem mirrors Rust
// history::items_after_last_model_generated_item: the local items recorded
// after the most recent model-generated item. These are not reflected in the
// last server-reported token usage and must be added back when estimating the
// active context.
func ItemsAfterLastModelGeneratedItem(items []Item) []Item {
	last := -1
	for i := range items {
		if IsModelGeneratedItem(&items[i]) {
			last = i
		}
	}
	if last < 0 || last+1 >= len(items) {
		return nil
	}
	return append([]Item(nil), items[last+1:]...)
}

// EstimateActiveContextTokens mirrors Rust Session::get_total_token_usage:
// the last server-reported total plus an estimate of any local items recorded
// after the most recent model-generated item.
func EstimateActiveContextTokens(items []Item, lastTotalTokens int) int {
	if lastTotalTokens < 0 {
		lastTotalTokens = 0
	}
	return lastTotalTokens + EstimateTokens(ItemsAfterLastModelGeneratedItem(items))
}

func EstimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	tokens := 0
	segmentRunes := 0
	flushSegment := func() {
		if segmentRunes == 0 {
			return
		}
		if segmentRunes <= 12 {
			tokens++
		} else {
			// Preserve the historical one-token-per-word estimate for ordinary
			// Latin text, but do not let long unbroken content look artificially
			// cheap (for example minified data or a base64-like payload).
			tokens += (segmentRunes + 3) / 4
		}
		segmentRunes = 0
	}
	for _, value := range text {
		switch {
		case unicode.IsSpace(value):
			flushSegment()
		case unicode.In(value, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			flushSegment()
			tokens++
		case unicode.IsPunct(value) || unicode.IsSymbol(value):
			flushSegment()
			tokens++
		default:
			segmentRunes++
		}
	}
	flushSegment()
	return tokens
}

func TrimHistoryToTokenBudget(items []Item, maxTokens int) []Item {
	if maxTokens <= 0 {
		return nil
	}
	selected := make([]Item, 0, len(items))
	used := 0
	for i := len(items) - 1; i >= 0; i-- {
		cost := EstimateItemTokens(&items[i])
		if cost == 0 {
			cost = 1
		}
		if used+cost > maxTokens && len(selected) > 0 {
			break
		}
		if used+cost > maxTokens {
			if isStructuredAgentMessage(&items[i]) {
				continue
			}
			item, ok := trimItemToTokenBudget(items[i], maxTokens-used)
			if !ok {
				break
			}
			selected = append(selected, item)
			break
		}
		selected = append(selected, items[i])
		used += cost
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return cloneItems(selected)
}

// trimItemToTokenBudget mirrors Rust's truncate_message_text_to_token_budget:
// content parts and their positional classifications are truncated together,
// non-text parts remain available, and the surrounding passthrough metadata is
// left intact. Legacy messages without classifications are annotated unknown.
func trimItemToTokenBudget(item Item, maxTokens int) (Item, bool) {
	trimmed := item
	trimmed.Raw = nil
	trimmed.Data = cloneItemData(item.Data)
	if trimmed.Data == nil {
		trimmed.Data = map[string]any{}
	}
	if len(item.Content) == 0 {
		trimmed.Text = truncateTextToTokens(ItemText(&item), maxTokens)
		if trimmed.Text == "" {
			return Item{}, false
		}
		trimmed.Data["content_item_kinds"] = []string{unknownContentKind}
		return trimmed, true
	}

	kinds := contentItemKinds(item.Data)
	retained := make([]ContentPart, 0, len(item.Content))
	retainedKinds := make([]string, 0, len(item.Content))
	remaining := maxTokens
	for index, part := range item.Content {
		kind := kindAt(kinds, index)
		if !contentPartIsText(part) && !isRetainedImagePart(part) {
			retained = append(retained, part)
			retainedKinds = append(retainedKinds, kind)
			continue
		}
		if !contentPartIsText(part) || remaining == 0 {
			if !contentPartIsText(part) {
				retained = append(retained, part)
				retainedKinds = append(retainedKinds, kind)
			}
			continue
		}
		tokens := EstimateTextTokens(part.Text)
		if tokens <= remaining {
			retained = append(retained, part)
			retainedKinds = append(retainedKinds, kind)
			remaining -= tokens
			continue
		}
		parts, partKinds := truncateUserTextParts([]ContentPart{part}, []string{kind}, remaining)
		retained = append(retained, parts...)
		retainedKinds = append(retainedKinds, partKinds...)
		remaining = 0
	}
	trimmed.Content = retained
	trimmed.Text = retainedMessageText(retained)
	trimmed.Data["content_item_kinds"] = retainedKinds
	if len(retained) == 0 {
		return Item{}, false
	}
	return trimmed, true
}

type WindowIDs struct {
	FirstWindowID    string
	PreviousWindowID string
	WindowID         string
}

type WindowSnapshot struct {
	PrefillInputTokens *int
}

type Window struct {
	mu                           sync.Mutex
	number                       uint64
	ids                          WindowIDs
	newContextWindowRequested    bool
	prefillInputTokens           *int
	prefillServerObserved        bool
	tokenBudgetReminderDelivered bool
	autoCompactFallbackDelivered bool
	nextID                       func() string
}

func NewWindow(threadID string) *Window {
	id := defaultWindowID(threadID, 0)
	return &Window{
		ids: WindowIDs{
			FirstWindowID: id,
			WindowID:      id,
		},
		nextID: func() string {
			return defaultWindowID(threadID, time.Now().UTC().UnixNano())
		},
	}
}

func NewWindowWithIDs(ids WindowIDs) *Window {
	if ids.FirstWindowID == "" {
		ids.FirstWindowID = ids.WindowID
	}
	return &Window{ids: ids, nextID: func() string { return defaultWindowID("window", time.Now().UTC().UnixNano()) }}
}

func (w *Window) SetIDGenerator(next func() string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if next == nil {
		return
	}
	w.nextID = next
}

func (w *Window) Number() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.number
}

func (w *Window) IDs() WindowIDs {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ids
}

func (w *Window) Restore(number uint64, ids WindowIDs) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.number = number
	if ids.FirstWindowID == "" {
		ids.FirstWindowID = ids.WindowID
	}
	w.ids = ids
}

func (w *Window) Advance() (uint64, WindowIDs) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.number++
	w.ids.PreviousWindowID = w.ids.WindowID
	w.ids.WindowID = w.nextID()
	w.newContextWindowRequested = false
	w.tokenBudgetReminderDelivered = false
	w.autoCompactFallbackDelivered = false
	return w.number, w.ids
}

func (w *Window) ClaimAutoCompactFallback() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.autoCompactFallbackDelivered {
		return false
	}
	w.autoCompactFallbackDelivered = true
	return true
}

func (w *Window) ClaimTokenBudgetReminder() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tokenBudgetReminderDelivered {
		return false
	}
	w.tokenBudgetReminderDelivered = true
	return true
}

func (w *Window) RequestNewContextWindow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.newContextWindowRequested = true
}

func (w *Window) TakeNewContextWindowRequest() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	requested := w.newContextWindowRequested
	w.newContextWindowRequested = false
	return requested
}

func (w *Window) ClearPrefill() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prefillInputTokens = nil
	w.prefillServerObserved = false
}

func (w *Window) SetEstimatedPrefill(tokens int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.prefillServerObserved {
		return
	}
	value := maxInt(tokens, 0)
	w.prefillInputTokens = &value
}

func (w *Window) EnsureServerObservedPrefill(inputTokens int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.prefillServerObserved {
		return
	}
	value := maxInt(inputTokens, 0)
	w.prefillInputTokens = &value
	w.prefillServerObserved = true
}

func (w *Window) Snapshot() WindowSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WindowSnapshot{PrefillInputTokens: cloneIntPtr(w.prefillInputTokens)}
}

func lastUserMessages(history []Item, count int) []Item {
	if count <= 0 {
		return nil
	}
	var selected []Item
	for i := len(history) - 1; i >= 0 && len(selected) < count; i-- {
		if history[i].Role == "user" && history[i].Kind != "compaction_summary" {
			selected = append(selected, history[i])
		}
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
}

// retainedUserMessagesForLocalCompaction mirrors Rust's
// `build_compacted_history_with_limit` (#48115): user messages are selected
// newest first under one shared token budget.
//
// A text-only message that fits keeps its exact content parts and annotations, so
// a multipart prompt survives compaction unchanged. A message that does not fit,
// or that carries media, is rebuilt from its text parts - media is never carried
// into the replacement history, and a message whose parts were all media keeps an
// empty text part instead of its wrapper labels - and ends the selection, so older
// messages are dropped rather than truncated.
func retainedUserMessagesForLocalCompaction(items []Item, maxTokens int) []Item {
	if maxTokens <= 0 {
		return nil
	}
	candidates := make([]Item, 0, len(items))
	for _, item := range items {
		if item.Role != "user" || item.Kind == "compaction_summary" {
			continue
		}
		if !ShouldKeepCompactedHistoryItem(item) {
			continue
		}
		candidates = append(candidates, item)
	}

	remaining := maxTokens
	selected := make([]Item, 0, len(candidates))
	for index := len(candidates) - 1; index >= 0; index-- {
		item := candidates[index]
		tokens := max(1, EstimateTextTokens(ItemText(&item)))
		if tokens <= remaining && contentPartsAreText(item.Content) {
			selected = append(selected, item)
			remaining -= tokens
			continue
		}
		// Rebuild the message from its original text parts so every retained
		// slice keeps its positional classification and the head/tail
		// truncation is applied to the parts together; discarded media is never
		// carried over (Rust #53831 `build_compacted_history_with_limit`).
		fallback := item
		fallback.Raw = nil
		parts, kinds := compactedUserTextParts(item)
		if len(parts) == 0 {
			// Rust pushes an empty-text part with an unknown classification when no
			// text part survives (`compact.rs:779-787`), so a message that carried only
			// media keeps no wrapper label as user text. Only the legacy flattened
			// shape - text with no content parts, produced by an earlier rollout edit -
			// still falls back to the flat item text.
			text := ""
			if len(item.Content) == 0 {
				text = ItemText(&item)
			}
			parts = []ContentPart{{Type: "input_text", Text: text}}
			kinds = []string{unknownContentKind}
		}
		parts, kinds = truncateUserTextParts(parts, kinds, remaining)
		fallback.Content = parts
		fallback.Text = strings.Join(partTexts(parts), "\n")
		fallback.Data = cloneItemData(item.Data)
		if fallback.Data == nil {
			fallback.Data = map[string]any{}
		}
		fallback.Data["content_item_kinds"] = kinds
		if tokens > remaining {
			markRetainedSourcesIncomplete(&fallback)
		}
		selected = append(selected, fallback)
		break
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

// unknownContentKind is the classification a retained slice or a generated
// truncation notice carries when no producer annotated it (Rust's
// `ContentItemKind("unknown")`).
const unknownContentKind = "unknown"

// compactedUserTextParts rebuilds a user message from its original text parts,
// keeping each part's positional classification and dropping media, so a
// shortened message stops flattening several producers into one part (Rust
// #53831 `build_compacted_history_with_limit`).
func compactedUserTextParts(item Item) ([]ContentPart, []string) {
	kinds := contentItemKinds(item.Data)
	parts := make([]ContentPart, 0, len(item.Content))
	partKinds := make([]string, 0, len(item.Content))
	for _, index := range eventmap.UserInputPartIndices(eventmapContentItems(item.Content)) {
		part := item.Content[index]
		if !contentPartIsText(part) {
			continue
		}
		parts = append(parts, ContentPart{Type: part.Type, Text: part.Text})
		partKinds = append(partKinds, kindAt(kinds, index))
	}
	return parts, partKinds
}

// truncateUserTextParts applies the planned head/tail truncation to the retained
// text parts together: slices keep the classification of the part they came from
// and the generated notice becomes its own part (Rust
// `codex_context_fragments::truncate_annotated_text`).
func truncateUserTextParts(parts []ContentPart, kinds []string, maxTokens int) ([]ContentPart, []string) {
	combined := ""
	for _, part := range parts {
		combined += part.Text
	}
	truncation := utils.PlanTokenTruncation(combined, maxTokens)
	if truncation == nil {
		return parts, kinds
	}
	truncatedParts := make([]ContentPart, 0, len(parts)+2)
	truncatedKinds := make([]string, 0, len(parts)+2)
	noticePending := true
	offset := 0
	for index, part := range parts {
		length := len(part.Text)
		start := offset
		offset += length
		if offset <= truncation.RemovedStart || start >= truncation.RemovedEnd {
			truncatedParts = append(truncatedParts, part)
			truncatedKinds = append(truncatedKinds, kinds[index])
			continue
		}
		prefixEnd := truncation.RemovedStart - start
		if prefixEnd < 0 {
			prefixEnd = 0
		}
		suffixStart := truncation.RemovedEnd - start
		if suffixStart > length {
			suffixStart = length
		}
		if suffixStart < 0 {
			suffixStart = 0
		}
		if prefixEnd > 0 {
			truncatedParts = append(truncatedParts, ContentPart{Type: part.Type, Text: part.Text[:prefixEnd]})
			truncatedKinds = append(truncatedKinds, kinds[index])
		}
		if noticePending {
			truncatedParts = append(truncatedParts, ContentPart{Type: "input_text", Text: truncation.Marker})
			truncatedKinds = append(truncatedKinds, unknownContentKind)
			noticePending = false
		}
		if suffixStart < length {
			truncatedParts = append(truncatedParts, ContentPart{Type: part.Type, Text: part.Text[suffixStart:]})
			truncatedKinds = append(truncatedKinds, kinds[index])
		}
	}
	return truncatedParts, truncatedKinds
}

// markRetainedSourcesIncomplete mirrors Rust's
// `CodexHarnessMetadata::mark_retained_sources_incomplete` call in
// `build_compacted_history_with_limit`: a shortened message no longer proves
// delivery of a complete original.
func markRetainedSourcesIncomplete(item *Item) {
	if item == nil || item.Data == nil {
		return
	}
	raw, ok := item.Data["harness_metadata"]
	if !ok {
		return
	}
	var metadata retainedctx.HarnessMetadata
	switch value := raw.(type) {
	case json.RawMessage:
		if json.Unmarshal(value, &metadata) != nil {
			return
		}
	case string:
		if strings.TrimSpace(value) == "" || json.Unmarshal([]byte(value), &metadata) != nil {
			return
		}
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil || json.Unmarshal(encoded, &metadata) != nil {
			return
		}
	default:
		return
	}
	metadata.MarkRetainedSourcesIncomplete()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	item.Data["harness_metadata"] = json.RawMessage(encoded)
}

// contentItemKinds returns the positional classifications an item was annotated
// with (Rust's `internal_chat_message_metadata_passthrough.content_item_kinds`).
func contentItemKinds(data map[string]any) []string {
	if data == nil {
		return nil
	}
	switch value := data["content_item_kinds"].(type) {
	case []string:
		return value
	case []any:
		kinds := make([]string, 0, len(value))
		for _, entry := range value {
			if text, ok := entry.(string); ok {
				kinds = append(kinds, text)
			}
		}
		return kinds
	}
	return nil
}

// kindAt returns the classification recorded for a content index, defaulting to
// unknown the way Rust does for a missing historical entry.
func kindAt(kinds []string, index int) string {
	if index >= 0 && index < len(kinds) && strings.TrimSpace(kinds[index]) != "" {
		return kinds[index]
	}
	return unknownContentKind
}

// contentPartIsText reports whether one content part carries text, which is what
// a retained message keeps when it drops media.
func contentPartIsText(part ContentPart) bool {
	switch strings.ToLower(strings.TrimSpace(part.Type)) {
	case "", "text", "input_text", "output_text":
		return part.ImageURL == "" && part.AudioURL == "" && part.FileID == ""
	}
	return false
}

// eventmapContentItems adapts compact content parts to the shared event-mapping
// view so the media wrapper labels are skipped exactly as Rust does.
func eventmapContentItems(parts []ContentPart) []eventmap.ContentItem {
	items := make([]eventmap.ContentItem, len(parts))
	for index := range parts {
		items[index] = eventmap.ContentItem{
			Kind:     eventmap.ContentKind(strings.ToLower(strings.TrimSpace(parts[index].Type))),
			Text:     parts[index].Text,
			ImageURL: parts[index].ImageURL,
		}
	}
	return items
}

// partTexts flattens retained parts for the text view of the message, matching
// ItemText's rule of joining the non-empty parts with newlines.
func partTexts(parts []ContentPart) []string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

// contentPartsAreText reports whether every content part is text, which is what
// lets a retained message keep its original parts.
func contentPartsAreText(parts []ContentPart) bool {
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "", "text", "input_text", "output_text":
		default:
			return false
		}
		if part.ImageURL != "" || part.AudioURL != "" || part.FileID != "" {
			return false
		}
	}
	return true
}

func cloneItemData(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneItems(items []Item) []Item {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]Item, len(items))
	for i := range items {
		cloned[i] = items[i]
		cloned[i].Content = cloneContentParts(items[i].Content)
		cloned[i].Data = cloneAnyMap(items[i].Data)
		cloned[i].Raw = append(json.RawMessage(nil), items[i].Raw...)
	}
	return cloned
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]ContentPart, len(parts))
	for i := range parts {
		cloned[i] = parts[i]
		if parts[i].Detail != nil {
			value := *parts[i].Detail
			cloned[i].Detail = &value
		}
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func truncateTextToTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	text = strings.TrimSpace(text)
	if EstimateTextTokens(text) <= maxTokens {
		return text
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := low + (high-low+1)/2
		if EstimateTextTokens(string(runes[:mid])) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.TrimSpace(string(runes[:low]))
}

func defaultWindowID(threadID string, suffix any) string {
	if threadID == "" {
		threadID = "thread"
	}
	return fmt.Sprintf("%s:%v", threadID, suffix)
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
