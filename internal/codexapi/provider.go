package codexapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RetryConfig struct {
	MaxAttempts    uint64        `json:"maxAttempts"`
	BaseDelay      time.Duration `json:"baseDelay"`
	Retry429       bool          `json:"retry429"`
	Retry5xx       bool          `json:"retry5xx"`
	RetryTransport bool          `json:"retryTransport"`
}

type RetryPolicy struct {
	MaxAttempts uint64        `json:"maxAttempts"`
	BaseDelay   time.Duration `json:"baseDelay"`
	RetryOn     RetryOn       `json:"retryOn"`
}

type RetryOn struct {
	Retry429       bool `json:"retry429"`
	Retry5xx       bool `json:"retry5xx"`
	RetryTransport bool `json:"retryTransport"`
}

func (c *RetryConfig) ToPolicy() RetryPolicy {
	if c == nil {
		return RetryPolicy{}
	}
	return RetryPolicy{
		MaxAttempts: c.MaxAttempts,
		BaseDelay:   c.BaseDelay,
		RetryOn: RetryOn{
			Retry429:       c.Retry429,
			Retry5xx:       c.Retry5xx,
			RetryTransport: c.RetryTransport,
		},
	}
}

type Provider struct {
	Name              string            `json:"name"`
	BaseURL           string            `json:"baseUrl"`
	QueryParams       map[string]string `json:"queryParams,omitempty"`
	Headers           http.Header       `json:"headers,omitempty"`
	Retry             RetryConfig       `json:"retry"`
	StreamIdleTimeout time.Duration     `json:"streamIdleTimeout"`
}

type Request struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

func (p *Provider) URLForPath(path string) string {
	if p == nil {
		return path
	}
	base := strings.TrimRight(p.BaseURL, "/")
	trimmed := strings.TrimLeft(path, "/")
	result := base
	if trimmed != "" {
		result += "/" + trimmed
	}
	if len(p.QueryParams) == 0 {
		return result
	}
	values := url.Values{}
	for key, value := range p.QueryParams {
		values.Set(key, value)
	}
	separator := "?"
	if strings.Contains(result, "?") {
		separator = "&"
	}
	return result + separator + values.Encode()
}

func (p *Provider) BuildRequest(method string, path string) Request {
	return Request{Method: method, URL: p.URLForPath(path), Headers: cloneHeader(p.Headers)}
}

func (p *Provider) IsAzureResponsesEndpoint() bool {
	if p == nil {
		return false
	}
	return IsAzureResponsesProvider(p.Name, p.BaseURL)
}

func (p *Provider) WebsocketURLForPath(path string) (string, error) {
	raw := p.URLForPath(path)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}
	return parsed.String(), nil
}

func IsAzureResponsesProvider(name string, baseURL string) bool {
	if strings.EqualFold(name, "azure") {
		return true
	}
	lower := strings.ToLower(baseURL)
	for _, marker := range []string{
		"openai.azure.",
		"cognitiveservices.azure.",
		"aoai.azure.",
		"azure-api.",
		"azurefd.",
		"windows.net/openai",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func cloneHeader(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	cloned := http.Header{}
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
