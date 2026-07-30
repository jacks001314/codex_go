package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestToolSearchHandlerFindsDiscoverableTools(t *testing.T) {
	handler := NewToolSearchHandler([]Spec{
		{Name: PlainName("visible"), Description: "visible direct tool", Exposure: ExposureModelVisible},
		{
			Name:        NamespacedName("drive", "create_doc"),
			Description: "Create a Google Docs file",
			InputSchema: map[string]any{
				"properties": map[string]any{"title": map[string]any{"type": "string"}},
			},
			Search: &SearchInfo{
				Text:   "google drive docs create document title",
				Source: &SearchSourceInfo{Name: "Google Drive", Description: "Docs and Drive tools."},
			},
			Exposure: ExposureDiscoverable,
		},
		{
			Name:        NamespacedName("mail", "send"),
			Description: "Send an email",
			Search:      &SearchInfo{Text: "mail email send"},
			Exposure:    ExposureDiscoverable,
		},
	})

	output, err := handler.Execute(context.Background(), &Invocation{
		CallID:   "search-1",
		ToolName: PlainName(ToolSearchName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"query":"docs title","limit":1}`},
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result ToolSearchResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name.Key() != "drive.create_doc" {
		t.Fatalf("result = %#v", result)
	}
	if result.Tools[0].Exposure != "" || result.Tools[0].Search != nil {
		t.Fatalf("search output spec = %#v", result.Tools[0])
	}
}

func TestToolSearchHandlerSupportsToolSearchPayload(t *testing.T) {
	handler := NewToolSearchHandler([]Spec{{
		Name:     PlainName("calendar_create"),
		Search:   &SearchInfo{Text: "calendar event create"},
		Exposure: ExposureDiscoverable,
	}})
	output, err := handler.Execute(context.Background(), &Invocation{
		ToolName: PlainName(ToolSearchName),
		Payload:  Payload{Kind: PayloadToolSearch, Search: map[string]any{"q": "event", "limit": float64(2)}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.Body, "calendar_create") {
		t.Fatalf("Body = %q", output.Body)
	}
}

func TestToolSearchHandlerRejectsInvalidQuery(t *testing.T) {
	handler := NewToolSearchHandler(nil)
	_, err := handler.Execute(context.Background(), &Invocation{
		ToolName: PlainName(ToolSearchName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"query":" "}`},
	})
	if err == nil || !strings.Contains(err.Error(), "query must not be empty") {
		t.Fatalf("error = %v", err)
	}
	_, err = handler.Execute(context.Background(), &Invocation{
		ToolName: PlainName(ToolSearchName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"query":"x","limit":0}`},
	})
	if err == nil || !strings.Contains(err.Error(), "limit must be greater than zero") {
		t.Fatalf("error = %v", err)
	}
}

func TestToolSearchDescriptionDeduplicatesSources(t *testing.T) {
	description := BuildToolSearchDescription([]SearchSourceInfo{
		{Name: "Google Drive", Description: "Docs and Drive."},
		{Name: "Google Drive"},
		{Name: "docs"},
	}, 8)
	if !strings.Contains(description, "- Google Drive: Docs and Drive.") || !strings.Contains(description, "- docs") {
		t.Fatalf("description = %q", description)
	}
}

func TestToolSearchDescriptionOmitsSourcesWhenWorldStateAdvertisesThem(t *testing.T) {
	description := BuildToolSearchDescriptionWithOptions([]SearchSourceInfo{{Name: "Google Drive", Description: "Search files and documents."}}, 8, true)
	if strings.Contains(description, "following sources") || strings.Contains(description, "Google Drive") {
		t.Fatalf("description = %q", description)
	}
	if !strings.Contains(description, "use this tool (`tool_search`) to search") {
		t.Fatalf("description = %q", description)
	}
}

func TestToolSearchDescriptionBoundsAggregateSources(t *testing.T) {
	sources := make([]SearchSourceInfo, 0, 8)
	for index := 0; index < 8; index++ {
		sources = append(sources, SearchSourceInfo{
			Name:        fmt.Sprintf("source-%02d", index),
			Description: strings.Repeat("🦀", 300),
		})
	}
	description := BuildToolSearchDescription(sources, 8)
	const prefix = "You have access to tools from the following sources:\n"
	section := strings.SplitN(description, prefix, 2)
	if len(section) != 2 {
		t.Fatalf("description missing source section: %q", description)
	}
	sourceList := strings.SplitN(section[1], "\nSome of the tools may not have been provided", 2)[0]
	if len(sourceList) > maxToolSearchSourceDescriptionBytes {
		t.Fatalf("source list bytes = %d", len(sourceList))
	}
	for index := 0; index < 8; index++ {
		if !strings.Contains(sourceList, fmt.Sprintf("- source-%02d", index)) {
			t.Fatalf("source list omitted source-%02d: %q", index, sourceList)
		}
	}
}

func TestRegisterToolSearchFromRegistry(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("deferred"), Search: &SearchInfo{Text: "find me"}, Exposure: ExposureDiscoverable}, noopExecutor)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("visible"), Search: &SearchInfo{Text: "find me too"}, Exposure: ExposureModelVisible}, noopExecutor)); err != nil {
		t.Fatalf("register visible: %v", err)
	}
	if err := RegisterToolSearchFromRegistry(registry); err != nil {
		t.Fatalf("RegisterToolSearchFromRegistry() error = %v", err)
	}
	if _, ok := registry.Lookup(PlainName(ToolSearchName)); !ok {
		t.Fatal("tool_search not registered")
	}
	output, err := NewRouter(registry).Dispatch(context.Background(), &Invocation{
		CallID:   "search",
		ToolName: PlainName(ToolSearchName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"query":"find"}`},
	})
	if err != nil {
		t.Fatalf("Dispatch(tool_search) error = %v", err)
	}
	var result ToolSearchResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal search body %q: %v", output.Body, err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name.Key() != "deferred" {
		t.Fatalf("search result = %#v, body = %q", result.Tools, output.Body)
	}
}
