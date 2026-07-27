package skillprovider

import (
	"context"
	"errors"
	"fmt"
)

type SourceKind string

const (
	SourceHost         SourceKind = "host"
	SourceExecutor     SourceKind = "executor"
	SourceOrchestrator SourceKind = "orchestrator"
)

func CustomSourceKind(kind string) SourceKind {
	return SourceKind(kind)
}

type Authority struct {
	Kind SourceKind
	ID   string
}

type CatalogEntry struct {
	PackageID        string
	Authority        Authority
	Name             string
	Description      string
	ShortDescription string
	MainResource     string
	DisplayPath      string
	Enabled          bool
	PromptVisible    bool
	Dependencies     []ToolDependency
}

type ToolDependency struct {
	Type      string
	Value     string
	Transport string
	Command   string
	URL       string
}

type Catalog struct {
	Entries  []CatalogEntry
	Warnings []string
}

func (c *Catalog) Extend(other Catalog) {
	if c == nil {
		return
	}
	for _, entry := range other.Entries {
		duplicate := false
		for _, existing := range c.Entries {
			if existing.Authority == entry.Authority && existing.PackageID == entry.PackageID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			c.Entries = append(c.Entries, entry)
		}
	}
	c.Warnings = append(c.Warnings, other.Warnings...)
}

type ListQuery struct {
	TurnID string
}

type ReadRequest struct {
	Authority Authority
	PackageID string
	Resource  string
}

type ReadResult struct {
	Resource string
	Contents string
}

type SearchRequest struct {
	Authority Authority
	PackageID string
	Query     string
}

type SearchMatch struct {
	Resource string
	Title    string
	Snippet  string
}

type SearchResult struct {
	Matches []SearchMatch
}

type Provider interface {
	List(context.Context, ListQuery) (Catalog, error)
	Read(context.Context, ReadRequest) (ReadResult, error)
	Search(context.Context, SearchRequest) (SearchResult, error)
}

type ProviderFuncs struct {
	ListFunc   func(context.Context, ListQuery) (Catalog, error)
	ReadFunc   func(context.Context, ReadRequest) (ReadResult, error)
	SearchFunc func(context.Context, SearchRequest) (SearchResult, error)
}

func (f ProviderFuncs) List(ctx context.Context, query ListQuery) (Catalog, error) {
	if f.ListFunc == nil {
		return Catalog{}, nil
	}
	return f.ListFunc(ctx, query)
}

func (f ProviderFuncs) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	if f.ReadFunc == nil {
		return ReadResult{}, errors.New("skill provider read is not supported")
	}
	return f.ReadFunc(ctx, request)
}

func (f ProviderFuncs) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if f.SearchFunc == nil {
		return SearchResult{}, nil
	}
	return f.SearchFunc(ctx, request)
}

type Source struct {
	Kind     SourceKind
	Label    string
	Provider Provider
}

type Registry struct {
	sources []Source
}

func NewRegistry(sources ...Source) *Registry {
	registry := &Registry{}
	for _, source := range sources {
		registry.Add(source)
	}
	return registry
}

func (r *Registry) Add(source Source) {
	if r == nil || source.Provider == nil {
		return
	}
	r.sources = append(r.sources, source)
}

func (r *Registry) ListCustom(ctx context.Context, query ListQuery) Catalog {
	var catalog Catalog
	if r == nil {
		return catalog
	}
	for _, source := range r.sources {
		if source.Kind == SourceHost || source.Kind == SourceExecutor || source.Kind == SourceOrchestrator {
			continue
		}
		listed, err := source.Provider.List(contextOrBackground(ctx), query)
		if err != nil {
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("%s skills unavailable: %s", source.Label, err))
			continue
		}
		catalog.Extend(listed)
	}
	return catalog
}

func (r *Registry) ListKind(ctx context.Context, kind SourceKind, query ListQuery) Catalog {
	var catalog Catalog
	if r == nil {
		return catalog
	}
	for _, source := range r.sources {
		if source.Kind != kind {
			continue
		}
		listed, err := source.Provider.List(contextOrBackground(ctx), query)
		if err != nil {
			label := source.Label
			if label == "" {
				label = string(kind)
			}
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("%s skills unavailable: %s", label, err))
			continue
		}
		catalog.Extend(listed)
	}
	return catalog
}

func (r *Registry) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	if r == nil {
		return ReadResult{}, fmt.Errorf("%s skill provider is not configured", request.Authority.Kind)
	}
	var lastErr error
	found := false
	for _, source := range r.sources {
		if source.Kind != request.Authority.Kind {
			continue
		}
		found = true
		result, err := source.Provider.Read(contextOrBackground(ctx), request)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return ReadResult{}, lastErr
	}
	if !found {
		return ReadResult{}, fmt.Errorf("%s skill provider is not configured", request.Authority.Kind)
	}
	return ReadResult{}, errors.New("skill provider read failed")
}

func (r *Registry) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if r == nil {
		return SearchResult{}, fmt.Errorf("%s skill provider is not configured", request.Authority.Kind)
	}
	var lastErr error
	found := false
	for _, source := range r.sources {
		if source.Kind != request.Authority.Kind {
			continue
		}
		found = true
		result, err := source.Provider.Search(contextOrBackground(ctx), request)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return SearchResult{}, lastErr
	}
	if !found {
		return SearchResult{}, fmt.Errorf("%s skill provider is not configured", request.Authority.Kind)
	}
	return SearchResult{}, errors.New("skill provider search failed")
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
