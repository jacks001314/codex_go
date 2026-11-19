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
func (s recordingSpan) SetAttribute(string, string)   {}

func (s recordingSpan) End() {
	if s.ended != nil {
		*s.ended++
	}
}

func (s recordingSpan) StartChildSpan(name string, _ map[string]string) tracehook.Span {
	*s.names = append(*s.names, name)
	return s
}

// countingStateSuggestedPluginProvider answers both recommendation reads so the
// span assertions cover the marker probe and the catalog fetch.
type countingStateSuggestedPluginProvider struct {
	catalogCalls int
	markerCalls  int
}

func (p *countingStateSuggestedPluginProvider) ListSuggestedPlugins(context.Context) (*SuggestedPluginList, error) {
	p.catalogCalls++
	return &SuggestedPluginList{Enabled: true}, nil
}

func (p *countingStateSuggestedPluginProvider) SuggestedPluginsChangedAt(context.Context) (string, bool, error) {
	p.markerCalls++
	return "marker-1", true, nil
}

// Mirrors Rust #62326: the recommendation load opens named info-level spans for
// reading the tooling marker and fetching the catalog.
func TestSuggestedPluginListOpensRecommendationSpansLikeRust(t *testing.T) {
	provider := &countingStateSuggestedPluginProvider{}
	service := NewPluginService()
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")
	var names []string
	ended := 0
	ctx := tracehook.WithSpan(context.Background(), recordingSpan{names: &names, ended: &ended})
	if _, err := service.suggestedPluginList(ctx); err != nil {
		t.Fatalf("suggestedPluginList() error = %v", err)
	}
	if provider.markerCalls != 1 || provider.catalogCalls != 1 {
		t.Fatalf("recommendation reads = marker %d catalog %d, want one each", provider.markerCalls, provider.catalogCalls)
	}
	want := []string{RecommendationsReadToolingMarkerSpanName, RecommendationsFetchCatalogSpanName}
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
