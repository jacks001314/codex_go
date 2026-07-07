package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const modelsEndpointETagHeader = "X-Models-ETag"
const modelsEndpointClientVersion = "0.0.0"
const modelsEndpointRefreshTimeout = 5 * time.Second

type ModelsEndpoint interface {
	ListModels(ctx context.Context, etag string) (*ModelsEndpointResponse, error)
}

type ModelsEndpointResponse struct {
	Models      []ModelInfo
	ETag        string
	NotModified bool
}

type HTTPModelsEndpoint struct {
	Provider   *APIProvider
	Auth       *AuthHeaders
	HTTPClient HTTPDoer
}

func NewHTTPModelsEndpoint(provider *APIProvider, authHeaders *AuthHeaders, httpClient HTTPDoer) *HTTPModelsEndpoint {
	return &HTTPModelsEndpoint{
		Provider:   cloneAPIProvider(provider),
		Auth:       cloneAuthHeaders(authHeaders),
		HTTPClient: httpClient,
	}
}

func (e *HTTPModelsEndpoint) ListModels(ctx context.Context, etag string) (*ModelsEndpointResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := e.newModelsHTTPRequest(ctx, etag)
	if err != nil {
		return nil, err
	}
	client := e.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return &ModelsEndpointResponse{
			ETag:        modelsETagFromHeaders(response.Header),
			NotModified: true,
		}, nil
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("models API request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	models, err := decodeModelsEndpointResponse(body)
	if err != nil {
		return nil, err
	}
	return &ModelsEndpointResponse{
		Models: models,
		ETag:   modelsETagFromHeaders(response.Header),
	}, nil
}

func (e *HTTPModelsEndpoint) newModelsHTTPRequest(ctx context.Context, etag string) (*http.Request, error) {
	provider := e.Provider
	if provider == nil {
		provider = &APIProvider{BaseURL: defaultResponsesEndpoint}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(provider), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if strings.TrimSpace(etag) != "" {
		request.Header.Set("If-None-Match", strings.TrimSpace(etag))
	}
	addHeaders(request.Header, provider.Headers)
	if e.Auth != nil {
		if err := e.Auth.Apply(ctx, request, nil); err != nil {
			return nil, err
		}
	}
	return request, nil
}

type RemoteModelsManager struct {
	mu                              sync.RWMutex
	remoteModels                    []ModelInfo
	etag                            string
	fetched                         bool
	endpoint                        ModelsEndpoint
	useRemoteCatalogAsSourceOfTruth bool
}

type RemoteModelsManagerOptions struct {
	ModelCatalog                    *ModelsResponse
	Endpoint                        ModelsEndpoint
	UseRemoteCatalogAsSourceOfTruth bool
}

func NewRemoteModelsManager(modelCatalog *ModelsResponse, endpoint ModelsEndpoint) *RemoteModelsManager {
	return NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: modelCatalog,
		Endpoint:     endpoint,
	})
}

func NewRemoteModelsManagerWithOptions(options *RemoteModelsManagerOptions) *RemoteModelsManager {
	if options == nil {
		options = &RemoteModelsManagerOptions{}
	}
	catalog := BundledModelsResponse()
	if options.ModelCatalog != nil {
		catalog = *options.ModelCatalog
	}
	return &RemoteModelsManager{
		remoteModels:                    cloneModelInfos(catalog.Models),
		endpoint:                        options.Endpoint,
		useRemoteCatalogAsSourceOfTruth: options.UseRemoteCatalogAsSourceOfTruth,
	}
}

func (m *RemoteModelsManager) ListModels(strategy RefreshStrategy) []ModelPreset {
	return BuildAvailableModels(m.RawModelCatalog(strategy).Models)
}

func (m *RemoteModelsManager) RawModelCatalog(strategy RefreshStrategy) ModelsResponse {
	m.refreshAvailableModels(strategy)
	return ModelsResponse{Models: m.GetRemoteModels()}
}

func (m *RemoteModelsManager) GetRemoteModels() []ModelInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneModelInfos(m.remoteModels)
}

func (m *RemoteModelsManager) GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string {
	availableModels := m.ListModels(strategy)
	if allowProviderModelFallback {
		if requestedModelIsAvailable(model, availableModels) {
			return model
		}
		return defaultModelFromAvailable(availableModels)
	}
	if model != "" {
		return model
	}
	return defaultModelFromAvailable(availableModels)
}

func (m *RemoteModelsManager) GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo {
	return ConstructModelInfoFromCandidates(model, m.GetRemoteModels(), config)
}

func (m *RemoteModelsManager) RefreshIfNewETag(etag string) {
	if m == nil {
		return
	}
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return
	}
	m.mu.RLock()
	current := m.etag
	m.mu.RUnlock()
	if current != "" && current == etag {
		return
	}
	m.refreshAvailableModels(RefreshOnline)
}

func (m *RemoteModelsManager) refreshAvailableModels(strategy RefreshStrategy) {
	if m == nil || m.endpoint == nil {
		return
	}
	switch strategy {
	case RefreshOnline:
		m.fetchAndUpdateModels()
	case RefreshOnlineIfUncached:
		m.mu.RLock()
		shouldFetch := !m.fetched
		m.mu.RUnlock()
		if shouldFetch {
			m.fetchAndUpdateModels()
		}
	}
}

func (m *RemoteModelsManager) fetchAndUpdateModels() {
	m.mu.RLock()
	etag := m.etag
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), modelsEndpointRefreshTimeout)
	defer cancel()
	response, err := m.endpoint.ListModels(ctx, etag)
	if err != nil || response == nil || response.NotModified {
		if response != nil && response.NotModified {
			m.mu.Lock()
			m.fetched = true
			if strings.TrimSpace(response.ETag) != "" {
				m.etag = strings.TrimSpace(response.ETag)
			}
			m.mu.Unlock()
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetched = true
	if len(response.Models) > 0 {
		if m.useRemoteCatalogAsSourceOfTruth && hasRemoteSourceOfTruthModel(response.Models) {
			m.remoteModels = cloneModelInfos(response.Models)
		} else {
			m.remoteModels = mergeModelInfos(m.remoteModels, response.Models)
		}
	}
	if strings.TrimSpace(response.ETag) != "" {
		m.etag = strings.TrimSpace(response.ETag)
	}
}

func hasRemoteSourceOfTruthModel(models []ModelInfo) bool {
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.Visibility), VisibilityList) {
			return true
		}
	}
	return false
}

func modelsURL(provider *APIProvider) string {
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultResponsesEndpoint
	}
	raw := baseURL + "/models"
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	if query.Get("client_version") == "" {
		query.Set("client_version", modelsEndpointClientVersion)
	}
	for key, value := range provider.QueryParams {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func modelsETagFromHeaders(headers http.Header) string {
	if value := strings.TrimSpace(headers.Get(modelsEndpointETagHeader)); value != "" {
		return value
	}
	return strings.TrimSpace(headers.Get("ETag"))
}

func decodeModelsEndpointResponse(data []byte) ([]ModelInfo, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil {
		if rawModels, ok := object["models"]; ok {
			var models []ModelInfo
			if err := json.Unmarshal(rawModels, &models); err != nil {
				return nil, err
			}
			return models, nil
		}
		if rawData, ok := object["data"]; ok {
			var models []ModelInfo
			if err := json.Unmarshal(rawData, &models); err != nil {
				return nil, err
			}
			return models, nil
		}
	}
	var models []ModelInfo
	if err := json.Unmarshal(data, &models); err == nil {
		return models, nil
	}
	return nil, fmt.Errorf("models API response did not contain models")
}
