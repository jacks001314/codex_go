package app

import (
	"context"
	"encoding/base64"
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"codex_go/appserver"
)

// Rust parity: codex-rs/tui/src/app_server_session/fs.rs (#61887).
//
// File previews resolve a requested path against the source thread's workspace
// on the app-server host and read bytes through `fs/readFile`, so the host keeps
// enforcing its own sandbox. Reads only work for POSIX workspaces, and a
// workspace change between resolution and read invalidates the result: the
// sidebar viewer must reopen the file instead of showing stale host bytes.

const (
	remotePreviewNeedsWorkspace     = "File previews require a workspace on the app-server host"
	remotePreviewHostLocal          = "File previews require a path on the app-server host"
	remotePreviewWindowsUnavailable = "File previews on Windows hosts are unavailable"
	remotePreviewWorkspaceChanged   = "Workspace changed. Reopen the file to try again."
	remotePreviewNotUTF8            = "Not a UTF-8 text file"
	remotePreviewNotText            = "Not a text file"

	remotePreviewPosix   = "posix"
	remotePreviewWindows = "windows"
)

// resolveRemoteThreadFile resolves a sidebar path against the source thread's
// workspace (Rust `resolve_thread_file`).
func resolveRemoteThreadFile(ctx context.Context, client *remoteAppServerTUIClient, sourceThreadID, target string) (string, error) {
	workspace, err := remotePreviewWorkspace(ctx, client, sourceThreadID)
	if err != nil {
		return "", err
	}
	return resolveRemotePreviewPath(target, workspace)
}

// remotePreviewWorkspace reads only the environment metadata of the source
// thread: other thread/read payloads may be native to a different host than the
// TUI's. Go's ThreadEnvironment already carries the app-server host's cwd.
func remotePreviewWorkspace(ctx context.Context, client *remoteAppServerTUIClient, sourceThreadID string) (string, error) {
	thread, err := remoteTUIReadThread(ctx, client, sourceThreadID, false)
	if err != nil {
		return "", err
	}
	for _, environment := range thread.Environments {
		if cwd := strings.TrimSpace(environment.CWD); cwd != "" {
			return cwd, nil
		}
	}
	return "", errors.New(remotePreviewNeedsWorkspace)
}

// resolveRemotePreviewPath mirrors Rust's `resolve_preview_path`: legacy app
// paths resolve against the workspace, `file://` URLs without a host are
// accepted, and anything naming another host is rejected.
func resolveRemotePreviewPath(target, workspace string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" || strings.HasPrefix(target, "~") {
		return "", errors.New(remotePreviewHostLocal)
	}
	if rest, ok := strings.CutPrefix(target, "file://"); ok {
		// `file://<host>/path` names another host; only hostless URLs
		// (`file:///path`, `file://C:/path`) resolve against the workspace.
		if !strings.HasPrefix(rest, "/") && remotePreviewPathConvention(rest) != remotePreviewWindows {
			return "", errors.New(remotePreviewHostLocal)
		}
		target = rest
	}
	workspace = strings.TrimSpace(workspace)
	convention := remotePreviewPathConvention(workspace)
	if remotePreviewIsAbsolute(target, convention) {
		return remotePreviewClean(target, convention), nil
	}
	if convention == "" {
		return "", errors.New(remotePreviewNeedsWorkspace)
	}
	return remotePreviewClean(workspace+"/"+target, convention), nil
}

func remotePreviewPathConvention(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, `\\`):
		return remotePreviewWindows
	case len(value) >= 3 && isASCIILetter(value[0]) && value[1] == ':' && (value[2] == '\\' || value[2] == '/'):
		return remotePreviewWindows
	case strings.HasPrefix(value, "/"):
		return remotePreviewPosix
	}
	return ""
}

func isASCIILetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func remotePreviewIsAbsolute(value, convention string) bool {
	switch convention {
	case remotePreviewPosix:
		return strings.HasPrefix(value, "/")
	case remotePreviewWindows:
		return remotePreviewPathConvention(value) == remotePreviewWindows
	}
	return false
}

func remotePreviewClean(value, convention string) string {
	unc := convention == remotePreviewWindows && strings.HasPrefix(value, `\\`)
	cleaned := path.Clean(strings.ReplaceAll(value, `\`, "/"))
	if unc {
		return "//" + strings.TrimPrefix(cleaned, "/")
	}
	return cleaned
}

// readRemoteDocumentText reads a preview file and rejects content the sidebar
// cannot display as text (Rust `read_document`).
func readRemoteDocumentText(ctx context.Context, client *remoteAppServerTUIClient, sourceThreadID, originalPath, resolvedPath string) (string, error) {
	data, err := readRemotePreviewFile(ctx, client, sourceThreadID, originalPath, resolvedPath)
	if err != nil {
		return "", err
	}
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

// readRemotePreviewFile re-resolves the original path before and after the read
// so a workspace switch cannot hand back bytes from the wrong tree (Rust
// `read_preview_file`). `fs/readFile` follows symlinks; only a server-side open
// can enforce no-follow.
func readRemotePreviewFile(ctx context.Context, client *remoteAppServerTUIClient, sourceThreadID, originalPath, resolvedPath string) ([]byte, error) {
	if remotePreviewPathConvention(resolvedPath) != remotePreviewPosix {
		return nil, errors.New(remotePreviewWindowsUnavailable)
	}
	workspace, err := remotePreviewWorkspace(ctx, client, sourceThreadID)
	if err != nil {
		return nil, err
	}
	current, err := resolveRemotePreviewPath(originalPath, workspace)
	if err != nil {
		return nil, err
	}
	if current != resolvedPath {
		return nil, errors.New(remotePreviewWorkspaceChanged)
	}
	data, err := readRemotePreviewBytes(ctx, client, resolvedPath)
	if err != nil {
		return nil, err
	}
	after, err := remotePreviewWorkspace(ctx, client, sourceThreadID)
	if err != nil {
		return nil, err
	}
	if after != workspace {
		return nil, errors.New(remotePreviewWorkspaceChanged)
	}
	return data, nil
}

func readRemotePreviewBytes(ctx context.Context, client *remoteAppServerTUIClient, remotePath string) ([]byte, error) {
	var response appserver.ReadFileResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodFSReadFile, appserver.FsReadFileParams{Path: remotePath}, &response); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil {
		return nil, err
	}
	return data, nil
}
