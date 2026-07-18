package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const LMStudioDefaultOSSModel = "openai/gpt-oss-20b"

type LMStudioClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewLMStudioClient(baseURL string) *LMStudioClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://127.0.0.1:1234"
	}
	return &LMStudioClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

func (c *LMStudioClient) FetchModels(ctx context.Context) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []string `json:"models"`
	}
	if err := c.getJSON(ctx, "/v1/models", &payload); err != nil {
		return nil, err
	}
	models := append([]string(nil), payload.Models...)
	for _, item := range payload.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	return uniqueLMStudioStrings(models), nil
}

func (c *LMStudioClient) DownloadModel(context.Context, string) error {
	return nil
}

func (c *LMStudioClient) LoadModel(context.Context, string) error {
	return nil
}

func EnsureLMStudioOSSReady(ctx context.Context, client *LMStudioClient, model string) error {
	if client == nil {
		return fmt.Errorf("lmstudio client is nil")
	}
	if model == "" {
		model = LMStudioDefaultOSSModel
	}
	models, err := client.FetchModels(ctx)
	if err != nil {
		return nil
	}
	if !containsLMStudioModel(models, model) {
		if err := client.DownloadModel(ctx, model); err != nil {
			return err
		}
	}
	return client.LoadModel(ctx, model)
}

func (c *LMStudioClient) getJSON(ctx context.Context, path string, target any) error {
	if c == nil {
		return fmt.Errorf("lmstudio client is nil")
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("lmstudio returned status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func containsLMStudioModel(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func uniqueLMStudioStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
