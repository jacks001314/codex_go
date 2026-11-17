package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"codex_go/appserver"
)

// Rust parity: codex-rs/tui/src/app_server_session/fs_preview_tests.rs (#61887).

type previewStubRequest struct {
	Method string
	Params json.RawMessage
}

type previewStubTransport struct {
	mu       sync.Mutex
	queue    [][]byte
	requests []previewStubRequest
	handler  func(method string, params json.RawMessage) (any, string)
}

func (s *previewStubTransport) read(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, io.EOF
	}
	frame := s.queue[0]
	s.queue = s.queue[1:]
	return frame, nil
}

func (s *previewStubTransport) write(_ context.Context, data []byte) error {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, previewStubRequest{Method: request.Method, Params: request.Params})
	frame := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	result, message := s.handler(request.Method, request.Params)
	if message != "" {
		frame["error"] = map[string]any{"code": -32000, "message": message}
	} else {
		frame["result"] = result
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	s.queue = append(s.queue, encoded)
	return nil
}

func (s *previewStubTransport) close() {}

func (s *previewStubTransport) calls(method string) []previewStubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]previewStubRequest, 0, len(s.requests))
	for _, request := range s.requests {
		if request.Method == method {
			out = append(out, request)
		}
	}
	return out
}

func newPreviewStubClient(handler func(method string, params json.RawMessage) (any, string)) (*remoteAppServerTUIClient, *previewStubTransport) {
	transport := &previewStubTransport{handler: handler}
	return &remoteAppServerTUIClient{transport: transport}, transport
}

func previewThreadResult(cwd string) map[string]any {
	environments := []any{}
	if cwd != "" {
		environments = append(environments, map[string]any{"environmentId": "local", "cwd": cwd})
	}
	return map[string]any{"thread": map[string]any{"id": "thread-source", "environments": environments}}
}

func previewFileResult(data []byte) map[string]any {
	return map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(data)}
}

func TestRemotePreviewResolvesRelativePathAndReadsDocumentLikeRust(t *testing.T) {
	client, transport := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult([]byte("hello\nworld\t!")), ""
		}
		return nil, "unexpected method " + method
	})
	ctx := context.Background()
	resolved, err := resolveRemoteThreadFile(ctx, client, "thread-source", "docs/note.md")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != "/workspace/repo/docs/note.md" {
		t.Fatalf("resolved = %q", resolved)
	}
	text, err := readRemoteDocumentText(ctx, client, "thread-source", "docs/note.md", resolved)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if text != "hello\nworld\t!" {
		t.Fatalf("text = %q", text)
	}
	reads := transport.calls(string(appserver.MethodThreadRead))
	if len(reads) != 3 {
		t.Fatalf("thread/read calls = %d, want resolve plus two workspace checks", len(reads))
	}
	var params appserver.ThreadReadParams
	if err := json.Unmarshal(reads[0].Params, &params); err != nil {
		t.Fatalf("thread/read params: %v", err)
	}
	if params.ThreadID != "thread-source" || params.IncludeTurns {
		t.Fatalf("thread/read params = %+v, want metadata-only read of the source thread", params)
	}
	files := transport.calls(string(appserver.MethodFSReadFile))
	if len(files) != 1 {
		t.Fatalf("fs/readFile calls = %d", len(files))
	}
	// The app-server host validates the path against its own native absolute
	// rules, so decode the wire params without the client-side host check.
	var read struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(files[0].Params, &read); err != nil {
		t.Fatalf("fs/readFile params: %v", err)
	}
	if read.Path != resolved {
		t.Fatalf("fs/readFile path = %q, want %q", read.Path, resolved)
	}
}

func TestRemotePreviewReadsLargeTextLikeRust(t *testing.T) {
	large := strings.Repeat("preview line\n", 40_000)
	client, _ := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult([]byte(large)), ""
		}
		return nil, "unexpected method " + method
	})
	text, err := readRemoteDocumentText(context.Background(), client, "thread-source", "big.txt", "/workspace/repo/big.txt")
	if err != nil {
		t.Fatalf("read large document: %v", err)
	}
	if text != large {
		t.Fatalf("large document truncated: %d bytes", len(text))
	}
}

func TestRemotePreviewRejectsNonUTF8LikeRust(t *testing.T) {
	client, _ := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult([]byte{0xff, 0xfe, 0xfd}), ""
		}
		return nil, "unexpected method " + method
	})
	_, err := readRemoteDocumentText(context.Background(), client, "thread-source", "binary.png", "/workspace/repo/binary.png")
	if err == nil || !strings.Contains(err.Error(), "Not a UTF-8 text file") {
		t.Fatalf("error = %v, want the UTF-8 rejection", err)
	}
}

func TestRemotePreviewRejectsControlCharactersLikeRust(t *testing.T) {
	client, _ := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult([]byte("a\x00b")), ""
		}
		return nil, "unexpected method " + method
	})
	_, err := readRemoteDocumentText(context.Background(), client, "thread-source", "binary.bin", "/workspace/repo/binary.bin")
	if err == nil || !strings.Contains(err.Error(), "Not a text file") {
		t.Fatalf("error = %v, want the control-character rejection", err)
	}
}

func TestRemotePreviewRejectsWorkspaceChangeLikeRust(t *testing.T) {
	reads := 0
	client, _ := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			reads++
			if reads == 1 {
				return previewThreadResult("/workspace/repo"), ""
			}
			return previewThreadResult("/workspace/other"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult([]byte("stale")), ""
		}
		return nil, "unexpected method " + method
	})
	_, err := readRemotePreviewFile(context.Background(), client, "thread-source", "docs/note.md", "/workspace/repo/docs/note.md")
	if err == nil || !strings.Contains(err.Error(), "Workspace changed") {
		t.Fatalf("error = %v, want the workspace-change rejection", err)
	}
}

func TestRemotePreviewRejectsWindowsHostLikeRust(t *testing.T) {
	client, transport := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		if method == string(appserver.MethodThreadRead) {
			return previewThreadResult(`C:\repo`), ""
		}
		return previewFileResult([]byte("x")), ""
	})
	resolved, err := resolveRemoteThreadFile(context.Background(), client, "thread-source", "docs/note.md")
	if err != nil {
		t.Fatalf("resolve on a Windows host: %v", err)
	}
	if resolved != "C:/repo/docs/note.md" {
		t.Fatalf("resolved = %q", resolved)
	}
	_, err = readRemotePreviewFile(context.Background(), client, "thread-source", "docs/note.md", resolved)
	if err == nil || !strings.Contains(err.Error(), "Windows hosts are unavailable") {
		t.Fatalf("error = %v, want the Windows-host rejection", err)
	}
	if len(transport.calls(string(appserver.MethodFSReadFile))) != 0 {
		t.Fatal("a Windows host must be rejected before any fs/readFile")
	}
}

func TestRemotePreviewRequiresWorkspaceAndHostPathLikeRust(t *testing.T) {
	client, _ := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		return previewThreadResult(""), ""
	})
	if _, err := resolveRemoteThreadFile(context.Background(), client, "thread-source", "note.md"); err == nil ||
		!strings.Contains(err.Error(), "require a workspace") {
		t.Fatalf("error = %v, want the missing-workspace rejection", err)
	}
	client, _ = newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		return previewThreadResult("/workspace/repo"), ""
	})
	if _, err := resolveRemoteThreadFile(context.Background(), client, "thread-source", "file://other-host/tmp/note.md"); err == nil ||
		!strings.Contains(err.Error(), "path on the app-server host") {
		t.Fatalf("error = %v, want the foreign-host rejection", err)
	}
}
