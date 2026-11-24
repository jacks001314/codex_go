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
	"codex_go/sandbox"
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

// embeddedSidebarPreviewLimit is Rust #63184's 8 MiB model preview limit
// (sidebar_document/model_read.rs MAX_PREVIEW_BYTES).
const embeddedSidebarPreviewLimit = 8 * 1024 * 1024

// Errors mirror the messages Rust's model_read.rs reports for a preview the
// calling thread's own permissions do not allow.
var (
	errPreviewOutsideWorkspace = errors.New("The preview path is outside the calling workspace or the workspace changed")
	errPreviewExternalSandbox  = errors.New("The thread's external sandbox cannot authorize a preview")
	errPreviewReadDenied       = errors.New("The calling workspace permissions do not allow reading this preview")
	errPreviewTooLarge         = errors.New("The file exceeds the 8 MiB model preview limit")
)

// sidebarPreviewPermissions resolves the calling thread's read permissions: the
// profile that answers the read rules and the workspace roots the preview must
// stay inside. A nil resolver leaves the read unchecked.
type sidebarPreviewPermissions func(cwd string) (*sandbox.PermissionProfile, []string, error)

// newEmbeddedSidebarDocumentHost reads the focused thread's workspace from the
// local filesystem, re-checking the calling thread's read permissions the way
// Rust #63184's model_read.rs does. cwd is empty when the session has no
// workspace yet; a nil permissions resolver skips the re-check.
func newEmbeddedSidebarDocumentHost(cwd func() string, permissions sidebarPreviewPermissions) *sidebarDocumentHost {
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
		if permissions != nil {
			profile, roots, permErr := permissions(workingDirectory)
			if permErr != nil {
				document.loaded("", permErr)
				return document
			}
			if permErr := validateEmbeddedPreviewRead(resolved, workingDirectory, profile, roots); permErr != nil {
				document.loaded("", permErr)
				return document
			}
			if info, statErr := os.Stat(filepath.FromSlash(resolved)); statErr == nil && info.Size() > embeddedSidebarPreviewLimit {
				document.loaded("", errPreviewTooLarge)
				return document
			}
		}
		text, err := readLocalDocumentText(resolved)
		if err != nil {
			document.loaded("", err)
			return document
		}
		// Rust re-reads the preview workspace after the read and refuses a
		// preview whose workspace changed underneath it.
		if permissions != nil && cwd != nil && strings.TrimSpace(cwd()) != workingDirectory {
			document.loaded("", errPreviewOutsideWorkspace)
			return document
		}
		document.loaded(text, nil)
		return document
	}}
}

// validateEmbeddedPreviewRead mirrors Rust #63184's model_read::read checks with
// the calling thread's permission profile: the canonical file stays inside a
// canonical workspace root, an external sandbox cannot authorize a preview, and
// the profile's own read rules (allow entries plus deny-read entries and globs)
// must permit the read.
func validateEmbeddedPreviewRead(path string, cwd string, profile *sandbox.PermissionProfile, roots []string) error {
	canonical, err := filepath.EvalSymlinks(filepath.FromSlash(path))
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		roots = []string{cwd}
	}
	withinWorkspace := false
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if pathInsideDirectory(canonical, canonicalRoot) {
			withinWorkspace = true
			break
		}
	}
	if !withinWorkspace {
		return errPreviewOutsideWorkspace
	}
	if profile == nil {
		return errPreviewReadDenied
	}
	if profile.SandboxPolicy != nil && profile.SandboxPolicy.Kind == "external-sandbox" {
		return errPreviewExternalSandbox
	}
	entries := profile.RuntimeFileSystemEntries()
	context := sandbox.DenyReadContext{CWD: cwd, WorkspaceRoots: roots}
	if !sandbox.CanReadPathWithContext(entries, canonical, context) {
		return errPreviewReadDenied
	}
	if profile.DeniesReadPath(canonical) {
		return errPreviewReadDenied
	}
	matcher, err := sandbox.NewPreparedReadDenials(entries, context)
	if err != nil {
		return err
	}
	if matcher != nil && matcher.IsReadDenied(canonical) {
		return errPreviewReadDenied
	}
	return nil
}

// pathInsideDirectory reports whether path is root or lives beneath it, using
// cleaned absolute paths so a sibling prefix cannot match.
func pathInsideDirectory(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
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
