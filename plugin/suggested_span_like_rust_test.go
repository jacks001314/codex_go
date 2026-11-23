package plugin

import (
	"context"
	"testing"

	"codex_go/tracehook"
)

// recordingSpan captures the child spans the recommendation path opens so a
// test can compare them with Rust's instrumented function names.
type recordingSpan struct {
	names *[]string
	ended *int
}

func (s recordingSpan) AddEvent(string, map[string]string) {}
func (s recordingSpan) SetAttribute(string, string)        {}

func (s recordingSpan) End() {
	if s.ended != nil {
		*s.ended++
	}
}

func (s recordingSpan) StartChildSpan(name string, _ map[string]string) tracehook.Span {
	*s.names = append(*s.names, name)
	return s
}

// countingSuggestedPluginProvider answers the recommendation catalog read so
// the span assertions cover the remote fetch.
type countingSuggestedPluginProvider struct {
	catalogCalls int
}

func (p *countingSuggestedPluginProvider) ListSuggestedPlugins(context.Context) (*SuggestedPluginList, error) {
	p.catalogCalls++
	return &SuggestedPluginList{Enabled: true}, nil
}

// Mirrors Rust #62326 and #62610: the recommendation load opens a named
// info-level span for the catalog fetch; polling for a tooling marker is gone.
func TestSuggestedPluginListOpensRecommendationSpansLikeRust(t *testing.T) {
	provider := &countingSuggestedPluginProvider{}
	service := NewPluginService()
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")
	var names []string
	ended := 0
	ctx := tracehook.WithSpan(context.Background(), recordingSpan{names: &names, ended: &ended})
	if _, err := service.suggestedPluginList(ctx); err != nil {
		t.Fatalf("suggestedPluginList() error = %v", err)
	}
	if provider.catalogCalls != 1 {
		t.Fatalf("recommendation reads = catalog %d, want one", provider.catalogCalls)
	}
	want := []string{RecommendationsFetchCatalogSpanName}
	if len(names) != len(want) {
		t.Fatalf("recommendation spans = %#v, want %#v", names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("recommendation spans = %#v, want %#v", names, want)
		}
	}
	if ended != len(want) {
		t.Fatalf("ended spans = %d, want %d", ended, len(want))
	}
}
