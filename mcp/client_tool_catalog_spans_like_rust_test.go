package mcp

// Rust parity: codex-rs/codex-mcp/src/client_tool_catalog.rs (#62320) opens one
// span per catalog lock wait, the fetch, publication, and a snapshot read, so a
// slow refresh can be attributed to the phase that spent the time.

import (
	"errors"
	"strings"
	"testing"
)

type recordingCatalogTracer struct {
	names []string
	ends  int
}

func (r *recordingCatalogTracer) StartCatalogSpan(name string) func() {
	r.names = append(r.names, name)
	return func() { r.ends++ }
}

func installRecordingCatalogTracer(t *testing.T) *recordingCatalogTracer {
	t.Helper()
	tracer := &recordingCatalogTracer{}
	SetCatalogTracer(tracer)
	t.Cleanup(func() { SetCatalogTracer(nil) })
	return tracer
}

func assertCatalogSpans(t *testing.T, tracer *recordingCatalogTracer, want []string) {
	t.Helper()
	if strings.Join(tracer.names, "|") != strings.Join(want, "|") {
		t.Fatalf("spans = %v, want %v", tracer.names, want)
	}
	if tracer.ends != len(want) {
		t.Fatalf("ended spans = %d, want %d", tracer.ends, len(want))
	}
}

func TestClientToolCatalogRefreshRecordsSpansLikeRust(t *testing.T) {
	tracer := installRecordingCatalogTracer(t)
	catalog := newClientToolCatalog(nil)
	if err := catalog.refresh(func() ([]MCPToolInfo, error) {
		return []MCPToolInfo{{Name: "alpha"}}, nil
	}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	catalog.read(func(uint64, []MCPToolInfo) {})
	assertCatalogSpans(t, tracer, []string{
		"mcp.catalog.refresh_lock_wait",
		"mcp.catalog.fetch",
		"mcp.catalog.active_calls_wait",
		"mcp.catalog.publication_lock_wait",
		"mcp.catalog.publish",
		"mcp.catalog.read_snapshot",
	})
}

func TestClientToolCatalogFailedFetchStopsBeforePublicationLikeRust(t *testing.T) {
	tracer := installRecordingCatalogTracer(t)
	catalog := newClientToolCatalog(nil)
	if err := catalog.refresh(func() ([]MCPToolInfo, error) {
		return nil, errors.New("list failed")
	}); err == nil {
		t.Fatal("refresh accepted a failed fetch")
	}
	assertCatalogSpans(t, tracer, []string{
		"mcp.catalog.refresh_lock_wait",
		"mcp.catalog.fetch",
	})
	if catalog.currentRevision() != 0 {
		t.Fatalf("failed fetch advanced the revision to %d", catalog.currentRevision())
	}
}

func TestClientToolCatalogSpansAreNoOpsWithoutTracerLikeRust(t *testing.T) {
	SetCatalogTracer(nil)
	catalog := newClientToolCatalog(nil)
	if err := catalog.refresh(func() ([]MCPToolInfo, error) {
		return []MCPToolInfo{{Name: "alpha"}}, nil
	}); err != nil {
		t.Fatalf("refresh without a tracer: %v", err)
	}
	catalog.read(func(uint64, []MCPToolInfo) {})
	if got := catalog.currentRevision(); got != 1 {
		t.Fatalf("revision = %d, want 1", got)
	}
}

func TestMCPToolPreparationRecordsSpansLikeRust(t *testing.T) {
	tracer := installRecordingCatalogTracer(t)
	statuses := []MCPServerStatus{{
		Name:  "docs",
		State: MCPServerReady,
		Tools: []MCPToolInfo{{Name: "search", Description: "Find a page"}},
	}}
	tools := RuntimeToolsFromStatuses(statuses)
	if len(tools) == 0 {
		t.Fatal("preparation dropped the ready server's tool")
	}
	want := []string{"mcp.tools.prepare_for_model", "mcp.binding.normalize_tools"}
	if got := tracer.names; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("spans = %v, want %v", got, want)
	}
	if tracer.ends != len(want) {
		t.Fatalf("ended spans = %d, want %d", tracer.ends, len(want))
	}
}
