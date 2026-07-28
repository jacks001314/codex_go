package turn

import (
	"bytes"
	"context"
	_ "embed"
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
	Originator      string
	TurnMetadata    string
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
	data := map[string]any{
		"content_items":             contentItems,
		"contains_external_context": true,
		"web_search":                true,
		"web_search_action":         webSearchCommandAction(&commands),
	}
	if response.Results != nil {
		data["web_search_results"] = append([]any(nil), response.Results...)
	}
	return &tool.Output{
		CallID:      invocation.CallID,
		ToolName:    invocation.ToolName,
		Success:     true,
		Body:        response.Output,
		LogPreview:  "[standalone web search output]",
		Data:        data,
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
	if originator := strings.TrimSpace(h.options.Originator); originator != "" && !strings.ContainsAny(originator, "\r\n") {
		req.Header.Set("originator", originator)
	}
	if metadata := strings.TrimSpace(h.options.TurnMetadata); metadata != "" && !strings.ContainsAny(metadata, "\r\n") {
		req.Header.Set(codexapi.ClientCodexTurnMetadataHeader, metadata)
	}
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

func WebSearchMaxOutputTokens(info *model.ModelInfo, configured *int) *uint64 {
	if configured != nil && *configured > 0 {
		value := uint64(*configured)
		return &value
	}
	if info == nil || info.TruncationPolicy.Limit <= 0 {
		return nil
	}
	limit := info.TruncationPolicy.Limit
	if info.TruncationPolicy.Mode == model.TruncationModeBytes {
		limit = (limit + 3) / 4
	}
	if limit <= 0 {
		return nil
	}
	value := uint64(limit)
	return &value
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
		"type": "object",
		"properties": map[string]any{
			"search_query": map[string]any{
				"type":        "array",
				"description": "Query the internet search engine for a given list of queries.",
				"items":       webSearchQuerySchema(),
			},
			"image_query": map[string]any{
				"type":        "array",
				"description": "Query the image search engine for a given list of queries.",
				"items":       webSearchQuerySchema(),
			},
			"open": map[string]any{
				"type":        "array",
				"description": "Open pages by reference id or URL.",
				"items": webSearchObjectSchema(map[string]any{
					"ref_id": webSearchStringSchema("Reference id or URL to open."),
					"lineno": webSearchIntegerSchema("Line number to position the page at."),
				}, "ref_id"),
			},
			"click": map[string]any{
				"type":        "array",
				"description": "Open links from previously opened pages.",
				"items": webSearchObjectSchema(map[string]any{
					"ref_id": webSearchStringSchema("Reference id containing the numbered link."),
					"id":     webSearchIntegerSchema("Numbered link id to open."),
				}, "id", "ref_id"),
			},
			"find": map[string]any{
				"type":        "array",
				"description": "Find text patterns in pages.",
				"items": webSearchObjectSchema(map[string]any{
					"ref_id":  webSearchStringSchema("Reference id or URL to search within."),
					"pattern": webSearchStringSchema("Text pattern to find."),
				}, "pattern", "ref_id"),
			},
			"screenshot": map[string]any{
				"type":        "array",
				"description": "Take screenshots of PDF pages.",
				"items": webSearchObjectSchema(map[string]any{
					"ref_id": webSearchStringSchema("Reference id or URL to screenshot."),
					"pageno": webSearchIntegerSchema("Zero-indexed PDF page number."),
				}, "pageno", "ref_id"),
			},
			"finance": map[string]any{
				"type":        "array",
				"description": "Look up prices for the given stock symbols.",
				"items": webSearchObjectSchema(map[string]any{
					"ticker": webSearchStringSchema("Ticker symbol to look up."),
					"type":   webSearchEnumSchema("Asset type to look up.", "equity", "fund", "crypto", "index"),
					"market": webSearchStringSchema(`ISO 3166-1 alpha-3 country code, "OTC", or "" for cryptocurrency.`),
				}, "ticker", "type"),
			},
			"weather": map[string]any{
				"type":        "array",
				"description": "Look up weather forecasts.",
				"items": webSearchObjectSchema(map[string]any{
					"location": webSearchStringSchema(`Location in "Country, Area, City" format.`),
					"start":    webSearchStringSchema("Start date in YYYY-MM-DD format. Defaults to today."),
					"duration": webSearchIntegerSchema("Number of days to return. Defaults to 7."),
				}, "location"),
			},
			"sports": map[string]any{
				"type":        "array",
				"description": "Look up sports schedules and standings.",
				"items": webSearchObjectSchema(map[string]any{
					"tool":      webSearchEnumSchema("Tool name for sports requests.", "sports"),
					"fn":        webSearchEnumSchema("Sports function to call.", "schedule", "standings"),
					"league":    webSearchEnumSchema("League to look up.", "nba", "wnba", "nfl", "nhl", "mlb", "epl", "ncaamb", "ncaawb", "ipl"),
					"team":      webSearchStringSchema("Team to look up, using the common 3 or 4 letter alias used in broadcasts."),
					"opponent":  webSearchStringSchema("Opponent to use with `team` when narrowing the lookup."),
					"date_from": webSearchStringSchema("Start date in YYYY-MM-DD format."),
					"date_to":   webSearchStringSchema("End date in YYYY-MM-DD format."),
					"num_games": webSearchIntegerSchema("Number of games to return."),
					"locale":    webSearchStringSchema("Locale for the lookup."),
				}, "fn", "league"),
			},
			"time": map[string]any{
				"type":        "array",
				"description": "Get time for the given UTC offsets.",
				"items": webSearchObjectSchema(map[string]any{
					"utc_offset": webSearchStringSchema(`UTC offset formatted like "+03:00".`),
				}, "utc_offset"),
			},
			"response_length": map[string]any{
				"type":        "string",
				"description": "Set the length of the response to be returned.",
				"enum":        []any{"short", "medium", "long"},
			},
		},
	}
}

func webSearchQuerySchema() map[string]any {
	return webSearchObjectSchema(map[string]any{
		"q":       webSearchStringSchema("Search query."),
		"recency": webSearchIntegerSchema("Whether to filter by recency, as a number of recent days."),
		"domains": map[string]any{
			"type":        "array",
			"description": "Whether to filter by a specific list of domains.",
			"items":       webSearchStringSchema(""),
		},
	}, "q")
}

func webSearchObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) != 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

func webSearchStringSchema(description string) map[string]any {
	schema := map[string]any{"type": "string"}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func webSearchIntegerSchema(description string) map[string]any {
	schema := map[string]any{"type": "integer"}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func webSearchEnumSchema(description string, values ...string) map[string]any {
	enum := make([]any, 0, len(values))
	for _, value := range values {
		enum = append(enum, value)
	}
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        enum,
	}
}

//go:embed web_run_description.md
var webRunDescription string
