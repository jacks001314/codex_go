package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/execserver"
	"codex_go/mcp"
	"codex_go/turn"

	"github.com/coder/websocket"
)

func TestManagerAddInfoAndList(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "powershell", Path: "powershell.exe"}, t.TempDir())
	timeout := uint64(250)
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote-2", ExecServerURL: "wss://example.test/exec", ConnectTimeoutMS: &timeout}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote-1", ExecServerURL: "ws://example.test/exec"}); err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	if err := manager.SetInfo("remote-2", EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "/workspace"); err != nil {
		t.Fatalf("SetInfo() error = %v", err)
	}

	info, err := manager.Info(&EnvironmentInfoParams{EnvironmentID: "remote-2"})
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Shell.Name != "bash" || info.CWD == nil || !strings.HasPrefix(*info.CWD, "file://") {
		t.Fatalf("Info() = %+v", info)
	}
	if err := manager.SetInfo("remote-2", EnvironmentShellInfo{Name: "zsh", Path: "/bin/zsh"}, "/updated"); err != nil {
		t.Fatalf("SetInfo(update) error = %v", err)
	}
	updated, err := manager.Info(&EnvironmentInfoParams{EnvironmentID: "remote-2"})
	if err != nil || updated.Shell.Name != "zsh" || updated.CWD == nil || !strings.HasSuffix(*updated.CWD, "/updated") {
		t.Fatalf("updated Info() = %+v, %v", updated, err)
	}

	records := manager.List()
	if len(records) != 2 || records[0].EnvironmentID != "remote-1" || records[1].EnvironmentID != "remote-2" {
		t.Fatalf("List() = %+v, want sorted remote-1, remote-2", records)
	}
	*records[1].ConnectTimeoutMS = 1
	if manager.List()[1].ConnectTimeoutMS == nil || *manager.List()[1].ConnectTimeoutMS != timeout {
		t.Fatalf("List() leaked pointer mutation")
	}
}

func TestManagerValidation(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "", ExecServerURL: "https://example.test"}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("Add(empty id) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: "relative"}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("Add(relative url) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: "https://example.test"}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("Add(non websocket url) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.Info(&EnvironmentInfoParams{EnvironmentID: "missing"}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("Info(missing) error = %v, want ErrInvalidEnvironmentRequest", err)
	} else if err == nil || err.Error() != "invalid environment request: unknown environment id `missing`" {
		t.Fatalf("Info(missing) error = %v, want Rust-shaped message", err)
	}
	if err := manager.SetInfo("env", EnvironmentShellInfo{Name: "", Path: "/bin/sh"}, ""); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("SetInfo(bad shell) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
}

func TestEnvironmentManagerAddsNoiseEnvironmentWithoutURL(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "/workspace")
	provider := execserver.NoiseRendezvousConnectProviderFunc(func(context.Context, execserver.RemotePublicKey) (*execserver.NoiseRendezvousConnectBundle, error) {
		return nil, errors.New("registry unavailable")
	})
	if err := manager.AddNoise("remote", provider); err != nil {
		t.Fatalf("AddNoise() error = %v", err)
	}
	record, ok := manager.Record("remote")
	if !ok || record == nil || record.ExecServerURL != "" || record.NoiseProvider == nil {
		t.Fatalf("noise environment record = %#v, %v", record, ok)
	}
	if _, err := manager.InfoContext(context.Background(), &EnvironmentInfoParams{EnvironmentID: "remote"}); err == nil || !strings.Contains(err.Error(), "registry unavailable") {
		t.Fatalf("InfoContext() error = %v", err)
	}
	if err := manager.AddNoise("", provider); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("AddNoise(empty) error = %v", err)
	}
	if err := manager.AddNoise("remote", nil); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("AddNoise(nil provider) error = %v", err)
	}
}

func TestManagerInfoFetchesRemoteExecServer(t *testing.T) {
	execServerURL, done := newEnvironmentInfoExecServerForTest(t, map[string]any{
		"shell": map[string]any{"name": "zsh", "path": "/bin/zsh"},
		"cwd":   "file:///workspace",
	})
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: execServerURL}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	info, err := manager.Info(&EnvironmentInfoParams{EnvironmentID: "remote"})
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Shell.Name != "zsh" || info.Shell.Path != "/bin/zsh" || info.CWD == nil || *info.CWD != "file:///workspace" {
		t.Fatalf("Info() = %+v", info)
	}
	waitEnvironmentInfoExecServerForTest(t, done)
}

func TestManagerStatusReportsRustShapedStates(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "pending", ExecServerURL: "ws://example.test/exec"}); err != nil {
		t.Fatalf("Add(pending) error = %v", err)
	}
	if err := manager.SetInfo("local", EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "/workspace"); err != nil {
		t.Fatalf("SetInfo(local) error = %v", err)
	}

	local, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "local"})
	if err != nil || local.Status != EnvironmentStatusReady || local.Error != nil {
		t.Fatalf("Status(local) = %+v, %v", local, err)
	}
	missing, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "missing"})
	if err != nil || missing.Status != EnvironmentStatusUnknown || missing.Error == nil || *missing.Error != "unknown environment id `missing`" {
		t.Fatalf("Status(missing) = %+v, %v", missing, err)
	}
	disconnected, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "pending"})
	if err != nil || disconnected.Status != EnvironmentStatusDisconnected || disconnected.Error == nil {
		t.Fatalf("Status(unreachable websocket) = %+v, %v", disconnected, err)
	}
}

func TestManagerStatusFetchesRemoteExecServer(t *testing.T) {
	execServerURL, done := newEnvironmentStatusExecServerForTest(t, map[string]any{"status": "ready"})
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: execServerURL}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "remote"})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Status != EnvironmentStatusReady || status.Error != nil {
		t.Fatalf("Status() = %+v", status)
	}
	waitEnvironmentInfoExecServerForTest(t, done)
}

func TestManagerStatusReportsPendingForConnectedUninitializedExecServer(t *testing.T) {
	execServerURL, done := newPendingEnvironmentStatusExecServerForTest(t)
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	timeoutMS := uint64(50)
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "pending", ExecServerURL: execServerURL, ConnectTimeoutMS: &timeoutMS}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "pending"})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Status != EnvironmentStatusPending || status.Error != nil {
		t.Fatalf("Status() = %+v", status)
	}
	waitEnvironmentInfoExecServerForTest(t, done)
}

func TestManagerRemove(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: "ws://example.test"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !manager.Remove("env") {
		t.Fatalf("Remove() = false, want true")
	}
	if manager.Remove("env") {
		t.Fatalf("Remove(second) = true, want false")
	}
}

func newEnvironmentInfoExecServerForTest(t *testing.T, result map[string]any) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			done <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "test-session"})
		}); err != nil {
			done <- err
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "initialized", nil); err != nil {
			done <- err
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "environment/info", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], result)
		}); err != nil {
			done <- err
			return
		}
		done <- nil
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), done
}

func newEnvironmentStatusExecServerForTest(t *testing.T, result map[string]any) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			done <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "status-session"})
		}); err != nil {
			done <- err
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "initialized", nil); err != nil {
			done <- err
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "environment/status", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], result)
		}); err != nil {
			done <- err
			return
		}
		done <- nil
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), done
}

func newPendingEnvironmentStatusExecServerForTest(t *testing.T) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			done <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", nil); err != nil {
			done <- err
			return
		}
		time.Sleep(150 * time.Millisecond)
		done <- nil
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), done
}

func TestFetchRemoteEnvironmentStatusRetriesAfterTransientStartupFailureLikeRust(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			// Rust 38020: a retryable failure during the initial remote
			// exec-server connection must not leave the environment
			// disconnected on later readiness checks. Go status fetches dial
			// per call, so the failed startup is not memoized.
			http.Error(w, "transient startup failure", http.StatusInternalServerError)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			t.Errorf("websocket accept error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "status-session"})
		}); err != nil {
			t.Errorf("initialize error = %v", err)
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "initialized", nil); err != nil {
			t.Errorf("initialized error = %v", err)
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "environment/status", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"status": "ready"})
		}); err != nil {
			t.Errorf("environment/status error = %v", err)
			return
		}
	}))
	t.Cleanup(server.Close)
	record := &EnvironmentRecord{ExecServerURL: "ws" + strings.TrimPrefix(server.URL, "http")}

	if _, err := fetchRemoteEnvironmentStatus(context.Background(), record); err == nil {
		t.Fatal("first readiness check should fail transiently")
	}
	status, err := fetchRemoteEnvironmentStatus(context.Background(), record)
	if err != nil {
		t.Fatalf("later readiness check should retry startup: %v", err)
	}
	if status == nil || status.Status != EnvironmentStatusReady {
		t.Fatalf("EnvironmentStatus after retry = %#v", status)
	}
}

func newRemoteSkillsExecServerForTest(t *testing.T, rootPath string, files map[string]string) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			done <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "skills-session"})
		}); err != nil {
			done <- err
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "initialized", nil); err != nil {
			done <- err
			return
		}
		paths := make([]string, 0, len(files))
		for path := range files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		if err := expectExecServerRequestForTest(ctx, conn, "fs/walk", func(request map[string]any) error {
			params, _ := request["params"].(map[string]any)
			if params["path"] != rootPath {
				return fmt.Errorf("fs/walk path = %v, want %s", params["path"], rootPath)
			}
			options, _ := params["options"].(map[string]any)
			for key, want := range map[string]int{
				"maxDepth":       remoteSkillWalkMaxDepth,
				"maxDirectories": remoteSkillWalkMaxDirectories,
				"maxEntries":     remoteSkillWalkMaxEntries,
			} {
				got, _ := options[key].(float64)
				if int(got) != want {
					return fmt.Errorf("fs/walk option %s = %v, want %d", key, options[key], want)
				}
			}
			if options["followDirectorySymlinks"] != true {
				return fmt.Errorf("fs/walk followDirectorySymlinks = %v, want true", options["followDirectorySymlinks"])
			}
			entries := make([]map[string]any, 0, len(paths))
			for _, path := range paths {
				entries = append(entries, map[string]any{"path": path, "kind": "file"})
			}
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"entries": entries, "errors": []any{}, "truncated": false})
		}); err != nil {
			done <- err
			return
		}
		remainingReads := len(files)
		for remainingReads > 0 {
			if err := expectExecServerRequestForTest(ctx, conn, "", func(request map[string]any) error {
				params, _ := request["params"].(map[string]any)
				path, _ := params["path"].(string)
				switch request["method"] {
				case "fs/getMetadata":
					_, ok := files[path]
					return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{
						"isFile":      ok,
						"isDirectory": false,
						"isSymlink":   false,
					})
				case "fs/readFile":
					contents, ok := files[path]
					if !ok {
						return fmt.Errorf("unexpected fs/readFile path %q", path)
					}
					remainingReads--
					return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{
						"dataBase64": base64.StdEncoding.EncodeToString([]byte(contents)),
					})
				default:
					return fmt.Errorf("method = %v, want fs/getMetadata or fs/readFile", request["method"])
				}
			}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), done
}

func waitEnvironmentInfoExecServerForTest(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exec-server fixture error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("exec-server fixture did not finish")
	}
}

func expectExecServerRequestForTest(ctx context.Context, conn *websocket.Conn, method string, respond func(map[string]any) error) error {
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
		return fmt.Errorf("message type = %v, want JSON", messageType)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	if method != "" && request["method"] != method {
		return fmt.Errorf("method = %v, want %s", request["method"], method)
	}
	if respond != nil {
		return respond(request)
	}
	return nil
}

func writeExecServerResponseForTest(ctx context.Context, conn *websocket.Conn, id any, result any) error {
	data, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// TestEnvironmentAddStoresRequiredSkillsLikeRust mirrors Rust #51157: an
// `environment/add` registration carries the same per-environment
// `skills.required` list that `environments.toml` sets statically, and the
// requirements follow the registration until it is replaced or removed.
func TestEnvironmentAddStoresRequiredSkillsLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "required",
		ExecServerURL: "ws://example.test/exec",
		Skills:        &EnvironmentSkillsParams{Required: []string{"review", "lint"}},
	}); err != nil {
		t.Fatalf("Add(required) error = %v", err)
	}
	if got := manager.RequiredSkills("required"); !reflect.DeepEqual(got, []string{"review", "lint"}) {
		t.Fatalf("RequiredSkills(required) = %#v, want [review lint]", got)
	}
	// The accessor copies, so a caller cannot mutate the registration.
	got := manager.RequiredSkills("required")
	got[0] = "mutated"
	if again := manager.RequiredSkills("required"); again[0] != "review" {
		t.Fatalf("RequiredSkills() after caller mutation = %#v", again)
	}
	// An environment with no skills and an unknown environment have no
	// requirements.
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "plain", ExecServerURL: "ws://example.test/exec"}); err != nil {
		t.Fatalf("Add(plain) error = %v", err)
	}
	if got := manager.RequiredSkills("plain"); len(got) != 0 {
		t.Fatalf("RequiredSkills(plain) = %#v, want none", got)
	}
	if got := manager.RequiredSkills("missing"); len(got) != 0 {
		t.Fatalf("RequiredSkills(missing) = %#v, want none", got)
	}
	// Re-registering the environment replaces its requirements (upsert), and
	// removing it drops them.
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "required",
		ExecServerURL: "ws://example.test/exec",
		Skills:        &EnvironmentSkillsParams{Required: []string{"format"}},
	}); err != nil {
		t.Fatalf("Add(replacement) error = %v", err)
	}
	if got := manager.RequiredSkills("required"); !reflect.DeepEqual(got, []string{"format"}) {
		t.Fatalf("RequiredSkills(required) after replacement = %#v, want [format]", got)
	}
	if !manager.Remove("required") {
		t.Fatalf("Remove(required) = false, want true")
	}
	if got := manager.RequiredSkills("required"); len(got) != 0 {
		t.Fatalf("RequiredSkills(required) after removal = %#v, want none", got)
	}
}

// TestEnvironmentAddDecodesRequiredSkillsLikeRust pins the wire shape Rust's
// `EnvironmentSkillsParams` uses for `environment/add` (Rust #51157): an
// optional `skills` object whose `required` list carries the exact catalog
// names, omitted when absent.
func TestEnvironmentAddDecodesRequiredSkillsLikeRust(t *testing.T) {
	var params EnvironmentAddParams
	if err := json.Unmarshal([]byte(`{"environmentId":"required","execServerUrl":"ws://example.test/exec","skills":{"required":["review"]}}`), &params); err != nil {
		t.Fatalf("Unmarshal(skills) error = %v", err)
	}
	if params.Skills == nil || !reflect.DeepEqual(params.Skills.Required, []string{"review"}) {
		t.Fatalf("decoded skills = %#v, want [review]", params.Skills)
	}
	var absent EnvironmentAddParams
	if err := json.Unmarshal([]byte(`{"environmentId":"plain","execServerUrl":"ws://example.test/exec"}`), &absent); err != nil {
		t.Fatalf("Unmarshal(absent) error = %v", err)
	}
	if absent.Skills != nil {
		t.Fatalf("decoded absent skills = %#v, want nil", absent.Skills)
	}
	var empty EnvironmentAddParams
	if err := json.Unmarshal([]byte(`{"environmentId":"empty","execServerUrl":"ws://example.test/exec","skills":{}}`), &empty); err != nil {
		t.Fatalf("Unmarshal(empty skills) error = %v", err)
	}
	if empty.Skills == nil || len(empty.Skills.Required) != 0 {
		t.Fatalf("decoded empty skills = %#v, want an empty required list", empty.Skills)
	}
}

// TestRequiredSkillsForSelectionsGatesInferenceLikeRust mirrors Rust #51157's
// `required_environment_skill_gates_inference` end to end: the registration's
// requirements reach only their own environment, a failed selection still
// enforces them, and dropping the selection (or an unavailable extension) is
// the only recovery besides supplying the skill.
func TestRequiredSkillsForSelectionsGatesInferenceLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "required",
		ExecServerURL: "ws://example.test/exec",
		Skills:        &EnvironmentSkillsParams{Required: []string{"review"}},
	}); err != nil {
		t.Fatalf("Add(required) error = %v", err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "primary", ExecServerURL: "ws://example.test/exec"}); err != nil {
		t.Fatalf("Add(primary) error = %v", err)
	}

	selections := mcp.NewSelectedEnvironments([]mcp.TurnEnvironmentSelection{
		{EnvironmentID: "primary", State: mcp.EnvironmentSelectionReady},
		{EnvironmentID: "required", State: mcp.EnvironmentSelectionFailed, Error: "configuration unavailable"},
	})
	requirements := manager.RequiredSkillsForSelections(selections)
	if len(requirements) != 1 || requirements[0].EnvironmentID != "required" || len(requirements[0].SkillNames) != 1 || requirements[0].SkillNames[0] != "review" {
		t.Fatalf("requirements = %#v, want the failed selection's review requirement", requirements)
	}

	const wantMessage = `Fatal error: Required skill "review" from environment "required" is unavailable`
	// The skill is supplied by the other environment: still unavailable.
	wrongEnvironment := mcp.RequiredSkillsCatalog{Available: true, Entries: []mcp.EnvironmentSkillCatalogEntry{
		{Name: "review", EnvironmentID: "primary", Enabled: true},
	}}
	if err := mcp.ValidateRequiredEnvironmentSkills(requirements, wrongEnvironment); err == nil || err.Error() != wantMessage {
		t.Fatalf("wrong-environment error = %v, want %q", err, wantMessage)
	}
	// A missing skill and a disabled skill fail the same way.
	if err := mcp.ValidateRequiredEnvironmentSkills(requirements, mcp.RequiredSkillsCatalog{Available: true}); err == nil || err.Error() != wantMessage {
		t.Fatalf("missing-skill error = %v, want %q", err, wantMessage)
	}
	disabled := mcp.RequiredSkillsCatalog{Available: true, Entries: []mcp.EnvironmentSkillCatalogEntry{
		{Name: "review", EnvironmentID: "required", Enabled: false},
	}}
	if err := mcp.ValidateRequiredEnvironmentSkills(requirements, disabled); err == nil || err.Error() != wantMessage {
		t.Fatalf("disabled-skill error = %v, want %q", err, wantMessage)
	}
	// The registration's own environment supplies the enabled skill.
	available := mcp.RequiredSkillsCatalog{Available: true, Entries: []mcp.EnvironmentSkillCatalogEntry{
		{Name: "review", EnvironmentID: "required", Enabled: true},
	}}
	if err := mcp.ValidateRequiredEnvironmentSkills(requirements, available); err != nil {
		t.Fatalf("available-skill error = %v, want nil", err)
	}
	// Deselection: the requirement disappears, so inference proceeds without
	// validating anything.
	deselected := mcp.NewSelectedEnvironments([]mcp.TurnEnvironmentSelection{{EnvironmentID: "primary", State: mcp.EnvironmentSelectionReady}})
	if got := manager.RequiredSkillsForSelections(deselected); len(got) != 0 {
		t.Fatalf("deselected requirements = %#v, want none", got)
	}
	// No skills extension state and no requirements is still valid (Rust's
	// early return), while a requirement without state is an unavailable
	// extension rather than a missing skill.
	if err := mcp.ValidateRequiredEnvironmentSkills(nil, mcp.RequiredSkillsCatalog{}); err != nil {
		t.Fatalf("no-requirement error = %v, want nil", err)
	}
	if err := mcp.ValidateRequiredEnvironmentSkills(requirements, mcp.RequiredSkillsCatalog{}); !errors.Is(err, mcp.ErrRequiredSkillsExtensionUnavailable) {
		t.Fatalf("unavailable-extension error = %v, want ErrRequiredSkillsExtensionUnavailable", err)
	}
}

// TestRequiredSkillsForSelectionsWithoutSnapshotLikeRust mirrors Rust's
// threadless discovery: a nil snapshot carries no requirements.
func TestRequiredSkillsForSelectionsWithoutSnapshotLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "required",
		ExecServerURL: "ws://example.test/exec",
		Skills:        &EnvironmentSkillsParams{Required: []string{"review"}},
	}); err != nil {
		t.Fatalf("Add(required) error = %v", err)
	}
	if got := manager.RequiredSkillsForSelections(nil); got != nil {
		t.Fatalf("nil snapshot requirements = %#v, want nil", got)
	}
	// A nil manager (no environment services configured) is nil-safe here.
	var nilManager *EnvironmentManager
	if got := nilManager.RequiredSkillsForSelections(mcp.NewSelectedEnvironments([]mcp.TurnEnvironmentSelection{{EnvironmentID: "required"}})); len(got) != 0 {
		t.Fatalf("nil manager requirements = %#v, want none", got)
	}
}

// TestRequiredSkillsForTurnExemptsGuardianReviewersLikeRust mirrors Rust
// #51157's Guardian exemption (`guardian::is_basic_session_source`): an
// isolated Guardian reviewer has no skill catalog, so its turn enforces no
// environment requirements, while an ordinary turn with the same selections
// still does. Go identifies the guardian through the turn's originator or its
// `x-openai-subagent` metadata (guardianTurnStart).
func TestRequiredSkillsForTurnExemptsGuardianReviewersLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "required",
		ExecServerURL: "ws://example.test/exec",
		Skills:        &EnvironmentSkillsParams{Required: []string{"review"}},
	}); err != nil {
		t.Fatalf("Add(required) error = %v", err)
	}
	environments := []map[string]any{{"environmentId": "required", "cwd": "/tmp/required"}}

	ordinary := &turn.TurnStartParams{ThreadID: "thread-1", Environments: environments}
	requirements := manager.RequiredSkillsForTurn(ordinary)
	if len(requirements) != 1 || requirements[0].EnvironmentID != "required" {
		t.Fatalf("ordinary requirements = %#v, want the required environment", requirements)
	}
	for _, guardian := range []*turn.TurnStartParams{
		{ThreadID: "thread-1", Environments: environments, Originator: "guardian"},
		{ThreadID: "thread-1", Environments: environments, ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "guardian"}},
		{ThreadID: "thread-1", Environments: environments, ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "GUARDIAN"}},
	} {
		if got := manager.RequiredSkillsForTurn(guardian); len(got) != 0 {
			t.Fatalf("guardian requirements = %#v, want none", got)
		}
	}
	// A non-guardian sub-agent is not exempt.
	other := &turn.TurnStartParams{ThreadID: "thread-1", Environments: environments, ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "reviewer"}}
	if got := manager.RequiredSkillsForTurn(other); len(got) != 1 {
		t.Fatalf("non-guardian sub-agent requirements = %#v, want the required environment", got)
	}
	if got := manager.RequiredSkillsForTurn(nil); got != nil {
		t.Fatalf("nil params requirements = %#v, want nil", got)
	}
}
