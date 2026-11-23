package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// commandExecServer answers command/exec and permissionProfile/list while
// recording the parameters it received.
type commandExecServer struct {
	requests []map[string]json.RawMessage
	profiles []string
	stdout   string
	exitCode int32
	stderr   string
}

func (s *commandExecServer) serve(t *testing.T, conn net.Conn) {
	t.Helper()
	go func() {
		defer conn.Close()
		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)
		for {
			var request struct {
				ID     json.RawMessage            `json:"id"`
				Method string                     `json:"method"`
				Params map[string]json.RawMessage `json:"params"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			s.requests = append(s.requests, request.Params)
			switch request.Method {
			case string(appserver.MethodCommandExec):
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result": map[string]any{
						"exitCode": s.exitCode,
						"stdout":   s.stdout,
						"stderr":   s.stderr,
					},
				})
			case string(appserver.MethodPermissionProfileList):
				data := []any{}
				for _, id := range s.profiles {
					data = append(data, map[string]any{"id": id, "allowed": true})
				}
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result":  map[string]any{"data": data, "nextCursor": nil},
				})
			default:
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"error":   map[string]any{"code": -32601, "message": "method not found"},
				})
			}
		}
	}()
}

func newCommandExecDiffRunner(t *testing.T, server *commandExecServer) *remoteTUIDiffRunner {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	server.serve(t, serverConn)
	return &remoteTUIDiffRunner{client: &remoteAppServerTUIClient{
		state:     codextui.NewState(nil),
		transport: &remoteJSONLineTransport{conn: clientConn, reader: bufio.NewReader(clientConn)},
	}}
}

// TestRemoteDiffRunnerForwardsPermissionProfileLikeRust covers Rust #63400:
// diff Git commands travel through command/exec with the session profile and
// the profiled environment intact.
func TestRemoteDiffRunnerForwardsPermissionProfileLikeRust(t *testing.T) {
	server := &commandExecServer{stdout: "true\n"}
	runner := newCommandExecDiffRunner(t, server)
	command := codextui.NewWorkspaceCommand("/workspace", "git", "rev-parse", "--is-inside-work-tree").
		WithEnv("GIT_NO_LAZY_FETCH", "1").
		WithEnv("GIT_ALLOW_PROTOCOL", "")
	command.PermissionProfile = "trusted"
	output, err := runner.RunWorkspaceCommand(context.Background(), command)
	if err != nil {
		t.Fatalf("RunWorkspaceCommand: %v", err)
	}
	if output.Stdout != "true\n" || output.ExitCode != 0 {
		t.Fatalf("output = %#v", output)
	}
	if len(server.requests) != 1 {
		t.Fatalf("requests = %#v", server.requests)
	}
	params := server.requests[0]
	var profile string
	if err := json.Unmarshal(params["permissionProfile"], &profile); err != nil || profile != "trusted" {
		t.Fatalf("permissionProfile = %q err=%v", profile, err)
	}
	var cwd string
	if err := json.Unmarshal(params["cwd"], &cwd); err != nil || cwd != "/workspace" {
		t.Fatalf("cwd = %q err=%v", cwd, err)
	}
	var env map[string]*string
	if err := json.Unmarshal(params["env"], &env); err != nil {
		t.Fatalf("env: %v", err)
	}
	if env["GIT_NO_LAZY_FETCH"] == nil || *env["GIT_NO_LAZY_FETCH"] != "1" {
		t.Fatalf("GIT_NO_LAZY_FETCH = %#v", env["GIT_NO_LAZY_FETCH"])
	}
	if env["GIT_ALLOW_PROTOCOL"] == nil || *env["GIT_ALLOW_PROTOCOL"] != "" {
		t.Fatalf("GIT_ALLOW_PROTOCOL = %#v", env["GIT_ALLOW_PROTOCOL"])
	}
	var argv []string
	if err := json.Unmarshal(params["command"], &argv); err != nil || strings.Join(argv, " ") != "git rev-parse --is-inside-work-tree" {
		t.Fatalf("command = %#v err=%v", argv, err)
	}
}

// TestRemoteDiffRunnerProbesProfilesLikeRust: the reattachment probe uses
// permissionProfile/list and reports whether the id still resolves.
func TestRemoteDiffRunnerProbesProfilesLikeRust(t *testing.T) {
	server := &commandExecServer{profiles: []string{"read-only", "trusted"}}
	runner := newCommandExecDiffRunner(t, server)
	exists, err := runner.HasPermissionProfile(context.Background(), "/workspace", "trusted")
	if err != nil || !exists {
		t.Fatalf("HasPermissionProfile(trusted) = %v, %v", exists, err)
	}
	exists, err = runner.HasPermissionProfile(context.Background(), "/workspace", "gone")
	if err != nil || exists {
		t.Fatalf("HasPermissionProfile(gone) = %v, %v", exists, err)
	}
}
