package codemode

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/tool"
)

// catalogRecordingTransport records the execute frames the client sends while
// delegating everything else to the shared framed transport.
type catalogRecordingTransport struct {
	conn   net.Conn
	reader *FramedReader
	writer *FramedWriter
	mu     sync.Mutex
	exec   []ExecuteRequest
}

func (t *catalogRecordingTransport) Read(_ context.Context, target any) (bool, error) {
	return t.reader.Read(target)
}

func (t *catalogRecordingTransport) Write(_ context.Context, message any) error {
	if client, ok := message.(ClientToHost); ok && client.Request != nil && client.Request.Method == "session/execute" && client.Request.Request != nil {
		request := *client.Request.Request
		request.EnabledTools = cloneProtocolToolDefinitions(request.EnabledTools)
		if request.CatalogRevision != nil {
			revision := *request.CatalogRevision
			request.CatalogRevision = &revision
		}
		t.mu.Lock()
		t.exec = append(t.exec, request)
		t.mu.Unlock()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writer.Write(message)
}

func (t *catalogRecordingTransport) Close() error { return t.conn.Close() }

func (t *catalogRecordingTransport) executions() []ExecuteRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]ExecuteRequest(nil), t.exec...)
}

func startCatalogTestHost(t *testing.T) (*remoteConnection, *catalogRecordingTransport) {
	t.Helper()
	hostSide, clientSide := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	hostDone := make(chan error, 1)
	go func() { hostDone <- RunStdioHost(ctx, hostSide, hostSide) }()
	t.Cleanup(func() {
		cancel()
		_ = clientSide.Close()
		select {
		case <-hostDone:
		case <-time.After(5 * time.Second):
			t.Errorf("stdio host did not exit")
		}
	})
	transport := &catalogRecordingTransport{conn: clientSide, reader: NewFramedReader(clientSide), writer: NewFramedWriter(clientSide)}
	connection, err := connectRemoteTransport(context.Background(), transport)
	if err != nil {
		t.Fatalf("connectRemoteTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection, transport
}

func catalogTestTools(description string) []tool.CodeModeRemoteToolDefinition {
	return []tool.CodeModeRemoteToolDefinition{{
		Name: "echo", ToolName: tool.PlainName("echo"), Description: description,
		Kind: tool.PayloadFunction, InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
}

// TestCodeModeCatalogRevisionEndToEndLikeRust covers Rust #61846's acknowledged
// reuse: the first execution installs revision 1 with the full catalog, the
// next identical execution omits the definitions, and changed tools install a
// new revision. Requests without tools stay self-contained.
func TestCodeModeCatalogRevisionEndToEndLikeRust(t *testing.T) {
	connection, transport := startCatalogTestHost(t)
	if !connection.capabilities.Contains(Capability(ToolCatalogCapability)) {
		t.Fatal("host did not negotiate tool-catalog-revision-v1")
	}
	session := &remoteSession{
		provider: &fakeRemoteSessionProvider{connection: connection},
		delegate: &recordingRemoteDelegate{},
		id:       "catalog-e2e",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run := func(callID string, tools []tool.CodeModeRemoteToolDefinition) {
		t.Helper()
		response, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{
			ToolCallID: callID, Source: `text("ok")`, EnabledTools: tools,
		}, nil)
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", callID, err)
		}
		if response.State != "completed" {
			t.Fatalf("Execute(%s) state = %q", callID, response.State)
		}
	}
	run("first", catalogTestTools("Echo a value"))
	run("reuse", catalogTestTools("Echo a value"))
	run("changed", catalogTestTools("Echo a changed value"))
	run("reuse-changed", catalogTestTools("Echo a changed value"))
	run("no-tools", nil)

	executions := transport.executions()
	if len(executions) != 5 {
		t.Fatalf("recorded executions = %d, want 5", len(executions))
	}
	want := []struct {
		revision *uint64
		tools    int
	}{
		{revision: uint64Ptr(1), tools: 1},
		{revision: uint64Ptr(1), tools: 0},
		{revision: uint64Ptr(2), tools: 1},
		{revision: uint64Ptr(2), tools: 0},
		{revision: nil, tools: 0},
	}
	for index, expectation := range want {
		got := executions[index]
		if got.CatalogRevision == nil != (expectation.revision == nil) {
			t.Fatalf("execution %d revision = %v, want %v", index, got.CatalogRevision, expectation.revision)
		}
		if expectation.revision != nil && *got.CatalogRevision != *expectation.revision {
			t.Fatalf("execution %d revision = %d, want %d", index, *got.CatalogRevision, *expectation.revision)
		}
		if len(got.EnabledTools) != expectation.tools {
			t.Fatalf("execution %d tools = %d, want %d", index, len(got.EnabledTools), expectation.tools)
		}
	}
}

// TestCodeModeCatalogRevisionHostValidationLikeRust covers Rust #61846's host
// rules: revision zero is invalid, an unknown reference fails, reusing the
// installed revision keeps working, conflicting definitions fail, and a cell
// keeps the catalog snapshot it started with while a newer revision installs.
func TestCodeModeCatalogRevisionHostValidationLikeRust(t *testing.T) {
	connection, _ := startCatalogTestHost(t)
	delegate := &recordingRemoteDelegate{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sessionID := SessionID("catalog-validation")
	if response, err := connection.Request(ctx, OpenSessionRequest(sessionID)); err != nil || response.Type != "session/ready" {
		t.Fatalf("open session = %#v, %v", response, err)
	}
	execute := func(callID string, tools []ProtocolToolDefinition, revision *uint64, source string, yieldMS *uint64) (RuntimeResponse, error) {
		return connection.Execute(ctx, sessionID, delegate, ExecuteRequest{
			ToolCallID: callID, EnabledTools: tools, Source: source,
			YieldTimeMS: yieldMS, CatalogRevision: revision,
		}, nil)
	}
	echo := ProtocolToolDefinition{Name: "echo", ToolName: ToolName{Name: "echo"}, Description: "Echo", Kind: ProtocolToolKindFunction}
	other := ProtocolToolDefinition{Name: "other", ToolName: ToolName{Name: "other"}, Description: "Other", Kind: ProtocolToolKindFunction}

	if _, err := execute("zero", nil, uint64Ptr(0), `text("x")`, nil); err == nil || !strings.Contains(err.Error(), "invalid code-mode catalog revision") {
		t.Fatalf("revision zero error = %v", err)
	}
	if _, err := execute("unknown", nil, uint64Ptr(9), `text("x")`, nil); err == nil || !strings.Contains(err.Error(), "unknown code-mode catalog revision") {
		t.Fatalf("unknown revision error = %v", err)
	}
	if _, err := execute("install", []ProtocolToolDefinition{echo}, uint64Ptr(1), `text("installed")`, nil); err != nil {
		t.Fatalf("install revision error = %v", err)
	}
	if _, err := execute("reuse", nil, uint64Ptr(1), `text("reused")`, nil); err != nil {
		t.Fatalf("reuse revision error = %v", err)
	}
	conflict := echo
	conflict.Description = "Conflicting echo"
	if _, err := execute("conflict", []ProtocolToolDefinition{conflict}, uint64Ptr(1), `text("x")`, nil); err == nil || !strings.Contains(err.Error(), "conflicting code-mode catalog revision") {
		t.Fatalf("conflicting revision error = %v", err)
	}

	yield := uint64(0)
	frozen, err := execute("frozen", nil, uint64Ptr(1), `await new Promise(r => setTimeout(r, 80)); text(typeof tools.echo + ":" + typeof tools.other);`, &yield)
	if err != nil {
		t.Fatalf("start frozen cell error = %v", err)
	}
	if frozen.Variant != "Yielded" {
		t.Fatalf("frozen cell initial response = %#v", frozen)
	}
	if _, err := execute("install-2", []ProtocolToolDefinition{other}, uint64Ptr(2), `text("installed 2")`, nil); err != nil {
		t.Fatalf("install revision 2 error = %v", err)
	}
	waitYield := uint64(5000)
	waited, err := connection.Request(ctx, WaitSessionRequest(sessionID, WaitRequest{CellID: frozen.CellID, YieldTimeMS: waitYield}))
	if err != nil || waited.Outcome == nil {
		t.Fatalf("wait frozen cell = %#v, %v", waited, err)
	}
	items := waited.Outcome.Response.ContentItems
	if len(items) != 1 || items[0].Text != "function:undefined" {
		t.Fatalf("frozen cell items = %#v", items)
	}
}

// TestCodeModeCatalogAdmissionCacheLikeRust covers Rust #61846's client-side
// admission rules: concurrent admissions fall back to full requests, accepted
// catalogs are reused, and an abandoned replacement never reuses its revision.
func TestCodeModeCatalogAdmissionCacheLikeRust(t *testing.T) {
	cache := &codeModeCatalogCache{}
	tools := []ProtocolToolDefinition{{Name: "echo", ToolName: ToolName{Name: "echo"}, Description: "Echo"}}
	first := cache.prepare(tools)
	if first == nil || first.reusesCatalog() || first.revision != 1 {
		t.Fatalf("first admission = %#v", first)
	}
	if cache.prepare(tools) != nil {
		t.Fatal("a concurrent admission did not fall back to a full request")
	}
	first.accept()
	reuse := cache.prepare(tools)
	if reuse == nil || !reuse.reusesCatalog() || reuse.revision != 1 {
		t.Fatalf("reuse admission = %#v", reuse)
	}
	reuse.accept()
	changed := cloneProtocolToolDefinitions(tools)
	changed[0].Description = "Changed"
	pending := cache.prepare(changed)
	if pending == nil || pending.reusesCatalog() || pending.revision != 2 {
		t.Fatalf("changed admission = %#v", pending)
	}
	pending.abandon()
	retry := cache.prepare(changed)
	if retry == nil || retry.reusesCatalog() || retry.revision != 3 {
		t.Fatalf("retry admission = %#v, want revision 3 after the abandoned replacement", retry)
	}
	retry.accept()
	cache.mu.Lock()
	stored := cache.tools
	cache.mu.Unlock()
	if len(stored) != 1 || stored[0].Description != "Changed" {
		t.Fatalf("installed catalog = %#v", stored)
	}
	tools[0].Description = "Mutated after admission"
	if stored[0].Description != "Changed" {
		t.Fatal("installed catalog aliased the caller's definitions")
	}
}
