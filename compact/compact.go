package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	SummaryPrefix = "We need continue from a compacted conversation summary:"

	TriggerAuto   Trigger = "auto"
	TriggerManual Trigger = "manual"

	ReasonContextWindowExceeded Reason = "contextWindowExceeded"
	ReasonTokenLimit            Reason = "tokenLimit"
	ReasonModelSwitch           Reason = "modelSwitch"
	ReasonUserRequested         Reason = "userRequested"

	PhasePreTurn        Phase = "preTurn"
	PhaseMidTurn        Phase = "midTurn"
	PhaseStandaloneTurn Phase = "standaloneTurn"

	StatusSkipped     Status = "skipped"
	StatusNeeded      Status = "needed"
	StatusCompleted   Status = "completed"
	StatusInterrupted Status = "interrupted"
	StatusFailed      Status = "failed"

	SourceLocal  Source = "local"
	SourceRemote Source = "remote"
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
	baseRemaining := limit - scopeTokens
	if baseRemaining < 0 {
		baseRemaining = 0
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
	ID      string
	Type    string
	Role    string
	Text    string
	Kind    string
	Content []ContentPart
	Data    map[string]any
	Created time.Time
}

type ContentPart struct {
	Type     string
	Text     string
	ImageURL string
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
	Usage       *Usage
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
	compacted := BuildCompactedHistory(nil, lastUserMessages(history, 1), summary)
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
	Runner               RemoteRunner
	MaxSummaryChars      int
	InitialContext       []Item
	InjectBeforeLastUser bool
	FallbackToLocal      bool
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
			result.NewHistory = ProcessRemoteHistory(result.NewHistory, options.InitialContext, options.InjectBeforeLastUser)
			if len(result.NewHistory) == 0 && strings.TrimSpace(result.Summary) != "" {
				result.NewHistory = BuildCompactedHistory(nil, lastUserMessages(requestHistoryForCompaction(request), 1), result.Summary)
				if options.InjectBeforeLastUser {
					result.NewHistory = InsertInitialContextBeforeLastUserOrSummary(result.NewHistory, options.InitialContext)
				}
			}
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
	case "agent_message", "compaction", "context_compaction":
		return true
	case "compaction_trigger":
		return false
	}
	if item.Type != "message" {
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

func FilterCompactedHistory(items []Item) []Item {
	filtered := make([]Item, 0, len(items))
	for _, item := range items {
		if ShouldKeepCompactedHistoryItem(item) {
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
		if item.Role == "user" && item.Kind != "compaction_summary" {
			insertAt = i
			break
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

func ProcessRemoteHistory(remote []Item, initialContext []Item, injectBeforeLastUser bool) []Item {
	filtered := FilterCompactedHistory(remote)
	if injectBeforeLastUser {
		return InsertInitialContextBeforeLastUserOrSummary(filtered, initialContext)
	}
	return filtered
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
		total += EstimateTextTokens(ItemText(&items[i]))
	}
	return total
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
		cost := EstimateTextTokens(ItemText(&items[i]))
		if cost == 0 {
			cost = 1
		}
		if used+cost > maxTokens && len(selected) > 0 {
			break
		}
		if used+cost > maxTokens {
			item := items[i]
			item.Text = truncateTextToTokens(ItemText(&items[i]), maxTokens-used)
			item.Content = nil
			item.Data = nil
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

func cloneItems(items []Item) []Item {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]Item, len(items))
	for i := range items {
		cloned[i] = items[i]
		cloned[i].Content = cloneContentParts(items[i].Content)
		cloned[i].Data = cloneAnyMap(items[i].Data)
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
