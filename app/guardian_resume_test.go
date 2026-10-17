package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/session"
	codextui "codex_go/tui"
)

// TestInteractiveResumeRejectsGuardianThreadLikeRust mirrors Rust #53816
// (session_resume::validate_resume_target): interactive CLI resume refuses a
// Guardian review thread and suggests its parent session.
func TestInteractiveResumeRejectsGuardianThreadLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	id := session.ThreadID("019e72f4-e09a-70f2-b2c2-a153a57b8cc0")
	parent := "019e72f4-e09a-70f2-b2c2-a153a57b8cc1"
	store := newSessionStore()
	if err := store.Create(&session.Record{
		ID:             id,
		SessionID:      "s-guardian",
		ParentThreadID: session.ThreadID(parent),
		Metadata: session.Metadata{
			Source:      session.GuardianReviewSource,
			HistoryMode: "legacy",
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	selection := codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{BackendID: codextui.ProcessBackendID(), ThreadID: string(id)},
	}
	_, err := interactiveResumeSessionHandler(nil)(selection)
	want := "Cannot resume a Guardian review thread.\nResume its parent session instead: codex resume " + parent
	if err == nil || err.Error() != want {
		t.Fatalf("guardian resume error = %v, want %q", err, want)
	}

	// Without a recorded parent the hint is omitted, matching the Rust
	// map_or_else(String::new, ..) branch.
	orphan := session.ThreadID("019e72f4-e09a-70f2-b2c2-a153a57b8cc2")
	if err := store.Create(&session.Record{
		ID:        orphan,
		SessionID: "s-guardian-orphan",
		Metadata:  session.Metadata{Source: session.GuardianReviewSource, HistoryMode: "legacy"},
	}); err != nil {
		t.Fatalf("Create(orphan) error = %v", err)
	}
	_, err = interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{BackendID: codextui.ProcessBackendID(), ThreadID: string(orphan)},
	})
	if err == nil || err.Error() != "Cannot resume a Guardian review thread." {
		t.Fatalf("orphan guardian resume error = %v", err)
	}
}

// TestInteractiveResumeStillAllowsOtherSubagentsLikeRust keeps the guard
// narrowly scoped to the Guardian "other" source.
func TestInteractiveResumeStillAllowsOtherSubagentsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	id := session.ThreadID("019e72f4-e09a-70f2-b2c2-a153a57b8cc3")
	store := newSessionStore()
	if err := store.Create(&session.Record{
		ID:        id,
		SessionID: "s-worker",
		Metadata:  session.Metadata{Source: "subagent_worker", HistoryMode: "legacy"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{BackendID: codextui.ProcessBackendID(), ThreadID: string(id)},
	}); err != nil {
		t.Fatalf("non-guardian subagent resume error = %v", err)
	}
}

// TestInteractiveRemoteResumeRejectsGuardianThreadLikeRust covers the remote
// TUI path: metadata is read with includeTurns=false before thread/resume, and
// a Guardian thread is refused without resuming it.
func TestInteractiveRemoteResumeRejectsGuardianThreadLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
	serverErrs := make(chan error, 1)
	parent := "thread-main"
	guardian := remoteSessionTestThread("thread-guardian", "guardian", false, 0)
	guardian["source"] = map[string]any{"subAgent": map[string]any{"other": "guardian"}}
	guardian["parentThreadId"] = parent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-guardian" || params.IncludeTurns {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/read params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": guardian}})
				return
			case string(appserver.MethodThreadResume):
				remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/resume must not run for a Guardian thread"))
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	backendID := codextui.NewBackendID()
	handler := interactiveRemoteResumeSessionHandler(ctx, endpoint, &cli.RootOptions{Shared: cli.SharedOptions{CWD: remoteResumeTestCWD}}, backendID)
	_, err := handler(codextui.SessionSelection{Kind: codextui.SessionSelectionResume, Target: codextui.SessionTarget{BackendID: backendID, ThreadID: "thread-guardian"}})
	want := "Cannot resume a Guardian review thread.\nResume its parent session instead: codex resume " + parent
	if err == nil || err.Error() != want {
		t.Fatalf("remote guardian resume error = %v, want %q", err, want)
	}
	if initialize := remoteTUITestReadCapturedRequest(t, requests); initialize.Method != string(appserver.MethodInitialize) {
		t.Fatalf("initialize method = %s", initialize.Method)
	}
	read := remoteTUITestReadCapturedRequest(t, requests)
	if read.Method != string(appserver.MethodThreadRead) {
		t.Fatalf("read method = %s", read.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
