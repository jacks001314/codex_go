package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestToolSpecs(t *testing.T) {
	if ListMCPResourcesTool().Name != "list_mcp_resources" {
		t.Fatalf("unexpected list tool")
	}
	read := ReadMCPResourceTool()
	if len(read.Required) != 2 || read.Required[0] != "server" {
		t.Fatalf("unexpected read tool: %#v", read)
	}
}

func TestNormalizeListArgs(t *testing.T) {
	server := "  srv  "
	cursor := " "
	args := &ListMCPResourcesArgs{Server: &server, Cursor: &cursor}
	args.Normalize()
	if args.Server == nil || *args.Server != "srv" || args.Cursor != nil {
		t.Fatalf("args = %#v", args)
	}
}

func TestReadResourceArgsValidate(t *testing.T) {
	if err := (&ReadMCPResourceArgs{Server: " ", URI: "x"}).Validate(); !errors.Is(err, ErrInvalidMCPResourceArguments) {
		t.Fatalf("expected invalid args, got %v", err)
	}
}

func TestResourcesFromAllServersSorts(t *testing.T) {
	payload := MCPResourcesFromAllServers(map[string][]MCPResource{
		"b": {{URI: "b://1"}},
		"a": {{URI: "a://1"}},
	})
	if payload.Resources[0].Server != "a" || payload.Resources[1].Server != "b" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestResourceTemplateToolPayloadIncludesCompatibilityMetadata(t *testing.T) {
	encoded, err := json.Marshal(&MCPResourceTemplate{
		URITemplate: "file://{path}",
		Name:        "file",
		Meta:        map[string]any{"legacy": true},
	})
	if err != nil {
		t.Fatalf("Marshal resource template returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal resource template returned error: %v", err)
	}
	if meta, ok := payload["_meta"].(map[string]any); !ok || meta["legacy"] != true {
		t.Fatalf("resource template _meta missing: %#v", payload)
	}
	if _, ok := payload["icons"]; ok {
		t.Fatalf("resource template tool payload should not emit unsupported icons: %#v", payload)
	}
}

func TestParseArguments(t *testing.T) {
	value, ok, err := ParseMCPResourceArguments(`{"server":"srv"}`)
	if err != nil || !ok || value["server"] != "srv" {
		t.Fatalf("ParseMCPResourceArguments() = %#v %v %v", value, ok, err)
	}
}

func TestReadResourcePayloadFromResponseConvertsContents(t *testing.T) {
	payload := ReadResourcePayloadFromResponse("server", &MCPResourceReadResponse{Contents: []MCPResourceContent{{
		URI:      "file://a",
		MimeType: "text/plain",
		Text:     "hello",
		Meta:     map[string]any{"k": "v"},
	}}})
	if payload.Server != "server" || payload.URI != "file://a" || len(payload.Contents) != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	content, ok := payload.Contents[0].(map[string]any)
	if !ok || content["text"] != "hello" || content["_meta"].(map[string]any)["k"] != "v" {
		t.Fatalf("content = %#v", payload.Contents[0])
	}
}

func TestMCPResourceCacheClonesAndExpires(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewMCPResourceCache(&MCPResourceCacheOptions{
		TTL:        time.Minute,
		MaxEntries: 2,
		Now:        func() time.Time { return now },
	})
	key := &MCPResourceCacheKey{Server: " docs ", URI: " file://a "}
	cache.Write(key, &MCPResourceReadResponse{Contents: []MCPResourceContent{{
		URI:      "file://a",
		MimeType: "text/plain",
		Text:     "hello",
		Meta:     map[string]any{"nested": map[string]any{"value": "original"}},
	}}})

	first, ok := cache.Read(&MCPResourceCacheKey{Server: "docs", URI: "file://a"})
	if !ok || len(first.Contents) != 1 || first.Contents[0].Text != "hello" {
		t.Fatalf("first cache read = %#v, %v", first, ok)
	}
	first.Contents[0].Text = "mutated"
	first.Contents[0].Meta["nested"].(map[string]any)["value"] = "mutated"

	second, ok := cache.Read(&MCPResourceCacheKey{Server: "docs", URI: "file://a"})
	if !ok || second.Contents[0].Text != "hello" || second.Contents[0].Meta["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("cache leaked mutation: %#v", second)
	}

	now = now.Add(2 * time.Minute)
	if expired, ok := cache.Read(&MCPResourceCacheKey{Server: "docs", URI: "file://a"}); ok || expired != nil {
		t.Fatalf("expired cache read = %#v, %v", expired, ok)
	}
}

func TestSerializeFunctionOutputTruncates(t *testing.T) {
	got, err := SerializeMCPFunctionOutput(map[string]string{"text": strings.Repeat("x", 20)}, 12)
	if err != nil {
		t.Fatalf("SerializeMCPFunctionOutput() error = %v", err)
	}
	if len(got) != 12 || !strings.HasSuffix(got, "...") {
		t.Fatalf("unexpected output: %q", got)
	}
}
