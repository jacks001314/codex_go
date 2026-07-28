package idecontext

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFetchIDEContextFromStreamRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	serverErr := make(chan error, 1)
	go func() {
		request, err := ReadFrame(server)
		if err != nil {
			serverErr <- err
			return
		}
		if request["method"] != "ide-context" {
			serverErr <- errors.New("unexpected IDE context method")
			return
		}
		params, _ := request["params"].(map[string]any)
		if params["workspaceRoot"] != "/repo" {
			serverErr <- errors.New("unexpected IDE workspace root")
			return
		}
		serverErr <- WriteFrame(server, map[string]any{
			"type":       "response",
			"requestId":  request["requestId"],
			"resultType": "success",
			"result": map[string]any{"ideContext": map[string]any{
				"activeFile": map[string]any{"path": "/repo/main.go", "label": "main.go", "selection": map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 0}}},
			}},
		})
	}()

	context, err := fetchIDEContextFromStream(client, "/repo", time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("fetchIDEContextFromStream() error = %v", err)
	}
	if context.ActiveFile == nil || context.ActiveFile.Path != "/repo/main.go" {
		t.Fatalf("IDE context = %#v", context)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("IDE context server error = %v", err)
	}
}

type frameReadWriter struct {
	reader *bytes.Reader
	writes bytes.Buffer
}

func newFrameReadWriter(frames []byte) *frameReadWriter {
	return &frameReadWriter{reader: bytes.NewReader(frames)}
}

func (rw *frameReadWriter) Read(p []byte) (int, error) {
	return rw.reader.Read(p)
}

func (rw *frameReadWriter) Write(p []byte) (int, error) {
	return rw.writes.Write(p)
}

func TestWriteIDEContextRequestFrameMatchesRust(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteIDEContextRequest(&buf, "req-1", "/repo"); err != nil {
		t.Fatalf("WriteIDEContextRequest() error = %v", err)
	}

	data := buf.Bytes()
	if len(data) < 4 {
		t.Fatalf("frame too short: %d", len(data))
	}
	if got, want := int(binary.LittleEndian.Uint32(data[:4])), len(data)-4; got != want {
		t.Fatalf("length prefix = %d, want %d", got, want)
	}

	message, err := ReadFrame(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if message["type"] != "request" ||
		message["requestId"] != "req-1" ||
		message["sourceClientId"] != TUISourceClientID ||
		message["version"] != float64(0) ||
		message["method"] != "ide-context" {
		t.Fatalf("request message = %#v", message)
	}
	params, ok := message["params"].(map[string]any)
	if !ok || params["workspaceRoot"] != "/repo" {
		t.Fatalf("params = %#v", message["params"])
	}
}

func TestReadResponseFrameHandlesInterleavedMessagesMatchRust(t *testing.T) {
	var frames bytes.Buffer
	messages := []any{
		map[string]any{"type": "broadcast"},
		map[string]any{"type": "client-discovery-request", "requestId": "discover-1"},
		map[string]any{"type": "client-discovery-response"},
		map[string]any{"type": "request", "requestId": "inbound-1"},
		map[string]any{"type": "response", "requestId": "other", "resultType": "success"},
		map[string]any{
			"type":       "response",
			"requestId":  "target",
			"resultType": "success",
			"result": map[string]any{
				"ideContext": map[string]any{"openTabs": []any{map[string]any{"label": "main.rs", "path": "src/main.rs"}}},
			},
		},
	}
	for _, message := range messages {
		if err := WriteFrame(&frames, message); err != nil {
			t.Fatalf("WriteFrame() error = %v", err)
		}
	}

	stream := newFrameReadWriter(frames.Bytes())
	response, err := ReadResponseFrame(stream, "target", time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("ReadResponseFrame() error = %v", err)
	}
	if response["requestId"] != "target" {
		t.Fatalf("response = %#v", response)
	}

	written := bytes.NewReader(stream.writes.Bytes())
	discoveryResponse, err := ReadFrame(written)
	if err != nil {
		t.Fatalf("reading discovery response error = %v", err)
	}
	if discoveryResponse["type"] != "client-discovery-response" || discoveryResponse["requestId"] != "discover-1" {
		t.Fatalf("discovery response = %#v", discoveryResponse)
	}
	discoveryBody, _ := discoveryResponse["response"].(map[string]any)
	if discoveryBody["canHandle"] != false {
		t.Fatalf("discovery response body = %#v", discoveryBody)
	}

	unsupportedResponse, err := ReadFrame(written)
	if err != nil {
		t.Fatalf("reading unsupported response error = %v", err)
	}
	if unsupportedResponse["type"] != "response" ||
		unsupportedResponse["requestId"] != "inbound-1" ||
		unsupportedResponse["resultType"] != "error" ||
		unsupportedResponse["error"] != "no-handler-for-request" {
		t.Fatalf("unsupported response = %#v", unsupportedResponse)
	}
}

func TestExtractIDEContextAndErrorsMatchRust(t *testing.T) {
	response := map[string]any{
		"resultType": "success",
		"result": map[string]any{
			"ideContext": map[string]any{
				"activeFile": map[string]any{
					"label": "lib.rs",
					"path":  "src/lib.rs",
					"selection": map[string]any{
						"start": map[string]any{"line": 1, "character": 2},
						"end":   map[string]any{"line": 3, "character": 4},
					},
					"activeSelectionContent": "selected",
				},
			},
		},
	}
	context, err := ExtractIDEContext(response)
	if err != nil {
		t.Fatalf("ExtractIDEContext() error = %v", err)
	}
	want := &IdeContext{
		ActiveFile: &ActiveFile{
			FileDescriptor:         descriptor("lib.rs", "src/lib.rs"),
			Selection:              Range{Start: Position{Line: 1, Character: 2}, End: Position{Line: 3, Character: 4}},
			ActiveSelectionContent: "selected",
		},
	}
	if !reflect.DeepEqual(context, want) {
		t.Fatalf("context = %#v, want %#v", context, want)
	}

	err = EnsureSuccessResponse(map[string]any{"resultType": "error", "error": "request-timeout"})
	var contextErr *IdeContextError
	if !errors.As(err, &contextErr) || contextErr.Kind != IdeContextErrorRequestFailed || contextErr.Message != "request-timeout" {
		t.Fatalf("request error = %#v", err)
	}
	if got, want := contextErr.PromptSkipHint(), "The IDE extension did not answer in time. "+KeepTryingHint; got != want {
		t.Fatalf("PromptSkipHint() = %q, want %q", got, want)
	}
}

func TestReadFrameSizeDeadlineAndDefaultPathMatchRustCore(t *testing.T) {
	var oversized [4]byte
	binary.LittleEndian.PutUint32(oversized[:], uint32(MaxIPCFrameBytes+1))
	_, err := ReadFrame(bytes.NewReader(oversized[:]))
	var contextErr *IdeContextError
	if !errors.As(err, &contextErr) || contextErr.Kind != IdeContextErrorResponseTooLarge {
		t.Fatalf("oversized error = %#v", err)
	}

	err = ReadExactBeforeDeadline(bytes.NewReader(nil), []byte{0}, time.Now().Add(-time.Second))
	if !errors.Is(err, ErrIDEContextTimedOut) {
		t.Fatalf("deadline error = %#v", err)
	}

	path := DefaultIPCSocketPath()
	if runtime.GOOS == "windows" {
		if path != `\\.\pipe\codex-ipc` {
			t.Fatalf("windows default path = %q", path)
		}
	} else if !strings.Contains(path, "codex-ipc") || !strings.HasSuffix(path, ".sock") {
		t.Fatalf("unix default path = %q", path)
	}
}
