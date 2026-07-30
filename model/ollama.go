package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const OllamaDefaultOSSModel = "gpt-oss:20b"

type OllamaVersion struct {
	Major int
	Minor int
	Patch int
}

func ParseOllamaVersion(value string) (OllamaVersion, bool) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 3 {
		return OllamaVersion{}, false
	}
	major, err1 := strconv.Atoi(numberPrefix(parts[0]))
	minor, err2 := strconv.Atoi(numberPrefix(parts[1]))
	patch, err3 := strconv.Atoi(numberPrefix(parts[2]))
	if err1 != nil || err2 != nil || err3 != nil {
		return OllamaVersion{}, false
	}
	return OllamaVersion{Major: major, Minor: minor, Patch: patch}, true
}

func (v *OllamaVersion) String() string {
	if v == nil {
		return "0.0.0"
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v *OllamaVersion) Compare(other OllamaVersion) int {
	if v.Major != other.Major {
		return compareInt(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return compareInt(v.Minor, other.Minor)
	}
	return compareInt(v.Patch, other.Patch)
}

func OllamaMinResponsesVersion() OllamaVersion {
	return OllamaVersion{Major: 0, Minor: 13, Patch: 4}
}

func OllamaSupportsResponses(version OllamaVersion) bool {
	if version == (OllamaVersion{}) {
		return true
	}
	return version.Compare(OllamaMinResponsesVersion()) >= 0
}

type OllamaClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewOllamaClient(baseURL string) *OllamaClient {
	return NewOllamaClientWithHTTPClient(baseURL, nil)
}

func NewOllamaClientWithHTTPClient(baseURL string, shared *http.Client) *OllamaClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if shared != nil {
		cloned := *shared
		cloned.Timeout = 10 * time.Second
		client = &cloned
	}
	return &OllamaClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: client}
}

func (c *OllamaClient) FetchModels(ctx context.Context) ([]string, error) {
	var payload struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/tags", &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Models))
	for _, item := range payload.Models {
		if item.Model != "" {
			models = append(models, item.Model)
		} else if item.Name != "" {
			models = append(models, item.Name)
		}
	}
	return uniqueOllamaStrings(models), nil
}

func (c *OllamaClient) FetchVersion(ctx context.Context) (*OllamaVersion, error) {
	var payload struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, "/api/version", &payload); err != nil {
		return nil, err
	}
	version, ok := ParseOllamaVersion(payload.Version)
	if !ok {
		return nil, nil
	}
	return &version, nil
}

func (c *OllamaClient) Pull(context.Context, string) error {
	return nil
}

func EnsureOllamaOSSReady(ctx context.Context, client *OllamaClient, model string) error {
	if client == nil {
		return fmt.Errorf("ollama client is nil")
	}
	if model == "" {
		model = OllamaDefaultOSSModel
	}
	models, err := client.FetchModels(ctx)
	if err != nil {
		return nil
	}
	if !containsOllamaModel(models, model) {
		return client.Pull(ctx, model)
	}
	return nil
}

func EnsureOllamaResponsesSupported(ctx context.Context, client *OllamaClient) error {
	if client == nil {
		return fmt.Errorf("ollama client is nil")
	}
	version, err := client.FetchVersion(ctx)
	if err != nil || version == nil {
		return err
	}
	if OllamaSupportsResponses(*version) {
		return nil
	}
	min := OllamaMinResponsesVersion()
	return fmt.Errorf("Ollama %s is too old. Codex requires Ollama %s or newer", version.String(), min.String())
}

func (c *OllamaClient) getJSON(ctx context.Context, path string, target any) error {
	if c == nil {
		return fmt.Errorf("ollama client is nil")
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
		return fmt.Errorf("ollama returned status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func numberPrefix(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if char < '0' || char > '9' {
			break
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func compareInt(left int, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func containsOllamaModel(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func uniqueOllamaStrings(values []string) []string {
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
