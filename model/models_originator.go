package model

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

// ModelCatalogOriginator identifies a caller whose model catalog and cache are
// isolated from the default catalog (Rust #56284).
type ModelCatalogOriginator string

const (
	ModelCatalogOriginatorWork        ModelCatalogOriginator = "codex_work_cca"
	ModelCatalogOriginatorWorkWeb     ModelCatalogOriginator = "codex_work_web"
	ModelCatalogOriginatorWorkDesktop ModelCatalogOriginator = "codex_work_desktop"
	ModelCatalogOriginatorWorkMobile  ModelCatalogOriginator = "codex_work_mobile"
	ModelCatalogOriginatorChatGPT     ModelCatalogOriginator = "chatgpt_cca"
)

// ParseModelCatalogOriginator returns the bounded set of originators migrated
// to CCA. Unsupported callers retain the default manager and catalog.
func ParseModelCatalogOriginator(originator string) (ModelCatalogOriginator, bool) {
	switch value := ModelCatalogOriginator(strings.TrimSpace(originator)); value {
	case ModelCatalogOriginatorWork,
		ModelCatalogOriginatorWorkWeb,
		ModelCatalogOriginatorWorkDesktop,
		ModelCatalogOriginatorWorkMobile,
		ModelCatalogOriginatorChatGPT:
		return value, true
	default:
		return "", false
	}
}

func (o ModelCatalogOriginator) String() string { return string(o) }

// OriginatorModelsManager is implemented by managers that can partition their
// catalog by thread originator. Rust's ModelsManager trait returns None for
// unsupported managers; Go models the same capability as an optional interface.
type OriginatorModelsManager interface {
	ForOriginator(originator string) (ModelsManager, bool)
}

// ModelsManagerForOriginator returns an isolated manager for a supported
// originator, or the supplied manager when this manager cannot partition.
func ModelsManagerForOriginator(manager ModelsManager, originator string) ModelsManager {
	scoped, ok := ModelsManagerForOriginatorWithOK(manager, originator)
	if !ok {
		return manager
	}
	return scoped
}

// ModelsManagerForOriginatorWithOK is ModelsManagerForOriginator with the
// distinction between an unsupported manager and a supported partition.
func ModelsManagerForOriginatorWithOK(manager ModelsManager, originator string) (ModelsManager, bool) {
	partitioned, ok := manager.(OriginatorModelsManager)
	if !ok {
		return nil, false
	}
	return partitioned.ForOriginator(originator)
}

// OriginatorModelsEndpoint is implemented by catalog clients that can send an
// originator-specific header while retaining the provider's other headers.
type OriginatorModelsEndpoint interface {
	ForOriginator(originator ModelCatalogOriginator) (ModelsEndpoint, bool)
}

func (e *HTTPModelsEndpoint) ForOriginator(originator ModelCatalogOriginator) (ModelsEndpoint, bool) {
	if e == nil {
		return nil, false
	}
	provider := cloneAPIProvider(e.Provider)
	if provider == nil {
		provider = &APIProvider{}
	}
	if provider.Headers == nil {
		provider.Headers = make(http.Header)
	}
	provider.Headers.Del("originator")
	provider.Headers.Set("originator", originator.String())
	return &HTTPModelsEndpoint{
		Provider:   provider,
		Auth:       cloneAuthHeaders(e.Auth),
		HTTPClient: e.HTTPClient,
		CatalogURL: e.CatalogURL,
	}, true
}

func modelsOriginatorCachePath(cachePath string, originator ModelCatalogOriginator) string {
	cachePath = strings.TrimSpace(cachePath)
	if cachePath == "" {
		return ""
	}
	extension := filepath.Ext(cachePath)
	base := strings.TrimSuffix(cachePath, extension)
	if extension == "" {
		extension = ".json"
	}
	return fmt.Sprintf("%s.%s%s", base, originator.String(), extension)
}

// apiKeyModelDiscoveryPolicy is shared by a manager and all of its originator
// partitions, matching Rust's Arc<AtomicBool> (#56284).
type apiKeyModelDiscoveryPolicy struct {
	mu      sync.Mutex
	enabled bool
}

func (p *apiKeyModelDiscoveryPolicy) get() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled
}

func (p *apiKeyModelDiscoveryPolicy) set(enabled bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.enabled = enabled
	p.mu.Unlock()
}
