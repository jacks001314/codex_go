package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultAppsMetadataProductSKU = "codex"

type ChatGPTMetadataProviderOptions struct {
	BaseURL    string
	Headers    http.Header
	ProductSKU string
	HTTPClient HTTPDoer
}

type ChatGPTMetadataProvider struct {
	baseURL    string
	headers    http.Header
	productSKU string
	httpClient HTTPDoer
}

func NewChatGPTMetadataProvider(options *ChatGPTMetadataProviderOptions) *ChatGPTMetadataProvider {
	provider := &ChatGPTMetadataProvider{
		baseURL:    DefaultChatGPTDirectoryBaseURL,
		headers:    http.Header{},
		productSKU: defaultAppsMetadataProductSKU,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
	if options == nil {
		return provider
	}
	provider.baseURL = normalizeChatGPTDirectoryBaseURL(options.BaseURL)
	provider.headers = cloneHTTPHeaderApps(options.Headers)
	if strings.TrimSpace(options.ProductSKU) != "" {
		provider.productSKU = strings.TrimSpace(options.ProductSKU)
	}
	if options.HTTPClient != nil {
		provider.httpClient = options.HTTPClient
	}
	return provider
}

func (p *ChatGPTMetadataProvider) ReadAppMetadata(params *AppMetadataReadParams) (*AppMetadataReadResponse, error) {
	if params == nil {
		params = &AppMetadataReadParams{}
	}
	target, err := url.Parse(strings.TrimRight(p.baseURL, "/") + "/ps/apps/batch")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		AppIDs       []string `json:"app_ids"`
		IncludeTools bool     `json:"include_tools"`
	}{AppIDs: append([]string(nil), params.AppIDs...), IncludeTools: params.IncludeTools})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range p.headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("OAI-Product-SKU", p.productSKU)
	response, err := p.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s failed: %s; body=%s", target.String(), response.Status, strings.TrimSpace(string(responseBody)))
	}
	var decoded struct {
		Apps []metadataApp `json:"apps"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode %s response: %w; body=%s", target.String(), err, string(responseBody))
	}
	requested := map[string]bool{}
	for _, id := range params.AppIDs {
		requested[id] = true
	}
	found := map[string]bool{}
	apps := make([]ConnectorMetadata, 0, len(decoded.Apps))
	for _, app := range decoded.Apps {
		if !requested[app.ID] || found[app.ID] {
			continue
		}
		found[app.ID] = true
		apps = append(apps, app.connectorMetadata(params.IncludeTools))
	}
	missing := make([]string, 0)
	for _, id := range params.AppIDs {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	return &AppMetadataReadResponse{Apps: apps, MissingAppIDs: missing}, nil
}

type metadataApp struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description *string           `json:"description"`
	IconURL     *string           `json:"icon_url"`
	Tools       []metadataAppTool `json:"tools"`
}

type metadataAppTool struct {
	Name           string  `json:"name"`
	Title          *string `json:"title"`
	Description    string  `json:"description"`
	IsEnabled      *bool   `json:"is_enabled"`
	DisabledReason *string `json:"disabled_reason"`
	IsReadOnly     bool    `json:"is_read_only"`
}

func (a metadataApp) connectorMetadata(includeTools bool) ConnectorMetadata {
	metadata := ConnectorMetadata{ID: a.ID, Name: a.Name, Description: cloneStringPtr(a.Description), IconURL: cloneStringPtr(a.IconURL), ToolsRequested: includeTools}
	if includeTools {
		metadata.ToolSummaries = make([]AppToolSummary, 0, len(a.Tools))
		for _, tool := range a.Tools {
			enabled := true
			if tool.IsEnabled != nil {
				enabled = *tool.IsEnabled
			}
			metadata.ToolSummaries = append(metadata.ToolSummaries, AppToolSummary{
				Name:           tool.Name,
				Title:          cloneStringPtr(tool.Title),
				Description:    tool.Description,
				IsEnabled:      enabled,
				DisabledReason: cloneStringPtr(tool.DisabledReason),
				IsReadOnly:     tool.IsReadOnly,
			})
		}
	}
	return metadata
}
