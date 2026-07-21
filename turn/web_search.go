package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/utils"
)

const (
	WebSearchNamespace = "web"
	WebSearchRunTool   = "run"
)

type WebSearchOptions struct {
	SessionID       string
	Model           string
	Provider        model.APIProvider
	Auth            model.AuthHeaders
	HTTPClient      model.HTTPDoer
	InputItems      []any
	Settings        *codexapi.SearchSettings
	MaxOutputTokens *uint64
}

type WebSearchHandler struct {
	options WebSearchOptions
}

func NewWebSearchHandler(options *WebSearchOptions) *WebSearchHandler {
	if options == nil {
		options = &WebSearchOptions{}
	}
	return &WebSearchHandler{options: *options}
}

func (h *WebSearchHandler) Spec() tool.Spec {
	return tool.Spec{
		Name:                 tool.NamespacedName(WebSearchNamespace, WebSearchRunTool),
		Description:          webRunDescription,
		InputSchema:          webSearchCommandsSchema(),
		Exposure:             tool.ExposureModelVisible,
		Parallel:             true,
		NamespaceDescription: "Tool for accessing the internet.",
	}
}

func (h *WebSearchHandler) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", tool.ErrToolInvalidCall)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var commands codexapi.SearchCommands
	args := strings.TrimSpace(invocation.Payload.Arguments)
	if args == "" {
		args = "{}"
	}
	if err := json.Unmarshal([]byte(args), &commands); err != nil {
		return nil, err
	}
	response, err := h.search(ctx, &commands)
	if err != nil {
		return nil, err
	}
	contentItems := []FunctionCallOutputContentItem{{
		Type: "input_text",
		Text: response.Output,
	}}
	return &tool.Output{
		CallID:     invocation.CallID,
		ToolName:   invocation.ToolName,
		Success:    true,
		Body:       response.Output,
		LogPreview: response.Output,
		Data: map[string]any{
			"content_items":     contentItems,
			"web_search":        true,
			"web_search_action": webSearchCommandAction(&commands),
		},
		CompletedAt: time.Now().UTC(),
	}, nil
}

func (h *WebSearchHandler) search(ctx context.Context, commands *codexapi.SearchCommands) (*codexapi.SearchResponse, error) {
	requestPayload := codexapi.SearchRequest{
		ID:              strings.TrimSpace(h.options.SessionID),
		Model:           strings.TrimSpace(h.options.Model),
		Input:           cloneSearchInputItems(h.options.InputItems),
		Commands:        commands,
		Settings:        h.options.Settings,
		MaxOutputTokens: h.options.MaxOutputTokens,
	}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		return nil, err
	}
	endpoint := (&codexapi.Provider{
		Name:        h.options.Provider.Name,
		BaseURL:     h.options.Provider.BaseURL,
		QueryParams: h.options.Provider.QueryParams,
		Headers:     h.options.Provider.Headers,
	}).URLForPath("alpha/search")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	addHeaderValues(req.Header, h.options.Provider.Headers)
	signed, err := h.options.Auth.ApplyRequest(ctx, req, body)
	if err != nil {
		return nil, err
	}
	if signed != nil && signed.Body != nil {
		body = signed.Body
	}
	if req.Body == nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		req.ContentLength = int64(len(body))
	}
	client := h.options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("web search request failed: %s", message)
	}
	var decoded codexapi.SearchResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func webSearchCommandAction(commands *codexapi.SearchCommands) map[string]any {
	if commands == nil {
		return map[string]any{"type": "other"}
	}
	queries := webSearchQueries(commands.SearchQuery)
	if len(queries) == 0 {
		queries = webSearchQueries(commands.ImageQuery)
	}
	if len(queries) == 1 {
		return map[string]any{"type": "search", "query": queries[0], "queries": nil}
	}
	if len(queries) > 1 {
		return map[string]any{"type": "search", "query": nil, "queries": queries}
	}
	if len(commands.Open) > 0 {
		if urlString := literalWebSearchURL(commands.Open[0].RefID); urlString != "" {
			return map[string]any{"type": "openPage", "url": urlString}
		}
		return map[string]any{"type": "other"}
	}
	if len(commands.Find) > 0 {
		return map[string]any{
			"type":    "findInPage",
			"url":     nullableString(literalWebSearchURL(commands.Find[0].RefID)),
			"pattern": commands.Find[0].Pattern,
		}
	}
	return map[string]any{"type": "other"}
}

func webSearchQueries(values []codexapi.SearchQuery) []string {
	out := make([]string, 0, len(values))
	for i := range values {
		out = append(out, values[i].Q)
	}
	return out
}

func literalWebSearchURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || strings.TrimSpace(parsed.Scheme) == "" {
		return ""
	}
	return value
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func cloneSearchInputItems(items []any) any {
	if len(items) == 0 {
		return nil
	}
	items = recentSearchInputItems(items)
	if len(items) == 0 {
		return nil
	}
	data, err := json.Marshal(items)
	if err != nil {
		return append([]any(nil), items...)
	}
	var out []any
	if err := json.Unmarshal(data, &out); err != nil {
		return append([]any(nil), items...)
	}
	return out
}

// assistantContextTokenLimit mirrors Rust's ASSISTANT_CONTEXT_TOKEN_LIMIT: the
// shared token budget for assistant text across the whole recent-input window.
const assistantContextTokenLimit = 1000

func recentSearchInputItems(items []any) []any {
	messages := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if message, ok := visibleSearchMessage(item); ok {
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		return nil
	}
	start, end := recentSearchUserWindow(messages, 2)
	if start < 0 || end < start {
		return nil
	}
	out := make([]any, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, cloneMapAnyTurn(messages[i]))
	}
	out = truncateAssistantSearchMessagesToTokenBudget(out, assistantContextTokenLimit)
	if len(out) == 0 {
		return nil
	}
	return out
}

func visibleSearchMessage(item any) (map[string]any, bool) {
	message, ok := mapAnyTurn(item)
	if !ok {
		return nil, false
	}
	itemType, _ := message["type"].(string)
	role, _ := message["role"].(string)
	switch {
	case itemType == "message" && role == "assistant":
		content := textContentItems(message["content"], "output_text")
		if len(content) == 0 {
			return nil, false
		}
		return map[string]any{"type": "message", "role": "assistant", "content": content}, true
	case itemType == "agent_message":
		text := strings.TrimSpace(stringFromMapTurn(message, "text", "message"))
		if text == "" {
			return nil, false
		}
		author := strings.TrimSpace(stringFromMapTurn(message, "author"))
		if author != "" {
			text = "Agent message from " + author + ":\n" + text
		}
		return map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": text,
			}},
		}, true
	case itemType == "message" && role == "user":
		content := textContentItems(message["content"], "input_text")
		if len(content) == 0 || contextualSearchUserMessage(content) {
			return nil, false
		}
		return map[string]any{"type": "message", "role": "user", "content": content}, true
	default:
		return nil, false
	}
}

// recentSearchUserWindow mirrors Rust's retain_tail_from_last_n_user_messages:
// it keeps items through the latest user message and back to the earliest of
// the last userMessageCount user messages. If no user message is present, the
// whole window is dropped (matching Rust's items.clear()).
func recentSearchUserWindow(messages []map[string]any, userMessageCount int) (int, int) {
	if userMessageCount == 0 {
		return -1, -1
	}
	userIndexes := make([]int, 0, userMessageCount)
	for i := range messages {
		if role, _ := messages[i]["role"].(string); role == "user" {
			userIndexes = append(userIndexes, i)
		}
	}
	if len(userIndexes) == 0 {
		return -1, -1
	}
	end := userIndexes[len(userIndexes)-1]
	startIndex := len(userIndexes) - userMessageCount
	if startIndex < 0 {
		startIndex = 0
	}
	return userIndexes[startIndex], end
}

func textContentItems(value any, contentType string) []map[string]any {
	items, ok := sliceAnyTurn(value)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		content, ok := mapAnyTurn(item)
		if !ok {
			continue
		}
		itemType, _ := content["type"].(string)
		if itemType != contentType {
			continue
		}
		text, _ := content["text"].(string)
		out = append(out, map[string]any{"type": contentType, "text": text})
	}
	return out
}

func contextualSearchUserMessage(content []map[string]any) bool {
	if len(content) != 1 {
		return false
	}
	text, _ := content[0]["text"].(string)
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<environment_context>") && strings.Contains(text, "</environment_context>")
}

// truncateAssistantSearchMessagesToTokenBudget mirrors Rust's
// truncate_assistant_output_text_to_token_budget: it shares a single token
// budget across every assistant text block in the window, truncating (and
// eventually dropping) content once the budget is exhausted, and drops any
// assistant message left with no content.
func truncateAssistantSearchMessagesToTokenBudget(messages []any, maxTokens int) []any {
	remainingBudget := maxTokens
	out := make([]any, 0, len(messages))
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		if role, _ := message["role"].(string); role != "assistant" {
			out = append(out, item)
			continue
		}
		content, ok := sliceAnyTurn(message["content"])
		if !ok {
			out = append(out, item)
			continue
		}
		keptContent := make([]any, 0, len(content))
		for _, block := range content {
			entry, ok := mapAnyTurn(block)
			if !ok {
				keptContent = append(keptContent, block)
				continue
			}
			if blockType, _ := entry["type"].(string); blockType != "output_text" {
				keptContent = append(keptContent, entry)
				continue
			}
			if remainingBudget == 0 {
				continue
			}
			text, _ := entry["text"].(string)
			tokenCount := utils.ApproxTokenCount(text)
			if tokenCount <= remainingBudget {
				remainingBudget -= tokenCount
				keptContent = append(keptContent, entry)
				continue
			}
			entry["text"] = utils.TruncateText(text, utils.TokensPolicy(remainingBudget))
			remainingBudget = 0
			keptContent = append(keptContent, entry)
		}
		if len(keptContent) == 0 {
			continue
		}
		message["content"] = keptContent
		out = append(out, message)
	}
	return out
}

func mapAnyTurn(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, false
		}
		return out, true
	}
}

func sliceAnyTurn(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for i := range typed {
			out = append(out, typed[i])
		}
		return out, true
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var out []any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, false
		}
		return out, true
	}
}

func cloneMapAnyTurn(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		out := make(map[string]any, len(values))
		for key, value := range values {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		out := make(map[string]any, len(values))
		for key, value := range values {
			out[key] = value
		}
		return out
	}
	return out
}

func stringFromMapTurn(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func addHeaderValues(dst http.Header, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func webSearchCommandsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"search_query": map[string]any{
				"type":        "array",
				"description": "query internet search engine for a given list of queries",
				"items":       webSearchQuerySchema(),
			},
			"image_query": map[string]any{
				"type":        "array",
				"description": "query image search engine for a given list of queries",
				"items":       webSearchQuerySchema(),
			},
			"open": map[string]any{
				"type":        "array",
				"description": "Open the page indicated by ref_id.",
				"items":       webSearchObjectSchema(map[string]any{"ref_id": stringSchema(""), "lineno": nullableIntegerSchema()}),
			},
			"click": map[string]any{
				"type":        "array",
				"description": "Open the link id from the page indicated by ref_id.",
				"items":       webSearchObjectSchema(map[string]any{"ref_id": stringSchema(""), "id": map[string]any{"type": "integer", "minimum": 0}}),
			},
			"find": map[string]any{
				"type":        "array",
				"description": "Find the text pattern in the page indicated by ref_id.",
				"items":       webSearchObjectSchema(map[string]any{"ref_id": stringSchema(""), "pattern": stringSchema("")}),
			},
			"screenshot": map[string]any{
				"type":        "array",
				"description": "Take a screenshot of the page pageno indicated by ref_id.",
				"items":       webSearchObjectSchema(map[string]any{"ref_id": stringSchema(""), "pageno": map[string]any{"type": "integer", "minimum": 0}}),
			},
			"finance": map[string]any{
				"type":        "array",
				"description": "Look up prices for a given list of stock symbols.",
				"items": webSearchObjectSchema(map[string]any{
					"ticker": stringSchema(""),
					"type":   map[string]any{"type": "string", "enum": []any{"equity", "fund", "crypto", "index"}},
					"market": nullableStringSchema(),
				}),
			},
			"weather": map[string]any{
				"type":        "array",
				"description": "Look up weather for a given list of locations.",
				"items": webSearchObjectSchema(map[string]any{
					"location": stringSchema(""),
					"start":    nullableStringSchema(),
					"duration": nullableIntegerSchema(),
				}),
			},
			"sports": map[string]any{
				"type":        "array",
				"description": "Look up sports schedules and standings for games in a given league.",
				"items": webSearchObjectSchema(map[string]any{
					"tool":      map[string]any{"type": "string", "enum": []any{"sports"}},
					"fn":        map[string]any{"type": "string", "enum": []any{"schedule", "standings"}},
					"league":    map[string]any{"type": "string", "enum": []any{"nba", "wnba", "nfl", "nhl", "mlb", "epl", "ncaamb", "ncaawb", "ipl"}},
					"team":      nullableStringSchema(),
					"opponent":  nullableStringSchema(),
					"date_from": nullableStringSchema(),
					"date_to":   nullableStringSchema(),
					"num_games": nullableIntegerSchema(),
					"locale":    nullableStringSchema(),
				}),
			},
			"time": map[string]any{
				"type":        "array",
				"description": "Get time for the given UTC offsets.",
				"items":       webSearchObjectSchema(map[string]any{"utc_offset": stringSchema("")}),
			},
			"response_length": map[string]any{
				"type":        "string",
				"description": "The length of the response to be returned.",
				"enum":        []any{"short", "medium", "long"},
			},
		},
	}
}

func webSearchQuerySchema() map[string]any {
	return webSearchObjectSchema(map[string]any{
		"q":       stringSchema("Search query."),
		"recency": nullableIntegerSchema(),
		"domains": map[string]any{"type": []any{"array", "null"}, "items": stringSchema("")},
	})
}

func webSearchObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
}

func stringSchema(description string) map[string]any {
	schema := map[string]any{"type": "string"}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func nullableStringSchema() map[string]any {
	return map[string]any{"type": []any{"string", "null"}}
}

func nullableIntegerSchema() map[string]any {
	return map[string]any{"type": []any{"integer", "null"}, "minimum": 0}
}

const webRunDescription = `Tool for accessing the internet.

Use this when a current, external lookup is needed. Accepts search, image, open, click, find, screenshot, finance, weather, sports, time, and response_length commands.`
