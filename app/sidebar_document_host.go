package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"codex_go/appserverdaemon"
	codextea "codex_go/tui/tea"
)

// Rust parity: codex-rs/tui/src/app/sidebar_document.rs `open_document` /
// `open_resolved_document` (#61888/#61889/#61890).
//
// The TUI asks for one preview per chooser row. The embedded session reads the
// workspace on this machine, while a remote session resolves and reads through
// the app-server so the host keeps enforcing its own sandbox.

// sidebarDocumentHost adapts the app-level reader to the TUI's document hook.
type sidebarDocumentHost struct {
	read func(request codextea.SidebarDocumentRequest) *sidebarDocument
}

func (h *sidebarDocumentHost) open(request codextea.SidebarDocumentRequest) codextea.SidebarDocumentView {
	if h == nil || h.read == nil {
		return codextea.SidebarDocumentView{Err: remotePreviewNotText}
	}
	return sidebarDocumentViewOf(h.read(request))
}

// newEmbeddedSidebarDocumentHost reads the focused thread's workspace from the
// local filesystem. cwd is empty when the session has no workspace yet.
func newEmbeddedSidebarDocumentHost(cwd func() string) *sidebarDocumentHost {
	return &sidebarDocumentHost{read: func(request codextea.SidebarDocumentRequest) *sidebarDocument {
		workingDirectory := ""
		if cwd != nil {
			workingDirectory = strings.TrimSpace(cwd())
		}
		document := newSidebarDocument(request.SourceThreadID, request.Path, request.Link, request.Location)
		resolved, err := resolveRemotePreviewPath(request.Path, workingDirectory)
		if err != nil {
			document.loaded("", err)
			return document
		}
		document.path = resolved
		text, err := readLocalDocumentText(resolved)
		if err != nil {
			document.loaded("", err)
			return document
		}
		document.loaded(text, nil)
		return document
	}}
}

// newRemoteSidebarDocumentHost reads previews over one app-server connection
// per open, the way Rust's request view does for a single read.
func newRemoteSidebarDocumentHost(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) *sidebarDocumentHost {
	return &sidebarDocumentHost{read: func(request codextea.SidebarDocumentRequest) *sidebarDocument {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			document := newSidebarDocument(request.SourceThreadID, request.Path, request.Link, request.Location)
			document.loaded("", err)
			return document
		}
		defer client.close()
		return openSidebarDocument(ctx, client, request.SourceThreadID, request.Path, request.Link, request.Location)
	}}
}

// sidebarDocumentViewOf converts a loaded document into the TUI's view.
func sidebarDocumentViewOf(document *sidebarDocument) codextea.SidebarDocumentView {
	if document == nil {
		return codextea.SidebarDocumentView{}
	}
	view := codextea.SidebarDocumentView{
		Path:       document.path,
		Text:       document.text,
		Code:       document.code,
		Language:   document.language,
		LineStarts: document.lineStarts,
	}
	if document.state == sidebarDocumentFailed {
		view.Err = document.readErr
	}
	if target := document.target; target != nil {
		view.Target = &codextea.SidebarDocumentTarget{Line: target.Line, Column: target.Column, Offset: target.Offset}
	}
	return view
}

// readLocalDocumentText reads and validates one preview file from the local
// workspace with the same text rules as the remote reader.
func readLocalDocumentText(resolvedPath string) (string, error) {
	data, err := os.ReadFile(filepath.FromSlash(resolvedPath))
	if err != nil {
		return "", err
	}
	return documentTextFromBytes(data)
}

// documentTextFromBytes keeps preview reads to UTF-8 text the sidebar can show
// (Rust `read_document`).
func documentTextFromBytes(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", errors.New(remotePreviewNotUTF8)
	}
	text := string(data)
	for _, r := range text {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return "", errors.New(remotePreviewNotText)
		}
	}
	return text, nil
}
