package skillprovider

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type staticProvider struct {
	catalog     Catalog
	listErr     error
	read        ReadResult
	readErr     error
	search      SearchResult
	searchErr   error
	readCalls   []ReadRequest
	searchCalls []SearchRequest
}

func (p *staticProvider) List(context.Context, ListQuery) (Catalog, error) {
	return p.catalog, p.listErr
}

func (p *staticProvider) Read(_ context.Context, request ReadRequest) (ReadResult, error) {
	p.readCalls = append(p.readCalls, request)
	return p.read, p.readErr
}

func (p *staticProvider) Search(_ context.Context, request SearchRequest) (SearchResult, error) {
	p.searchCalls = append(p.searchCalls, request)
	return p.search, p.searchErr
}

func TestRegistryCustomProvidersMatchRustRouting(t *testing.T) {
	kind := CustomSourceKind("private")
	authority := Authority{Kind: kind, ID: "tenant-1"}
	entry := CatalogEntry{PackageID: "private/pkg", Authority: authority, Name: "private-search", MainResource: "private/pkg/SKILL.md", Enabled: true, PromptVisible: true}
	first := &staticProvider{catalog: Catalog{Entries: []CatalogEntry{entry}}, readErr: errors.New("first read failed"), searchErr: errors.New("first search failed")}
	second := &staticProvider{
		catalog: Catalog{Entries: []CatalogEntry{entry}, Warnings: []string{"provider warning"}},
		read:    ReadResult{Resource: entry.MainResource, Contents: "body"},
		search:  SearchResult{Matches: []SearchMatch{{Resource: "private/pkg/ref.md", Title: "Ref"}}},
	}
	failing := &staticProvider{listErr: errors.New("temporary failure")}
	registry := NewRegistry(
		Source{Kind: kind, Label: "private-primary", Provider: first},
		Source{Kind: kind, Label: "private-secondary", Provider: second},
		Source{Kind: CustomSourceKind("broken"), Label: "broken", Provider: failing},
	)
	catalog := registry.ListCustom(context.Background(), ListQuery{TurnID: "turn-1"})
	if len(catalog.Entries) != 1 || !reflect.DeepEqual(catalog.Warnings, []string{"provider warning", "broken skills unavailable: temporary failure"}) {
		t.Fatalf("custom catalog = %#v", catalog)
	}
	read, err := registry.Read(context.Background(), ReadRequest{Authority: authority, PackageID: entry.PackageID, Resource: entry.MainResource})
	if err != nil || read.Contents != "body" || len(first.readCalls) != 1 || len(second.readCalls) != 1 {
		t.Fatalf("Read() = %#v err=%v calls=%d/%d", read, err, len(first.readCalls), len(second.readCalls))
	}
	search, err := registry.Search(context.Background(), SearchRequest{Authority: authority, PackageID: entry.PackageID, Query: "ref"})
	if err != nil || len(search.Matches) != 1 || len(first.searchCalls) != 1 || len(second.searchCalls) != 1 {
		t.Fatalf("Search() = %#v err=%v calls=%d/%d", search, err, len(first.searchCalls), len(second.searchCalls))
	}
	_, err = registry.Read(context.Background(), ReadRequest{Authority: Authority{Kind: CustomSourceKind("missing")}})
	if err == nil || err.Error() != "missing skill provider is not configured" {
		t.Fatalf("missing provider error = %v", err)
	}
}
