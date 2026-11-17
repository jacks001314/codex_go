package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/app/sidebar_document.rs `Document::loaded`
// and `source_target` (#61888).

func TestSidebarMarkdownLinkRendersWithoutSourceLinesLikeRust(t *testing.T) {
	document := newSidebarDocument("thread-1", "/workspace/README.md", 0, nil)
	document.loaded("# Title\n\nbody\n", nil)
	if document.state != sidebarDocumentReady {
		t.Fatalf("state = %v, want ready", document.state)
	}
	if document.code || len(document.lineStarts) != 0 || document.language != "" {
		t.Fatalf("document = %+v, want rendered Markdown", document)
	}
	if document.target != nil {
		t.Fatalf("target = %+v, want none without a location", document.target)
	}
}

func TestSidebarSourcePreviewTracksLineStartsLikeRust(t *testing.T) {
	document := newSidebarDocument("thread-1", "/workspace/main.go", 2, nil)
	document.loaded("package main\n\nfunc main() {}\n", nil)
	if !document.code || document.language != "go" {
		t.Fatalf("document = %+v, want a Go source preview", document)
	}
	want := []int{0, len("package main") + 1, len("package main") + 2}
	if len(document.lineStarts) != len(want) {
		t.Fatalf("line starts = %v, want %v", document.lineStarts, want)
	}
	for index := range want {
		if document.lineStarts[index] != want[index] {
			t.Fatalf("line starts = %v, want %v", document.lineStarts, want)
		}
	}
}

func TestSidebarCoordinateLinkShowsSourceAndClampsTargetLikeRust(t *testing.T) {
	location := &markdown.SidebarSourceLocation{Line: 2, Column: 3}
	document := newSidebarDocument("thread-1", "/workspace/notes.md", 0, location)
	document.loaded("alpha\nbeta\ngamma\n", nil)
	if !document.code {
		t.Fatal("a coordinate link must show source")
	}
	if document.target == nil || document.target.Line != 2 || document.target.Column != 3 || document.target.Offset != len("alpha\n")+2 {
		t.Fatalf("target = %+v, want line 2 column 3", document.target)
	}

	document = newSidebarDocument("thread-1", "/workspace/notes.md", 0, &markdown.SidebarSourceLocation{Line: 99, Column: 99})
	document.loaded("alpha\nbeta\n", nil)
	if document.target == nil || document.target.Line != 2 || document.target.Column != len("beta")+1 {
		t.Fatalf("target = %+v, want the last line's end", document.target)
	}

	document = newSidebarDocument("thread-1", "/workspace/notes.md", 0, &markdown.SidebarSourceLocation{Line: 1, Column: 3})
	document.loaded("a\U0001f44d\U0001f3fdb\n", nil)
	if document.target == nil || document.target.Column != 2 {
		t.Fatalf("target = %+v, want the emoji's grapheme start", document.target)
	}
}

func TestSidebarSourcePreviewLimitsLikeRust(t *testing.T) {
	large := strings.Repeat("x", sidebarDocumentMaxSourceBytes+1)
	document := newSidebarDocument("thread-1", "/workspace/main.go", 0, nil)
	document.loaded(large, nil)
	if document.state != sidebarDocumentFailed || document.readErr != "Source preview limit is 1 MiB" {
		t.Fatalf("state = %v err = %q, want the 1 MiB limit", document.state, document.readErr)
	}

	// Markdown renders without a byte limit; only coordinate links read source.
	document = newSidebarDocument("thread-1", "/workspace/README.md", 0, nil)
	document.loaded(large, nil)
	if document.state != sidebarDocumentReady {
		t.Fatalf("state = %v, want large Markdown rendered", document.state)
	}

	lines := strings.Repeat("line\n", sidebarDocumentMaxSourceLines+1)
	document = newSidebarDocument("thread-1", "/workspace/main.go", 0, nil)
	document.loaded(lines, nil)
	if document.state != sidebarDocumentFailed || document.readErr != "Source preview limit is 10000 lines" {
		t.Fatalf("state = %v err = %q, want the line limit", document.state, document.readErr)
	}
}

func TestSidebarDocumentFailuresKeepTheirReasonLikeRust(t *testing.T) {
	document := newSidebarDocument("thread-1", "/workspace/main.go", 0, nil)
	document.connectionReplaced()
	if document.state != sidebarDocumentFailed || document.readErr != "Connection changed. Reopen this file to try again." {
		t.Fatalf("state = %v err = %q, want the connection note", document.state, document.readErr)
	}

	document = newSidebarDocument("thread-1", "/workspace/main.go", 0, nil)
	document.loaded("", errors.New("file is not valid UTF-8"))
	if document.state != sidebarDocumentFailed || document.readErr != "file is not valid UTF-8" {
		t.Fatalf("state = %v err = %q, want the read error", document.state, document.readErr)
	}

	// A ready document survives a reconnect; only in-flight reads fail.
	document = newSidebarDocument("thread-1", "/workspace/main.go", 0, nil)
	document.loaded("package main\n", nil)
	document.connectionReplaced()
	if document.state != sidebarDocumentReady {
		t.Fatalf("state = %v, want the loaded preview kept", document.state)
	}
}

func TestSidebarDocumentSourceLinesMatchRustLinesLikeRust(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\n\n", []string{"a", ""}},
	}
	for _, testCase := range cases {
		got := sidebarSourceLines(testCase.text)
		if len(got) != len(testCase.want) {
			t.Fatalf("sidebarSourceLines(%q) = %v, want %v", testCase.text, got, testCase.want)
		}
		for index := range testCase.want {
			if got[index] != testCase.want[index] {
				t.Fatalf("sidebarSourceLines(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
		}
	}
}

func TestOpenSidebarDocumentReadsThroughAppServerLikeRust(t *testing.T) {
	client, transport := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult([]byte("package main\n\nfunc main() {}\n")), ""
		}
		return nil, "unexpected method " + method
	})
	document := openSidebarDocument(context.Background(), client, "thread-source", "main.go", 1, nil)
	if document.state != sidebarDocumentReady {
		t.Fatalf("state = %v err = %q, want ready", document.state, document.readErr)
	}
	if document.path != "/workspace/repo/main.go" {
		t.Fatalf("path = %q, want the workspace-resolved path", document.path)
	}
	if document.link != 1 || !document.code || document.language != "go" || document.text == "" {
		t.Fatalf("document = %+v, want a linked Go source preview", document)
	}
	reads := transport.calls(string(appserver.MethodFSReadFile))
	if len(reads) != 1 {
		t.Fatalf("fs/readFile calls = %d, want 1", len(reads))
	}
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(reads[0].Params, &params); err != nil {
		t.Fatalf("decode fs/readFile params: %v", err)
	}
	if params.Path != "/workspace/repo/main.go" {
		t.Fatalf("fs/readFile path = %q, want the resolved path", params.Path)
	}
}

func TestOpenSidebarDocumentKeepsReadFailuresLikeRust(t *testing.T) {
	client, _ := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return nil, "file not found"
		}
		return nil, "unexpected method " + method
	})
	document := openSidebarDocument(context.Background(), client, "thread-source", "missing.go", 0, nil)
	if document.state != sidebarDocumentFailed || !strings.Contains(document.readErr, "file not found") {
		t.Fatalf("state = %v err = %q, want the host read error", document.state, document.readErr)
	}

	client, _ = newPreviewStubClient(func(string, json.RawMessage) (any, string) {
		return previewThreadResult(""), ""
	})
	document = openSidebarDocument(context.Background(), client, "thread-source", "main.go", 0, nil)
	if document.state != sidebarDocumentFailed || document.readErr != remotePreviewNeedsWorkspace {
		t.Fatalf("state = %v err = %q, want the missing workspace note", document.state, document.readErr)
	}
}
