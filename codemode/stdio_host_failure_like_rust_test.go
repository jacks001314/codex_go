package codemode

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust's code-mode-host Result contract: a script failure is a
// completed observation that carries `error_text` and, in server-truncation
// mode, the labeled, budgeted `Script error:` item
// (code-mode-host/src/grpc/service_tests.rs).

type stdioHostFrames struct {
	client   *io.PipeWriter
	hostRead *io.PipeReader
	reader   *FramedReader
}

func startStdioHostForTest(t *testing.T) *stdioHostFrames {
	t.Helper()
	hostIn, clientOut := io.Pipe()
	clientIn, hostOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunStdioHost(ctx, hostIn, hostOut) }()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("RunStdioHost did not exit")
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

func (f *stdioHostFrames) send(t *testing.T, message ClientToHost) {
	t.Helper()
	if err := NewFramedWriter(f.client).Write(message); err != nil {
		t.Fatalf("write %s: %v", message.Type, err)
	}
}

func (f *stdioHostFrames) next() (HostToClient, error) {
	var frame HostToClient
	ok, err := f.reader.Read(&frame)
	if err != nil || !ok {
		return HostToClient{}, err
	}
	return frame, nil
}

func (f *stdioHostFrames) nextOfType(t *testing.T, want string) HostToClient {
	t.Helper()
	for attempt := 0; attempt < 4; attempt++ {
		frame, err := f.next()
		if err != nil {
			t.Fatalf("read %s: %v", want, err)
		}
		if frame.Type == want {
			return frame
		}
	}
	t.Fatalf("no %s frame", want)
	return HostToClient{}
}

func openStdioSession(t *testing.T, frames *stdioHostFrames, id string, truncation *OutputTruncation) SessionID {
	t.Helper()
	sessionID, _ := NewSessionID(id)
	frames.send(t, ClientToHost{Type: "operation/request", ID: 1, Request: &HostRequest{Method: "session/open", SessionID: sessionID, OutputTruncation: truncation}})
	response := frames.nextOfType(t, "operation/response")
	if response.Result == nil || response.Result.Status != "ok" || response.Result.Value.Type != "session/ready" {
		t.Fatalf("session/open response = %#v", response.Result)
	}
	return sessionID
}

func executeFailingScript(t *testing.T, frames *stdioHostFrames, sessionID SessionID, budget *int) RuntimeResponse {
	t.Helper()
	yield := uint64(60000)
	frames.send(t, ClientToHost{Type: "operation/request", ID: 2, Request: &HostRequest{
		Method:    "session/execute",
		SessionID: sessionID,
		Request: &ExecuteRequest{
			ToolCallID:      "call-failure",
			Source:          "text('partial'); throw new Error('boom');",
			YieldTimeMS:     &yield,
			MaxOutputTokens: budget,
		},
	}})
	frames.nextOfType(t, "operation/response")
	initial := frames.nextOfType(t, "execute/initialResponse")
	if initial.Initial == nil || initial.Initial.Status != "ok" {
		t.Fatalf("initial response = %#v", initial.Initial)
	}
	return initial.Initial.Value
}

func TestStdioHostReportsScriptFailuresLikeRust(t *testing.T) {
	frames := startStdioHostForTest(t)
	sessionID := openStdioSession(t, frames, "session-failure", nil)
	response := executeFailingScript(t, frames, sessionID, nil)
	if response.Variant != "Result" {
		t.Fatalf("variant = %q, want Result", response.Variant)
	}
	if response.ErrorText == nil || *response.ErrorText != "Error: boom" {
		t.Fatalf("error_text = %#v, want the script diagnostic", response.ErrorText)
	}
	if got := observationText(response.ContentItems); got != "partial" {
		t.Fatalf("client-mode items = %q, want the raw script output", got)
	}
}

func TestStdioHostServerTruncationLabelsFailuresLikeRust(t *testing.T) {
	server := OutputTruncationServer
	frames := startStdioHostForTest(t)
	sessionID := openStdioSession(t, frames, "session-failure-server", &server)
	budget := 7
	response := executeFailingScript(t, frames, sessionID, &budget)
	diagnostic := "Error: boom"
	if response.ErrorText == nil || *response.ErrorText != diagnostic {
		t.Fatalf("error_text = %#v, want %q", response.ErrorText, diagnostic)
	}
	want := "Warning: truncated output (original token count: 9)\nTotal output lines: 3\n\npartial\nScript…2 tokens truncated…r:\nError: boom"
	got := observationText(response.ContentItems)
	if got != want {
		t.Fatalf("server-mode items =\n%q\nwant\n%q", got, want)
	}
	if strings.Count(got, "Script error:") > 1 {
		t.Fatalf("the observation carries the label twice: %q", got)
	}
}

