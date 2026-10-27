package state

import (
	"fmt"
	"strings"

	"codex_go/utils"
)

// ContextTarget selects the Guardian consumer whose transcript policy applies.
type ContextTarget int

const (
	// ContextTargetSync is the synchronous, stateful approval reviewer.
	ContextTargetSync ContextTarget = iota
	// ContextTargetAsync is the asynchronous, independent action scorer.
	ContextTargetAsync
)

// Default transcript entry and retention limits from Rust's
// guardian-context ContextProfile (#43806). The asynchronous profile lowers the
// per-entry message cap and total message budget while keeping the same tool
// limits and recent-entry window.
const (
	// MaxMessageEntryTokens caps one user, developer, assistant, or reasoning
	// entry before transcript retention.
	MaxMessageEntryTokens = 5_000
	// MaxToolEntryTokens caps one tool call or tool result entry.
	MaxToolEntryTokens = 1_000
	// MaxMessageTranscriptTokens budgets rendered conversation messages.
	MaxMessageTranscriptTokens = 20_000
	// MaxToolTranscriptTokens budgets rendered tool evidence separately so it
	// cannot crowd out the human conversation.
	MaxToolTranscriptTokens = 10_000
	// MaxRecentNonUserEntries bounds retained non-user entries.
	MaxRecentNonUserEntries = 40

	// AsyncMaxMessageEntryTokens is the asynchronous per-message cap.
	AsyncMaxMessageEntryTokens = 2_000
	// AsyncMaxMessageTranscriptTokens is the asynchronous message budget.
	AsyncMaxMessageTranscriptTokens = 10_000
)

// GuardianTranscriptOmissionNotice is emitted by the synchronous profile when
// retention drops at least one collected entry.
const GuardianTranscriptOmissionNotice = "Some conversation entries were omitted."

// TranscriptEntryLimits holds per-entry caps resolved for the current review.
type TranscriptEntryLimits struct {
	// MessageTokens caps user, developer, assistant, and reasoning entries.
	MessageTokens int
	// ToolTokens caps tool calls and ordinary tool outputs.
	ToolTokens int
	// NodeReplOutputTokens caps node-repl-backed tool outputs.
	NodeReplOutputTokens int
}

// TranscriptRetentionConfig holds aggregate limits for rendered transcript
// entries.
type TranscriptRetentionConfig struct {
	// MaxMessageTranscriptTokens budgets rendered message entries.
	MaxMessageTranscriptTokens int
	// MaxToolTranscriptTokens budgets rendered tool entries.
	MaxToolTranscriptTokens int
	// MaxRecentNonUserEntries bounds retained entries other than user messages.
	MaxRecentNonUserEntries int
}

// ConversationTranscriptOptions selects the evidence sources included in a
// collected transcript.
type ConversationTranscriptOptions struct {
	// IncludeToolCalls includes submitted tool calls.
	IncludeToolCalls bool
	// IncludeToolOutputs includes tool results.
	IncludeToolOutputs bool
	// IncludeReasoning includes plaintext reasoning evidence.
	IncludeReasoning bool
	// NativeMultiAgentNamespace preserves calls in this MAv2 namespace as
	// native transcript items for synchronous backend decryption. Empty keeps
	// the existing text transcript; "functions" selects the default namespace
	// (#57448).
	NativeMultiAgentNamespace string
}

// DefaultConversationTranscriptOptions mirrors Rust's default: tool calls and
// outputs are included while reasoning is excluded.
func DefaultConversationTranscriptOptions() ConversationTranscriptOptions {
	return ConversationTranscriptOptions{
		IncludeToolCalls:   true,
		IncludeToolOutputs: true,
		IncludeReasoning:   false,
	}
}

// ConversationTranscriptConfig combines evidence sources with per-entry limits.
type ConversationTranscriptConfig struct {
	// Options selects the evidence sources for this request.
	Options ConversationTranscriptOptions
	// EntryLimits applies before aggregate retention.
	EntryLimits TranscriptEntryLimits
}

// ContextProfile is the request-local Guardian transcript policy resolved from
// the consumer's model and configuration. Registry scope follows Target; source
// flags and per-entry caps apply before retention.
type ContextProfile struct {
	// Target selects the synchronous or asynchronous retention policy.
	Target ContextTarget
	// Transcript controls sources and per-entry caps.
	Transcript ConversationTranscriptConfig
	// Retention controls aggregate message, tool, and entry budgets.
	Retention TranscriptRetentionConfig
	// IncludeImages mirrors the consumer's transcript image opt-in.
	IncludeImages bool
}

// SynchronousContextProfile returns the existing synchronous defaults before
// the host resolves the REPL output cap.
func SynchronousContextProfile() ContextProfile {
	return ContextProfile{
		Target: ContextTargetSync,
		Transcript: ConversationTranscriptConfig{
			Options: DefaultConversationTranscriptOptions(),
			EntryLimits: TranscriptEntryLimits{
				MessageTokens:        MaxMessageEntryTokens,
				ToolTokens:           MaxToolEntryTokens,
				NodeReplOutputTokens: MaxToolEntryTokens,
			},
		},
		Retention: TranscriptRetentionConfig{
			MaxMessageTranscriptTokens: MaxMessageTranscriptTokens,
			MaxToolTranscriptTokens:    MaxToolTranscriptTokens,
			MaxRecentNonUserEntries:    MaxRecentNonUserEntries,
		},
		IncludeImages: false,
	}
}

// AsynchronousContextProfile returns the existing asynchronous defaults before
// model and local overrides.
func AsynchronousContextProfile() ContextProfile {
	return ContextProfile{
		Target: ContextTargetAsync,
		Transcript: ConversationTranscriptConfig{
			Options: DefaultConversationTranscriptOptions(),
			EntryLimits: TranscriptEntryLimits{
				MessageTokens:        AsyncMaxMessageEntryTokens,
				ToolTokens:           MaxToolEntryTokens,
				NodeReplOutputTokens: MaxToolEntryTokens,
			},
		},
		Retention: TranscriptRetentionConfig{
			MaxMessageTranscriptTokens: AsyncMaxMessageTranscriptTokens,
			MaxToolTranscriptTokens:    MaxToolTranscriptTokens,
			MaxRecentNonUserEntries:    MaxRecentNonUserEntries,
		},
		IncludeImages: true,
	}
}

// GuardianTranscriptKind classifies collected evidence for profile retention.
type GuardianTranscriptKind int

const (
	// GuardianTranscriptUser is a genuine user message; it is selected before
	// non-user evidence and always keeps the first entry as an anchor.
	GuardianTranscriptUser GuardianTranscriptKind = iota
	// GuardianTranscriptProtectedMessage is trusted developer context or a final
	// assistant answer.
	GuardianTranscriptProtectedMessage
	// GuardianTranscriptMessage is ordinary assistant commentary or reasoning.
	GuardianTranscriptMessage
	// GuardianTranscriptTool is a tool call or tool result.
	GuardianTranscriptTool
)

func (k GuardianTranscriptKind) defaultRole() string {
	switch k {
	case GuardianTranscriptUser:
		return "user"
	case GuardianTranscriptProtectedMessage, GuardianTranscriptMessage:
		return "assistant"
	case GuardianTranscriptTool:
		return "tool"
	default:
		return "assistant"
	}
}

// TranscriptEvidenceEntry is one collected, per-entry-bounded transcript entry
// before consumer rendering.
type TranscriptEvidenceEntry struct {
	// Kind selects the retention pool.
	Kind GuardianTranscriptKind
	// Role is the rendered role or descriptive tool label.
	Role string
	// Text is the bounded entry text.
	Text string
	// OriginalBytes records the pre-truncation text size for async truncation
	// observations.
	OriginalBytes int
	// Tokens overrides the rendered token cost. Native encrypted evidence uses
	// this because its payload cannot be rendered as text.
	Tokens int
}

// TranscriptTruncationObservation records evidence lost while rendering.
type TranscriptTruncationObservation struct {
	// Component names the evidence family.
	Component string
	// OriginalBytes is the pre-truncation size.
	OriginalBytes int
	// RetainedBytes is the rendered size after retention.
	RetainedBytes int
}

// RenderedTranscript is the profile-selected transcript slice plus omission and
// truncation observations. Indices refer to positions in the original input.
type RenderedTranscript struct {
	Items        []string
	Indices      []int
	OmissionNote string
	Truncations  []TranscriptTruncationObservation
}

// UserMessageCost is the rendered token cost and original position of a
// genuine user message.
type UserMessageCost struct {
	Index  int
	Tokens int
}

// UserMessageSelection contains selected original indices and their total
// rendered token cost.
type UserMessageSelection struct {
	Indices []int
	Tokens  int
}

// SelectUserMessages keeps the first and latest user messages, then fills from
// newest to oldest. The first message remains an anchor even when it exceeds
// the budget; later messages are included only when they fit.
func SelectUserMessages(messages []UserMessageCost, maxMessageTokens int) UserMessageSelection {
	if len(messages) == 0 {
		return UserMessageSelection{}
	}
	selection := UserMessageSelection{
		Indices: []int{messages[0].Index},
		Tokens:  messages[0].Tokens,
	}
	for i := len(messages) - 1; i >= 1; i-- {
		message := messages[i]
		if message.Tokens <= maxMessageTokens-selection.Tokens {
			selection.Indices = append(selection.Indices, message.Index)
			selection.Tokens += message.Tokens
		}
	}
	return selection
}

type transcriptProfileEntry struct {
	kind          GuardianTranscriptKind
	tokens        int
	text          string
	originalBytes int
	retainedBytes int
}

// RenderTranscript applies the profile's sync or async retention policy without
// advancing the host's full/delta cursor. The host supplies the collected slice
// and original offset; empty placeholders remain a host concern.
func (p ContextProfile) RenderTranscript(entries []TranscriptEvidenceEntry, entryNumberOffset int) RenderedTranscript {
	profileEntries := make([]transcriptProfileEntry, 0, len(entries))
	userMessages := make([]UserMessageCost, 0, len(entries))
	for index, entry := range entries {
		number := index + entryNumberOffset + 1
		role := strings.TrimSpace(entry.Role)
		if role == "" {
			role = entry.Kind.defaultRole()
		}
		suffix := ""
		if p.Target == ContextTargetAsync {
			suffix = "\n"
		}
		text := fmt.Sprintf("[%d] %s: %s%s", number, role, entry.Text, suffix)
		tokens := entry.Tokens
		if tokens <= 0 {
			tokens = utils.ApproxTokenCount(text)
		}
		profileEntries = append(profileEntries, transcriptProfileEntry{
			kind:          entry.Kind,
			tokens:        tokens,
			text:          text,
			originalBytes: entry.OriginalBytes,
			retainedBytes: len(entry.Text),
		})
		if entry.Kind == GuardianTranscriptUser {
			userMessages = append(userMessages, UserMessageCost{Index: index, Tokens: tokens})
		}
	}

	included := make([]bool, len(profileEntries))
	selection := SelectUserMessages(userMessages, p.Retention.MaxMessageTranscriptTokens)
	for _, index := range selection.Indices {
		included[index] = true
	}
	switch p.Target {
	case ContextTargetSync:
		p.retainSynchronous(profileEntries, selection.Tokens, included)
	case ContextTargetAsync:
		available := p.Retention.MaxMessageTranscriptTokens - selection.Tokens
		if available < 0 {
			available = 0
		}
		window := newTranscriptWindow(profileEntries, p.Retention, available)
		for index := range profileEntries {
			window.insert(index)
		}
		for _, index := range window.indices() {
			included[index] = true
		}
	}

	rendered := RenderedTranscript{
		Indices: make([]int, 0, len(profileEntries)),
		Items:   make([]string, 0, len(profileEntries)),
	}
	if p.Target == ContextTargetSync {
		for _, keep := range included {
			if !keep {
				rendered.OmissionNote = GuardianTranscriptOmissionNotice
				break
			}
		}
	}
	for index, entry := range profileEntries {
		if included[index] {
			rendered.Indices = append(rendered.Indices, index)
			rendered.Items = append(rendered.Items, entry.text)
		}
		if p.Target != ContextTargetAsync || entry.originalBytes <= 0 {
			continue
		}
		retainedBytes := 0
		if included[index] {
			retainedBytes = entry.retainedBytes
		}
		if entry.originalBytes > retainedBytes {
			rendered.Truncations = append(rendered.Truncations, TranscriptTruncationObservation{
				Component:     transcriptKindComponent(entry.kind),
				OriginalBytes: entry.originalBytes,
				RetainedBytes: retainedBytes,
			})
		}
	}
	return rendered
}

func (p ContextProfile) retainSynchronous(entries []transcriptProfileEntry, selectedTokens int, included []bool) {
	messageTokens := selectedTokens
	toolTokens := 0
	retainedNonUser := 0
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.kind == GuardianTranscriptUser ||
			retainedNonUser >= p.Retention.MaxRecentNonUserEntries {
			continue
		}
		var used *int
		limit := p.Retention.MaxMessageTranscriptTokens
		if entry.kind == GuardianTranscriptTool {
			used = &toolTokens
			limit = p.Retention.MaxToolTranscriptTokens
		} else {
			used = &messageTokens
		}
		if *used <= limit && entry.tokens <= limit-*used {
			included[index] = true
			retainedNonUser++
			*used += entry.tokens
		}
	}
}

func transcriptKindComponent(kind GuardianTranscriptKind) string {
	switch kind {
	case GuardianTranscriptUser:
		return "transcript_user"
	case GuardianTranscriptTool:
		return "transcript_tool"
	default:
		return "transcript_message"
	}
}

const minRecentToolEntries = 5

type transcriptEntryPool struct {
	indices []int
	head    int
	tokens  int
}

func (p *transcriptEntryPool) len() int {
	return len(p.indices) - p.head
}

func (p *transcriptEntryPool) push(index int, tokens int) {
	p.indices = append(p.indices, index)
	p.tokens += tokens
}

func (p *transcriptEntryPool) front() (int, bool) {
	if p.len() == 0 {
		return 0, false
	}
	return p.indices[p.head], true
}

func (p *transcriptEntryPool) popOldest(entries []transcriptProfileEntry) {
	if p.len() == 0 {
		return
	}
	index := p.indices[p.head]
	p.head++
	p.tokens -= entries[index].tokens
}

func (p *transcriptEntryPool) evictOldest(count int, entries []transcriptProfileEntry) {
	if count > p.len() {
		count = p.len()
	}
	for range count {
		p.popOldest(entries)
	}
}

func (p *transcriptEntryPool) all() []int {
	return p.indices[p.head:]
}

// transcriptWindow retains protected asynchronous messages and recent tools
// with cacheable chunked eviction. Entry pools preserve the asynchronous cache
// behavior and the five-entry tool reservation.
type transcriptWindow struct {
	entries            []transcriptProfileEntry
	messageTokenBudget int
	toolTokenBudget    int
	maxEntries         int
	userMessages       transcriptEntryPool
	protectedMessages  transcriptEntryPool
	ordinaryMessages   transcriptEntryPool
	tools              transcriptEntryPool
}

func newTranscriptWindow(entries []transcriptProfileEntry, config TranscriptRetentionConfig, messageTokenBudget int) *transcriptWindow {
	return &transcriptWindow{
		entries:            entries,
		messageTokenBudget: messageTokenBudget,
		toolTokenBudget:    config.MaxToolTranscriptTokens,
		maxEntries:         config.MaxRecentNonUserEntries,
	}
}

func (w *transcriptWindow) insert(index int) {
	entry := w.entries[index]
	if entry.kind == GuardianTranscriptUser ||
		w.maxEntries == 0 ||
		!w.makeRoomForTokenBudget(entry.kind, entry.tokens) ||
		!w.makeRoomForEntryLimit(entry.kind) {
		return
	}
	w.pool(entry.kind).push(index, entry.tokens)
}

func (w *transcriptWindow) indices() []int {
	indices := make([]int, 0, w.retainedCount())
	indices = append(indices, w.protectedMessages.all()...)
	indices = append(indices, w.ordinaryMessages.all()...)
	indices = append(indices, w.tools.all()...)
	return indices
}

func (w *transcriptWindow) retainedCount() int {
	return w.protectedMessages.len() + w.ordinaryMessages.len() + w.tools.len()
}

func (w *transcriptWindow) makeRoomForTokenBudget(kind GuardianTranscriptKind, tokens int) bool {
	tokenBudget := w.messageTokenBudget
	if kind == GuardianTranscriptTool {
		tokenBudget = w.toolTokenBudget
	}
	protectedTokens := 0
	if kind == GuardianTranscriptMessage {
		protectedTokens = w.protectedMessages.tokens
	}
	// Rejected commentary must not evict protected context it cannot fit beside.
	if protectedTokens > tokenBudget || tokens > tokenBudget-protectedTokens {
		return false
	}

	var evictionOrder []GuardianTranscriptKind
	switch kind {
	case GuardianTranscriptProtectedMessage:
		evictionOrder = []GuardianTranscriptKind{GuardianTranscriptMessage, GuardianTranscriptProtectedMessage}
	case GuardianTranscriptMessage:
		evictionOrder = []GuardianTranscriptKind{GuardianTranscriptMessage}
	case GuardianTranscriptTool:
		evictionOrder = []GuardianTranscriptKind{GuardianTranscriptTool}
	}
	for _, evictionKind := range evictionOrder {
		if w.hasTokenCapacity(kind, tokens, tokenBudget) {
			break
		}
		pool := w.pool(evictionKind)
		poolLen := pool.len()
		if poolLen == 0 {
			continue
		}
		pool.evictOldest((poolLen+1)/2, w.entries)
		for !w.hasTokenCapacity(kind, tokens, tokenBudget) && pool.len() > 0 {
			pool.popOldest(w.entries)
		}
	}
	return w.hasTokenCapacity(kind, tokens, tokenBudget)
}

func (w *transcriptWindow) makeRoomForEntryLimit(kind GuardianTranscriptKind) bool {
	if w.retainedCount() < w.maxEntries {
		return true
	}

	// Smaller windows still leave one slot available for protected context.
	minimumExistingTools := min(minRecentToolEntries, max(0, w.maxEntries-1))
	if kind == GuardianTranscriptTool && minimumExistingTools > 0 {
		minimumExistingTools--
	}
	removableTools := max(0, w.tools.len()-minimumExistingTools)
	removableCount := w.ordinaryMessages.len() + removableTools
	if removableCount > 0 {
		evidenceCount := w.ordinaryMessages.len() + w.tools.len()
		evictionCount := min((evidenceCount+1)/2, removableCount)
		for range evictionCount {
			messageFront, hasMessage := w.ordinaryMessages.front()
			toolFront, hasTool := w.tools.front()
			evictTool := false
			if hasTool && w.tools.len() > minimumExistingTools {
				if !hasMessage || toolFront <= messageFront {
					evictTool = true
				}
			}
			switch {
			case evictTool:
				w.tools.popOldest(w.entries)
			case hasMessage:
				w.ordinaryMessages.popOldest(w.entries)
			}
		}
		return true
	}

	if kind == GuardianTranscriptProtectedMessage || kind == GuardianTranscriptTool {
		count := w.protectedMessages.len()
		w.protectedMessages.evictOldest((count+1)/2, w.entries)
		return true
	}
	return false
}

func (w *transcriptWindow) hasTokenCapacity(kind GuardianTranscriptKind, tokens int, tokenBudget int) bool {
	retainedTokens := w.tools.tokens
	if kind != GuardianTranscriptTool {
		retainedTokens = w.protectedMessages.tokens + w.ordinaryMessages.tokens
	}
	return retainedTokens <= tokenBudget && tokens <= tokenBudget-retainedTokens
}

func (w *transcriptWindow) pool(kind GuardianTranscriptKind) *transcriptEntryPool {
	switch kind {
	case GuardianTranscriptProtectedMessage:
		return &w.protectedMessages
	case GuardianTranscriptMessage:
		return &w.ordinaryMessages
	case GuardianTranscriptTool:
		return &w.tools
	case GuardianTranscriptUser:
		// User entries are selected separately and never inserted into a pool.
		return &w.userMessages
	default:
		return &w.userMessages
	}
}
