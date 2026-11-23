package tool

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

// Rust #63440 builds the deferred-tool index only when the first search over a
// nonempty catalog needs it, so a turn that never calls `tool_search` pays
// nothing for tokenizing the catalog.
func TestToolSearchIndexBuildsOnFirstSearchLikeRust(t *testing.T) {
	var builds atomic.Int32
	previous := toolSearchIndexBuildHook
	toolSearchIndexBuildHook = func() { builds.Add(1) }
	t.Cleanup(func() { toolSearchIndexBuildHook = previous })

	handler := NewToolSearchHandler([]Spec{
		{Name: NamespacedName("drive", "create_doc"), Description: "Create a document", Exposure: ExposureDiscoverable},
		{Name: NamespacedName("mail", "send"), Description: "Send mail", Exposure: ExposureDiscoverable},
	})
	if handler.index != nil {
		t.Fatalf("constructing a handler built the index: %#v", handler.index)
	}
	if got := builds.Load(); got != 0 {
		t.Fatalf("constructor built the index %d times, want 0", got)
	}

	// An empty query never reaches the index, matching Rust's gate on a search
	// that actually needs it.
	if results := handler.search("   ", 5); results != nil {
		t.Fatalf("empty query returned %#v", results)
	}
	if builds.Load() != 0 {
		t.Fatal("an empty query built the index")
	}

	results := handler.search("document", 5)
	if len(results) != 1 || results[0].Name.Key() != "drive.create_doc" {
		t.Fatalf("search results = %#v", results)
	}
	if builds.Load() != 1 {
		t.Fatalf("first search built the index %d times, want 1", builds.Load())
	}
	if len(handler.index) != 2 {
		t.Fatalf("index length = %d, want one document per deferred spec", len(handler.index))
	}

	// A later search reuses the completed index.
	if results := handler.search("mail", 5); len(results) != 1 || results[0].Name.Key() != "mail.send" {
		t.Fatalf("second search results = %#v", results)
	}
	if builds.Load() != 1 {
		t.Fatalf("second search rebuilt the index: %d builds", builds.Load())
	}
}

// Concurrent first searches share one build, the same way Rust's build gate
// hands every waiting search the completed index.
func TestToolSearchConcurrentFirstSearchesShareOneBuildLikeRust(t *testing.T) {
	var builds atomic.Int32
	previous := toolSearchIndexBuildHook
	toolSearchIndexBuildHook = func() { builds.Add(1) }
	t.Cleanup(func() { toolSearchIndexBuildHook = previous })

	handler := NewToolSearchHandler([]Spec{
		{Name: NamespacedName("drive", "create_doc"), Description: "Create a document", Exposure: ExposureDiscoverable},
		{Name: NamespacedName("mail", "send"), Description: "Send mail", Exposure: ExposureDiscoverable},
	})

	const searchers = 8
	var wg sync.WaitGroup
	keys := make([]string, searchers)
	for i := 0; i < searchers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results := handler.search("document", 5)
			if len(results) == 1 {
				keys[index] = results[0].Name.Key()
			}
		}(i)
	}
	wg.Wait()
	for i, key := range keys {
		if key != "drive.create_doc" {
			t.Fatalf("searcher %d got %q, want drive.create_doc", i, key)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("concurrent first searches built the index %d times, want 1", got)
	}
}

// A canceled search still leaves a completed index behind, mirroring Rust's
// build continuing on its blocking worker after the initiating search stops.
func TestToolSearchBuildSurvivesCanceledSearchLikeRust(t *testing.T) {
	handler := NewToolSearchHandler([]Spec{{
		Name:        NamespacedName("drive", "create_doc"),
		Description: "Create a document",
		Exposure:    ExposureDiscoverable,
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output, err := handler.Execute(ctx, &Invocation{
		ToolName: PlainName(ToolSearchName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"query":"document"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result ToolSearchResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name.Key() != "drive.create_doc" {
		t.Fatalf("canceled search results = %#v", result.Tools)
	}
	if len(handler.index) != 1 {
		t.Fatalf("canceled search left no index: %#v", handler.index)
	}
}
