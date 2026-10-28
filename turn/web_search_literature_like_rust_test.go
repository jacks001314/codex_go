package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/codexapi"
	"codex_go/tool"
)

func TestWebSearchSchemaGatesLiteratureSearchLikeRust(t *testing.T) {
	properties := func(schema map[string]any) map[string]any {
		typed, _ := schema["properties"].(map[string]any)
		return typed
	}
	unsupported := properties(webSearchCommandsSchema(false))
	if _, ok := unsupported[LiteratureSearchQueryToolName]; ok {
		t.Fatal("literature_search_query present without model support")
	}
	supported := properties(webSearchCommandsSchema(true))
	entry, ok := supported[LiteratureSearchQueryToolName].(map[string]any)
	if !ok {
		t.Fatal("literature_search_query missing with model support")
	}
	if entry["description"] != "Search for scientific papers. Open relevant papers and cite the primary sources you use." {
		t.Fatalf("description = %#v", entry["description"])
	}
}

func TestWebSearchRejectsLiteratureSearchWithoutCapabilityLikeRust(t *testing.T) {
	handler := NewWebSearchHandler(&WebSearchOptions{})
	invocation := &tool.Invocation{
		ToolName: tool.NamespacedName(WebSearchNamespace, WebSearchRunTool),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"literature_search_query":[{"q":"papers"}]}`,
		},
	}
	_, err := handler.Execute(context.Background(), invocation)
	if err == nil || !strings.Contains(err.Error(), "literature_search_query is unavailable for this model") {
		t.Fatalf("error = %v", err)
	}
}

func TestWebSearchActionReportsLiteratureQueriesLikeRust(t *testing.T) {
	// Rust #57542 falls back to literature queries in the same position as image
	// queries: after search_query.
	preferred := webSearchCommandAction(&codexapi.SearchCommands{
		SearchQuery:           []codexapi.SearchQuery{{Q: "regular"}},
		LiteratureSearchQuery: []codexapi.SearchQuery{{Q: "papers"}},
	})
	if preferred["type"] != "search" || preferred["query"] != "regular" {
		t.Fatalf("search_query should win: %#v", preferred)
	}
	literature := webSearchCommandAction(&codexapi.SearchCommands{
		LiteratureSearchQuery: []codexapi.SearchQuery{{Q: "papers"}, {Q: "more papers"}},
	})
	if literature["type"] != "search" {
		t.Fatalf("action = %#v", literature)
	}
	queries, _ := literature["queries"].([]string)
	if len(queries) != 2 || queries[0] != "papers" {
		t.Fatalf("queries = %#v", literature["queries"])
	}
	single := webSearchCommandAction(&codexapi.SearchCommands{
		LiteratureSearchQuery: []codexapi.SearchQuery{{Q: "papers"}},
	})
	if single["type"] != "search" || single["query"] != "papers" {
		t.Fatalf("single literature action = %#v", single)
	}
}
