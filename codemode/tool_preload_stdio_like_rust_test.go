package codemode

import (
	"context"
	"io"
	"testing"
	"time"
)

func startStdioHostWithPreloadsForTest(t *testing.T, preloads []ToolPreload) *stdioHostFrames {
	t.Helper()
	hostIn, clientOut := io.Pipe()
	clientIn, hostOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	reader := NewFramedReader(hostIn)
	writer := NewFramedWriter(hostOut)
	done := make(chan error, 1)
	go func() {
		done <- runCodemodeHost(
			ctx,
			func(message *ClientToHost) (bool, error) { return reader.Read(message) },
			func(message any) error { return writer.Write(message) },
			codemodeHostOptions{toolPreloads: toolPreloadsForHost(preloads)},
		)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("code-mode host did not exit")
		}
	})
	frames := &stdioHostFrames{client: clientOut, hostRead: clientIn, reader: NewFramedReader(clientIn)}
	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	if err := NewFramedWriter(clientOut).Write(ClientHelloMessage(hello)); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if frame, err := frames.next(); err != nil || frame.Type != "connection/ready" {
		t.Fatalf("handshake frame = %#v, err = %v", frame, err)
	}
	return frames
}

func executeStdioCell(t *testing.T, frames *stdioHostFrames, id RequestID, sessionID SessionID, tools []ProtocolToolDefinition, source string) RuntimeResponse {
	t.Helper()
	yield := uint64(60000)
	frames.send(t, ClientToHost{Type: "operation/request", ID: id, Request: &HostRequest{
		Method:    "session/execute",
		SessionID: sessionID,
		Request: &ExecuteRequest{
			ToolCallID:   "cell-" + string(sessionID),
			EnabledTools: tools,
			Source:       source,
			YieldTimeMS:  &yield,
		},
	}})
	frames.nextOfType(t, "operation/response")
	initial := frames.nextOfType(t, "execute/initialResponse")
	if initial.Initial == nil || initial.Initial.Status != "ok" {
		t.Fatalf("initial response = %#v", initial.Initial)
	}
	return initial.Initial.Value
}

// Mirrors Rust #62430's stdio subprocess test: a helper that fails one cell must
// leave another session on the same connection usable.
func TestStdioHostToolPreloadFailureKeepsOtherSessionsUsableLikeRust(t *testing.T) {
	frames := startStdioHostWithPreloadsForTest(t, []ToolPreload{{
		Tool: "lookup", ToolName: "lookup", Source: "const =",
	}})
	failing := openStdioSession(t, frames, "session-preload-failure", nil)
	response := executeStdioCell(t, frames, 2, failing, []ProtocolToolDefinition{
		toolPreloadDefinition("lookup", PlainToolName("lookup")),
	}, `text("never runs")`)
	if response.ErrorText == nil || *response.ErrorText != "failed to compile tool preload #1 for `lookup`" {
		t.Fatalf("error_text = %#v, want the bounded helper failure", response.ErrorText)
	}

	healthy := openStdioSession(t, frames, "session-preload-healthy", nil)
	response = executeStdioCell(t, frames, 3, healthy, nil, `text("still usable")`)
	if response.ErrorText != nil {
		t.Fatalf("healthy session failed: %s", *response.ErrorText)
	}
	if got := observationText(response.ContentItems); got != "still usable" {
		t.Fatalf("healthy session output = %q, want still usable", got)
	}
}
