package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newAuthorizationRequiringExecServerForTest serves environment/info only when
// the WebSocket upgrade carries the expected Authorization header.
func newAuthorizationRequiringExecServerForTest(t *testing.T, expectedAuthorization string) (string, *int32Recorder) {
	t.Helper()
	attempts := &int32Recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.increment()
		if got := r.Header.Get("Authorization"); got != expectedAuthorization {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "test-session"})
		}); err != nil {
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "initialized", nil); err != nil {
			return
		}
		_ = expectExecServerRequestForTest(ctx, conn, "environment/info", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{
				"shell": map[string]any{"name": "zsh", "path": "/bin/zsh"},
				"cwd":   "file:///workspace",
			})
		})
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), attempts
}

type int32Recorder struct {
	count int
}

func (r *int32Recorder) increment() { r.count++ }
func (r *int32Recorder) value() int { return r.count }

// Rust parity: codex-rs/cli/tests/exec_server_websocket_auth.rs and
// environment_processor.rs (#47648): environment/add accepts an optional bearer
// token, requires wss:// or a loopback destination for it, and sends it on the
// executor connection while omitted or null tokens keep the unauthenticated
// behavior.
func TestEnvironmentAddAuthBearerTokenLikeRust(t *testing.T) {
	token := "private-token"
	wss := "wss://executor.example/exec"
	plainRemote := "ws://executor.example/exec"
	loopback := "ws://127.0.0.1:8080/exec"
	blank := "   "
	for _, testCase := range []struct {
		name        string
		url         string
		token       *string
		wantErr     bool
		wantHeaders bool
	}{
		{name: "wss accepts a token", url: wss, token: &token, wantHeaders: true},
		{name: "loopback accepts a token", url: loopback, token: &token, wantHeaders: true},
		{name: "plain remote ws rejects a token", url: plainRemote, token: &token, wantErr: true},
		{name: "omitted token stays unauthenticated", url: plainRemote},
		{name: "null token stays unauthenticated", url: plainRemote, token: nil},
		{name: "blank token stays unauthenticated", url: plainRemote, token: &blank},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
			_, err := manager.Add(&EnvironmentAddParams{
				EnvironmentID:   "remote",
				ExecServerURL:   testCase.url,
				AuthBearerToken: testCase.token,
			})
			if testCase.wantErr {
				if !errors.Is(err, ErrInvalidEnvironmentRequest) {
					t.Fatalf("Add() error = %v, want ErrInvalidEnvironmentRequest", err)
				}
				if strings.Contains(err.Error(), token) {
					t.Fatalf("validation error leaked the token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add() error = %v", err)
			}
			record, ok := manager.Record("remote")
			if !ok || record == nil {
				t.Fatalf("record = %#v, %v", record, ok)
			}
			got := record.ExecServerHeaders.Get("Authorization")
			if testCase.wantHeaders {
				if got != "Bearer "+token {
					t.Fatalf("Authorization = %q, want the bearer header", got)
				}
				options := execServerDialOptions(record)
				if options == nil || options.HTTPHeader.Get("Authorization") != "Bearer "+token {
					t.Fatalf("dial options = %#v, want the bearer header", options)
				}
				return
			}
			if got != "" || len(record.ExecServerHeaders) != 0 {
				t.Fatalf("unauthenticated record headers = %#v, want none", record.ExecServerHeaders)
			}
		})
	}
}

// TestEnvironmentAddRPCAcceptsAuthBearerTokenLikeRust drives the wire name and
// proves the token reaches the executor connection.
func TestEnvironmentAddRPCAcceptsAuthBearerTokenLikeRust(t *testing.T) {
	const token = "private-token"
	execServerURL, attempts := newAuthorizationRequiringExecServerForTest(t, "Bearer "+token)
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })

	response := router.Handle(requestWithParams(t, IntID(1), MethodEnvironmentAdd, map[string]any{
		"environmentId":   "remote",
		"execServerUrl":   execServerURL,
		"authBearerToken": token,
	}))
	if response.Error != nil {
		t.Fatalf("environment/add error = %+v", response.Error)
	}
	info := router.Handle(requestWithParams(t, IntID(2), MethodEnvironmentInfo, map[string]any{"environmentId": "remote"}))
	if info.Error != nil {
		t.Fatalf("environment/info error = %+v", info.Error)
	}
	result, ok := info.Result.(*EnvironmentInfoResponse)
	if !ok || result.Shell.Name != "zsh" || result.CWD == nil || *result.CWD != "file:///workspace" {
		t.Fatalf("environment/info result = %#v", info.Result)
	}
	if attempts.value() == 0 {
		t.Fatal("executor was never contacted")
	}

	// A null token keeps the unauthenticated behavior: the same server rejects
	// the connection, so the request fails instead of silently authenticating.
	second := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = second.Close() })
	if response := second.Handle(requestWithParams(t, IntID(3), MethodEnvironmentAdd, map[string]any{
		"environmentId":   "remote",
		"execServerUrl":   execServerURL,
		"authBearerToken": nil,
	})); response.Error != nil {
		t.Fatalf("environment/add with a null token error = %+v", response.Error)
	}
	unauthorized := second.Handle(requestWithParams(t, IntID(4), MethodEnvironmentInfo, map[string]any{"environmentId": "remote"}))
	if unauthorized.Error == nil {
		t.Fatal("environment/info without a token succeeded against an authenticated executor")
	}
}

// TestEnvironmentAddRPCAcceptsNullAuthBearerTokenLikeRust pins the explicit
// null case at the wire boundary (Rust preserves unauthenticated behavior).
func TestEnvironmentAddRPCAcceptsNullAuthBearerTokenLikeRust(t *testing.T) {
	var params EnvironmentAddParams
	if err := json.Unmarshal([]byte(`{"environmentId":"remote","execServerUrl":"ws://127.0.0.1:1/exec","authBearerToken":null}`), &params); err != nil {
		t.Fatalf("Unmarshal(authBearerToken: null) error = %v", err)
	}
	if params.AuthBearerToken != nil {
		t.Fatalf("AuthBearerToken = %#v, want nil", params.AuthBearerToken)
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a token-free registration to pass", err)
	}
}
