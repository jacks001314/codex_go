package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codex_go/chatgptapi"
	"codex_go/network"
)

// Rust parity: codex-rs/app-server-client/src/cloud_backend/thread_title.rs
// (#61600). Cloud titles are generated with the selected Cloud account's own
// backend routing and no agent tools, outside the conversation command queue.

const (
	cloudTitlePrompt        = "Generate a thread title from the user prompt."
	cloudTitleTimeout       = 30 * time.Second
	cloudTitleMaxBytes      = 8 * 1024
	cloudTitleStreamID      = "thread_title"
	cloudTitleResponsesPath = "/backend-api/codex/responses"
	cloudTitleAccountRoute  = "x-openai-account-routing-override"
)

// CloudTitleOptions configures the title generator used by Cloud conversations.
type CloudTitleOptions struct {
	// BaseURL is the configured ChatGPT base URL (chatgpt_base_url). It
	// bootstraps account routing discovery and acts as the fallback origin.
	BaseURL string
	// BearerToken and AccountID are the credentials that opened the Cloud
	// backend client.
	BearerToken string
	AccountID   string
	// RequireOriginMatch is set when BaseURL came from configuration, so a
	// discovered workspace origin must match the configured backend.
	RequireOriginMatch bool
	// Originator is stamped on the Responses request (originator header).
	Originator string
	// HTTPClient overrides the transport. nil uses http.DefaultClient.
	HTTPClient *http.Client
}

// CloudTitleClient generates Cloud conversation titles.
type CloudTitleClient struct {
	bootstrap          *url.URL
	bearerToken        string
	accountID          string
	requireOriginMatch bool
	originator         string
	httpClient         *http.Client
}

// NewCloudTitleClient validates the bootstrap routing URL. An invalid
// bootstrap disables Cloud title generation (Rust CloudTitleClient::new).
func NewCloudTitleClient(options CloudTitleOptions) (*CloudTitleClient, error) {
	bootstrap, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || !validCloudTitleBootstrap(bootstrap) {
		return nil, errors.New("invalid Cloud title routing bootstrap")
	}
	if strings.TrimSpace(options.BearerToken) == "" || strings.TrimSpace(options.AccountID) == "" {
		return nil, errors.New("Cloud title authentication is unavailable")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &CloudTitleClient{
		bootstrap:          bootstrap,
		bearerToken:        options.BearerToken,
		accountID:          strings.TrimSpace(options.AccountID),
		requireOriginMatch: options.RequireOriginMatch,
		originator:         strings.TrimSpace(options.Originator),
		httpClient:         httpClient,
	}, nil
}

// validCloudTitleBootstrap mirrors Rust's CloudTitleClient::new check: HTTPS,
// an allowed ChatGPT host, and no credentials, query, or fragment.
func validCloudTitleBootstrap(bootstrap *url.URL) bool {
	return bootstrap != nil &&
		bootstrap.Scheme == "https" &&
		network.IsAllowedChatGPTHost(bootstrap.Hostname()) &&
		bootstrap.User == nil &&
		bootstrap.RawQuery == "" &&
		bootstrap.Fragment == ""
}

// Generate asks the selected account's routed backend for a structured title
// and returns the assistant's final text (Rust CloudTitleClient::generate).
func (c *CloudTitleClient) Generate(ctx context.Context, modelID string, effort string, prompt string, schema map[string]any) (string, error) {
	if c == nil {
		return "", errors.New("Cloud title generation is unavailable")
	}
	runCtx, cancel := context.WithTimeout(ctx, cloudTitleTimeout)
	defer cancel()

	origin, routing, err := c.resolveRouting(runCtx)
	if err != nil {
		return "", err
	}
	body := cloudTitleRequestBody(modelID, effort, prompt, schema)
	response, err := c.postResponses(runCtx, origin, routing, body)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("Cloud title request failed")
	}
	return readCloudTitleStream(response.Body)
}

// resolveRouting discovers the selected account's workspace backend origin and
// routing override (Rust's accounts/check selection).
func (c *CloudTitleClient) resolveRouting(ctx context.Context) (string, string, error) {
	client := chatgptapi.NewCloudClient(&chatgptapi.CloudClientOptions{
		BaseURL:    c.bootstrap.String(),
		Headers:    c.authHeaders(),
		HTTPClient: c.httpClient,
	})
	accounts, err := client.GetAccountsCheck(ctx)
	if err != nil || accounts == nil {
		return "", "", errors.New("Cloud title routing is unavailable")
	}
	var selected *chatgptapi.AccountsCheckEntry
	count := 0
	for i := range accounts.Accounts {
		account := accounts.Accounts[i]
		if account.ID != c.accountID {
			continue
		}
		count++
		if count == 1 {
			candidate := account
			selected = &candidate
		}
	}
	if selected == nil || count != 1 || selected.IsDeactivated || selected.IsFedrampCompliant {
		return "", "", errors.New("Cloud title account is unavailable")
	}
	origin, err := c.resolveOrigin(selected)
	if err != nil {
		return "", "", err
	}
	routing := ""
	if selected.AccountRoutingOverride != nil {
		switch strings.TrimSpace(*selected.AccountRoutingOverride) {
		case "NO_CONSTRAINT":
		case "us", "us_cr":
			routing = strings.TrimSpace(*selected.AccountRoutingOverride)
		default:
			return "", "", errors.New("invalid Cloud title routing override")
		}
	}
	return origin, routing, nil
}

// resolveOrigin validates the discovered backend origin. NO_CONSTRAINT keeps
// the bootstrap origin; a configured backend must match it.
func (c *CloudTitleClient) resolveOrigin(account *chatgptapi.AccountsCheckEntry) (string, error) {
	if account.WorkspaceBackendOrigin == nil {
		return "", errors.New("Cloud title backend origin is unavailable")
	}
	value := strings.TrimSpace(*account.WorkspaceBackendOrigin)
	if value == "NO_CONSTRAINT" {
		return cloudTitleOrigin(c.bootstrap), nil
	}
	origin, err := url.Parse(value)
	if err != nil || !validCloudTitleOrigin(origin) {
		return "", errors.New("invalid Cloud title backend origin")
	}
	if c.requireOriginMatch && cloudTitleOrigin(origin) != cloudTitleOrigin(c.bootstrap) {
		return "", errors.New("invalid Cloud title backend origin")
	}
	return cloudTitleOrigin(origin), nil
}

// validCloudTitleOrigin accepts an HTTPS origin without credentials, a path,
// a query, or a fragment.
func validCloudTitleOrigin(origin *url.URL) bool {
	return origin != nil &&
		origin.Scheme == "https" &&
		network.IsAllowedChatGPTHost(origin.Hostname()) &&
		origin.User == nil &&
		(origin.Path == "" || origin.Path == "/") &&
		origin.RawQuery == "" &&
		origin.Fragment == ""
}

func cloudTitleOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host
}

func (c *CloudTitleClient) authHeaders() http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.bearerToken)
	headers.Set("chatgpt-account-id", c.accountID)
	return headers
}

// cloudTitleRequestBody mirrors Rust's title request body: no tools, no
// storage, and a strict JSON-schema output.
func cloudTitleRequestBody(modelID string, effort string, prompt string, schema map[string]any) map[string]any {
	turnMetadata, _ := json.Marshal(map[string]string{"thread_source": cloudTitleStreamID})
	body := map[string]any{
		"model":        modelID,
		"instructions": cloudTitlePrompt,
		"input": []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": prompt,
			}},
		}},
		"store":               false,
		"stream":              true,
		"tools":               []any{},
		"tool_choice":         "none",
		"parallel_tool_calls": false,
		"include":             []any{},
		"client_metadata":     map[string]any{"x-codex-turn-metadata": string(turnMetadata)},
		"text": map[string]any{"format": map[string]any{
			"type":   "json_schema",
			"strict": true,
			"schema": schema,
			"name":   "codex_output_schema",
		}},
	}
	if strings.TrimSpace(effort) != "" {
		body["reasoning"] = map[string]any{"effort": strings.TrimSpace(effort)}
	}
	return body
}

func (c *CloudTitleClient) postResponses(ctx context.Context, origin string, routing string, body map[string]any) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+cloudTitleResponsesPath, strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	for key, values := range c.authHeaders() {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if c.originator != "" && !strings.ContainsAny(c.originator, "\r\n") {
		request.Header.Set("originator", c.originator)
	}
	if routing != "" {
		request.Header.Set(cloudTitleAccountRoute, routing)
	}
	return c.httpClient.Do(request)
}

// readCloudTitleStream consumes the SSE reply and returns the final assistant
// message, ignoring commentary items (Rust's generate loop).
func readCloudTitleStream(reader io.Reader) (string, error) {
	parser := newResponsesSSEParser(reader)
	title := ""
	for {
		event, err := parser.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("Cloud title stream failed")
		}
		if event == nil {
			continue
		}
		eventType := strings.TrimSpace(event.Event)
		if eventType == "" {
			eventType = streamEventType(event.Data)
		}
		switch eventType {
		case "response.output_item.done":
			text, ok, err := cloudTitleItemText(event.Data)
			if err != nil {
				return "", err
			}
			if ok {
				title = text
			}
		case "response.completed":
			if title == "" {
				return "", errors.New("Cloud title response is empty")
			}
			return title, nil
		}
	}
	return "", errors.New("Cloud title stream ended before completion")
}

// cloudTitleItemText extracts the final assistant text from one
// response.output_item.done payload.
func cloudTitleItemText(data []byte) (string, bool, error) {
	var payload struct {
		Item struct {
			Type    string  `json:"type"`
			Role    string  `json:"role"`
			Phase   *string `json:"phase"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false, nil
	}
	if payload.Item.Type != "message" || payload.Item.Role != "assistant" {
		return "", false, nil
	}
	if payload.Item.Phase != nil && strings.EqualFold(strings.TrimSpace(*payload.Item.Phase), "commentary") {
		return "", false, nil
	}
	text := ""
	for _, item := range payload.Item.Content {
		if item.Type != "output_text" {
			continue
		}
		if len(text)+len(item.Text) > cloudTitleMaxBytes {
			return "", false, fmt.Errorf("Cloud title is too large")
		}
		text += item.Text
	}
	return text, true, nil
}
