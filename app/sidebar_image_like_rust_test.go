package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/sandbox"
	"codex_go/tui/markdown"
	codextea "codex_go/tui/tea"
)

func sidebarPNGBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 30, 20))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestEmbeddedSidebarImageBytesAndPermissionsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	var err error
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "picture.png"), sidebarPNGBytes(t), 0600); err != nil {
		t.Fatal(err)
	}
	profile := sandbox.WorkspaceWritePermissionProfile()
	host := newEmbeddedSidebarDocumentHost(func() string { return workspace }, fixedPreviewPermissions(&profile, workspace))
	view := host.open(codextea.SidebarDocumentRequest{Path: "picture.png"})
	if view.Err != "" || view.Image == nil || view.Image.Width != 30 || view.Code {
		t.Fatalf("image view: %#v", view)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.png"), sidebarPNGBytes(t), 0600); err != nil {
		t.Fatal(err)
	}
	denied := host.open(codextea.SidebarDocumentRequest{Path: filepath.Join(outside, "outside.png")})
	if denied.Err != errPreviewOutsideWorkspace.Error() || denied.Image != nil {
		t.Fatalf("outside read: %#v", denied)
	}
	// The image path still goes through all the ordinary permission checks.
	forbidden := profile
	forbidden.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{Path: sandbox.FileSystemPath{Type: "path", Path: filepath.Join(workspace, "picture.png")}, Access: sandbox.FileSystemAccessDeny}}
	deniedHost := newEmbeddedSidebarDocumentHost(func() string { return workspace }, fixedPreviewPermissions(&forbidden, workspace))
	denied = deniedHost.open(codextea.SidebarDocumentRequest{Path: "picture.png"})
	if denied.Err != errPreviewReadDenied.Error() || denied.Image != nil {
		t.Fatalf("denied image: %#v", denied)
	}
}

func TestSidebarImageDecodeFallsBackToTextLikeRust(t *testing.T) {
	d := newSidebarDocument("thread", "renamed.png", -1, nil)
	d.loadedBytes([]byte("plain text renamed as an image"))
	if d.state != sidebarDocumentReady || d.image != nil || !strings.Contains(d.text, "plain text") {
		t.Fatalf("text fallback: %#v", d)
	}
	d = newSidebarDocument("thread", "bad.png", -1, nil)
	d.loadedBytes([]byte{0xff, 0, 0xfe})
	if d.state != sidebarDocumentFailed || d.image != nil {
		t.Fatalf("invalid image: %#v", d)
	}
}

func TestRemoteSidebarImageUsesAuthorizedServerBytesLikeRust(t *testing.T) {
	data := sidebarPNGBytes(t)
	client, transport := newPreviewStubClient(func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case string(appserver.MethodThreadRead):
			return previewThreadResult("/workspace/repo"), ""
		case string(appserver.MethodFSReadFile):
			return previewFileResult(data), ""
		}
		return nil, "unexpected method " + method
	})
	d := openSidebarDocument(context.Background(), client, "thread", "picture.png", -1, nil)
	view := sidebarDocumentViewOf(d)
	if view.Err != "" || view.Image == nil || view.Image.Width != 30 {
		t.Fatalf("remote image: %#v", view)
	}
	if len(transport.calls(string(appserver.MethodFSReadFile))) != 1 {
		t.Fatal("remote image did not use fs/readFile exactly once")
	}
}

func TestRestoredGeneratedImageFollowsAnswerLikeRust(t *testing.T) {
	thread := &appserver.Thread{ID: "thread", CWD: "/workspace", Turns: []appserver.Turn{{ID: "turn", Status: "completed", Items: []appserver.ThreadItem{
		{ID: "img", Type: "imageGeneration", Data: map[string]any{"status": "completed", "savedPath": "/workspace/image.png", "revisedPrompt": "a picture"}},
		{ID: "answer", Type: "agentMessage", Text: "Here is your picture."},
	}}}}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjection(""), false)
	answer, image := -1, -1
	for index, message := range messages {
		if strings.Contains(message.Text, "Here is your picture.") {
			answer = index
		}
		if message.ImagePath != "" {
			image = index
		}
	}
	if answer < 0 || image <= answer {
		t.Fatalf("restored order answer=%d image=%d: %#v", answer, image, messages)
	}
}

func TestSidebarImageLocationUsesSourceRulesLikeRust(t *testing.T) {
	d := newSidebarDocument("thread", "picture.png", -1, &markdown.SidebarSourceLocation{Line: 1, Column: 1})
	d.loadedBytes(sidebarPNGBytes(t))
	if d.image != nil || d.state != sidebarDocumentFailed {
		t.Fatalf("coordinate link incorrectly decoded an image: %#v", d)
	}
}
