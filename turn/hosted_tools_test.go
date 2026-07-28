package turn

import (
	"testing"

	"codex_go/codexapi"
)

func TestHostedWebSearchToolMatchesRustModes(t *testing.T) {
	if got := HostedWebSearchTool(codexapi.WebSearchModeDisabled, nil, "text"); got != nil {
		t.Fatalf("disabled tool = %#v", got)
	}
	cached := HostedWebSearchTool(codexapi.WebSearchModeCached, nil, "text")
	if cached["type"] != "web_search" || cached["external_web_access"] != false {
		t.Fatalf("cached tool = %#v", cached)
	}
	indexed := HostedWebSearchTool(codexapi.WebSearchModeIndexed, nil, "text")
	if indexed["external_web_access"] != true || indexed["indexed_web_access"] != true {
		t.Fatalf("indexed tool = %#v", indexed)
	}
	live := HostedWebSearchTool(codexapi.WebSearchModeLive, &codexapi.SearchSettings{
		SearchContextSize: searchContextSizePtr(codexapi.SearchContextHigh),
		Filters:           &codexapi.SearchFilters{AllowedDomains: []string{"example.com"}},
	}, "text_and_image")
	if live["external_web_access"] != true || live["search_context_size"] != codexapi.SearchContextHigh {
		t.Fatalf("live tool = %#v", live)
	}
	contentTypes, ok := live["search_content_types"].([]string)
	if !ok || len(contentTypes) != 2 {
		t.Fatalf("search_content_types = %#v", live["search_content_types"])
	}
}

func searchContextSizePtr(value codexapi.SearchContextSize) *codexapi.SearchContextSize {
	return &value
}
