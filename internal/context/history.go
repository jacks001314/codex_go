package context

import (
	"encoding/json"
	"strings"
	"sync"

	"codex_go/internal/eventmap"
)

const ImageOmittedPlaceholder = "image content omitted because you do not support image input"

type HistoryItem = eventmap.ResponseItem

type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type TokenUsageInfo struct {
	TotalTokenUsage    TokenUsage `json:"total_token_usage"`
	LastTokenUsage     TokenUsage `json:"last_token_usage"`
	ModelContextWindow *int64     `json:"model_context_window,omitempty"`
}

type HistoryManager struct {
	mu             sync.Mutex
	items          []HistoryItem
	historyVersion uint64
	tokenInfo      *TokenUsageInfo
}

func NewHistoryManager() *HistoryManager {
	return &HistoryManager{tokenInfo: &TokenUsageInfo{}}
}

func (m *HistoryManager) RecordItems(items ...HistoryItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range items {
		if !IsAPIMessage(&item) {
			continue
		}
		m.items = append(m.items, cloneItem(item))
	}
}

func (m *HistoryManager) RawItems() []HistoryItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneItems(m.items)
}

func (m *HistoryManager) Replace(items []HistoryItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = cloneItems(items)
	m.historyVersion++
}

func (m *HistoryManager) HistoryVersion() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.historyVersion
}

func (m *HistoryManager) RemoveFirstItem() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return
	}
	removed := m.items[0]
	m.items = m.items[1:]
	removeCorrespondingFor(&m.items, &removed)
	m.historyVersion++
}

func (m *HistoryManager) DropLastUserTurns(count uint32) {
	if count == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	positions := userTurnPositions(m.items)
	if len(positions) == 0 {
		return
	}
	n := int(count)
	cut := positions[0]
	if n < len(positions) {
		cut = positions[len(positions)-n]
	}
	m.items = cloneItems(m.items[:cut])
	m.historyVersion++
}

func (m *HistoryManager) ReplaceLastTurnImages(placeholder string) bool {
	if placeholder == "" {
		placeholder = ImageOmittedPlaceholder
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := len(m.items) - 1; index >= 0; index-- {
		item := &m.items[index]
		if item.Kind == eventmap.ResponseMessage && isUserTurnBoundary(item) {
			return false
		}
		if item.Kind == eventmap.ResponseOther && item.WebSearchAction == "function_call_output" {
			replaced := replaceImagesInContent(item.Content, placeholder)
			if replaced {
				m.historyVersion++
			}
			return replaced
		}
	}
	return false
}

func (m *HistoryManager) ForPrompt(supportsImages bool) []HistoryItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := cloneItems(m.items)
	EnsureCallOutputsPresent(&items)
	RemoveOrphanOutputs(&items)
	if !supportsImages {
		StripImages(&items, ImageOmittedPlaceholder)
	}
	return items
}

func (m *HistoryManager) EstimateTokenCount(baseInstructions string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := ApproxTokenCount(baseInstructions)
	for _, item := range m.items {
		total += EstimateItemTokens(&item)
	}
	return total
}

func (m *HistoryManager) UpdateTokenInfo(usage TokenUsage, contextWindow *int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokenInfo == nil {
		m.tokenInfo = &TokenUsageInfo{}
	}
	m.tokenInfo.LastTokenUsage = usage
	m.tokenInfo.TotalTokenUsage.InputTokens += usage.InputTokens
	m.tokenInfo.TotalTokenUsage.CachedInputTokens += usage.CachedInputTokens
	m.tokenInfo.TotalTokenUsage.OutputTokens += usage.OutputTokens
	m.tokenInfo.TotalTokenUsage.ReasoningOutputTokens += usage.ReasoningOutputTokens
	m.tokenInfo.TotalTokenUsage.TotalTokens += usage.TotalTokens
	if contextWindow != nil {
		value := *contextWindow
		m.tokenInfo.ModelContextWindow = &value
	}
}

func (m *HistoryManager) TokenInfo() *TokenUsageInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokenInfo == nil {
		return nil
	}
	cloned := *m.tokenInfo
	if m.tokenInfo.ModelContextWindow != nil {
		value := *m.tokenInfo.ModelContextWindow
		cloned.ModelContextWindow = &value
	}
	return &cloned
}

func IsAPIMessage(item *HistoryItem) bool {
	if item == nil {
		return false
	}
	switch item.Kind {
	case eventmap.ResponseMessage:
		return item.Role != "system"
	case eventmap.ResponseReasoning, eventmap.ResponseWebSearchCall, eventmap.ResponseImageGeneration, eventmap.ResponseOther:
		return true
	default:
		return false
	}
}

func EnsureCallOutputsPresent(items *[]HistoryItem) {
	if items == nil {
		return
	}
	outputs := map[string]bool{}
	for _, item := range *items {
		if item.Kind == eventmap.ResponseOther && item.WebSearchAction == "function_call_output" {
			outputs[item.ID] = true
		}
	}
	insertions := make([]insertion, 0)
	for index, item := range *items {
		if item.Kind != eventmap.ResponseOther || item.WebSearchAction != "function_call" || item.ID == "" || outputs[item.ID] {
			continue
		}
		insertions = append(insertions, insertion{Index: index + 1, Item: HistoryItem{
			Kind:            eventmap.ResponseOther,
			ID:              item.ID,
			WebSearchAction: "function_call_output",
			ImageResult:     "aborted",
		}})
	}
	for i := len(insertions) - 1; i >= 0; i-- {
		insert := insertions[i]
		*items = append((*items)[:insert.Index], append([]HistoryItem{insert.Item}, (*items)[insert.Index:]...)...)
	}
}

func RemoveOrphanOutputs(items *[]HistoryItem) {
	if items == nil {
		return
	}
	calls := map[string]bool{}
	for _, item := range *items {
		if item.Kind == eventmap.ResponseOther && item.WebSearchAction == "function_call" {
			calls[item.ID] = true
		}
	}
	filtered := (*items)[:0]
	for _, item := range *items {
		if item.Kind == eventmap.ResponseOther && item.WebSearchAction == "function_call_output" && item.ID != "" && !calls[item.ID] {
			continue
		}
		filtered = append(filtered, item)
	}
	*items = filtered
}

func StripImages(items *[]HistoryItem, placeholder string) {
	if items == nil {
		return
	}
	if placeholder == "" {
		placeholder = ImageOmittedPlaceholder
	}
	for index := range *items {
		replaceImagesInContent((*items)[index].Content, placeholder)
		if (*items)[index].Kind == eventmap.ResponseImageGeneration {
			(*items)[index].ImageResult = ""
		}
	}
}

func MergeContextualMessages(items []HistoryItem) []HistoryItem {
	out := make([]HistoryItem, 0, len(items))
	for _, item := range items {
		if len(out) == 0 || out[len(out)-1].Kind != eventmap.ResponseMessage || out[len(out)-1].Role != item.Role || item.Kind != eventmap.ResponseMessage {
			out = append(out, cloneItem(item))
			continue
		}
		out[len(out)-1].Content = append(out[len(out)-1].Content, item.Content...)
	}
	return out
}

func BuildTextMessage(role string, sections []string) *HistoryItem {
	content := make([]eventmap.ContentItem, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section) == "" {
			continue
		}
		content = append(content, eventmap.ContentItem{Kind: eventmap.ContentInputText, Text: section})
	}
	if len(content) == 0 {
		return nil
	}
	return &HistoryItem{Kind: eventmap.ResponseMessage, Role: role, Content: content}
}

func EstimateItemTokens(item *HistoryItem) int64 {
	if item == nil {
		return 0
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return 0
	}
	bytes := int64(len(raw))
	for _, content := range item.Content {
		if content.Kind == eventmap.ContentInputImage {
			bytes -= int64(len(content.ImageURL))
			bytes += 7373
		}
	}
	if bytes < 0 {
		bytes = 0
	}
	return ApproxTokensFromBytes(bytes)
}

func ApproxTokenCount(text string) int64 {
	return ApproxTokensFromBytes(int64(len(text)))
}

func ApproxTokensFromBytes(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

type insertion struct {
	Index int
	Item  HistoryItem
}

func isUserTurnBoundary(item *HistoryItem) bool {
	return item != nil && item.Kind == eventmap.ResponseMessage && item.Role == "user" && !eventmap.IsContextualUserMessageContent(item.Content)
}

func userTurnPositions(items []HistoryItem) []int {
	out := make([]int, 0)
	for i := range items {
		if isUserTurnBoundary(&items[i]) {
			out = append(out, i)
		}
	}
	return out
}

func removeCorrespondingFor(items *[]HistoryItem, removed *HistoryItem) {
	if items == nil || removed == nil || removed.ID == "" || removed.Kind != eventmap.ResponseOther {
		return
	}
	counterpart := ""
	switch removed.WebSearchAction {
	case "function_call":
		counterpart = "function_call_output"
	case "function_call_output":
		counterpart = "function_call"
	default:
		return
	}
	for i := range *items {
		if (*items)[i].Kind == eventmap.ResponseOther && (*items)[i].ID == removed.ID && (*items)[i].WebSearchAction == counterpart {
			*items = append((*items)[:i], (*items)[i+1:]...)
			return
		}
	}
}

func replaceImagesInContent(content []eventmap.ContentItem, placeholder string) bool {
	replaced := false
	for i := range content {
		if content[i].Kind == eventmap.ContentInputImage {
			content[i] = eventmap.ContentItem{Kind: eventmap.ContentInputText, Text: placeholder}
			replaced = true
		}
	}
	return replaced
}

func cloneItems(items []HistoryItem) []HistoryItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]HistoryItem, len(items))
	for i := range items {
		out[i] = cloneItem(items[i])
	}
	return out
}

func cloneItem(item HistoryItem) HistoryItem {
	cloned := item
	cloned.Content = append([]eventmap.ContentItem(nil), item.Content...)
	cloned.Summary = append([]string(nil), item.Summary...)
	cloned.RawContent = append([]string(nil), item.RawContent...)
	return cloned
}
