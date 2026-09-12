package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// permissionProfileServer answers permissionProfile/list with the supplied
// results in order (a nil result ends the loop).
func permissionProfileServer(t *testing.T, serverConn net.Conn, results []map[string]any, errCode int) {
	t.Helper()
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			if len(results) == 0 {
				return
			}
			result := results[0]
			results = results[1:]
			if errCode != 0 {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"error":   map[string]any{"code": errCode, "message": "method not found"},
				})
				continue
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()
}

func newPermissionProfileClient(t *testing.T, results []map[string]any, errCode int) *remoteAppServerTUIClient {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	permissionProfileServer(t, serverConn, results, errCode)
	return &remoteAppServerTUIClient{
		state:     codextui.NewState(nil),
		transport: &remoteJSONLineTransport{conn: clientConn, reader: bufio.NewReader(clientConn)},
	}
}

// TestRemoteTUIListPermissionProfilesPaginates covers Rust #43340's bounded
// discovery: pages accumulate in order and the cursor is followed.
func TestRemoteTUIListPermissionProfilesPaginates(t *testing.T) {
	next := "page-2"
	client := newPermissionProfileClient(t, []map[string]any{
		{"data": []any{map[string]any{"id": "read-only-remote", "description": "Read only", "allowed": true}}, "nextCursor": next},
		{"data": []any{map[string]any{"id": "trusted", "allowed": false}}, "nextCursor": nil},
	}, 0)
	profiles, err := remoteTUIListPermissionProfiles(context.Background(), client)
	if err != nil {
		t.Fatalf("list permission profiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %#v", profiles)
	}
	if profiles[0].ID != "read-only-remote" || profiles[0].Description != "Read only" || !profiles[0].Allowed {
		t.Fatalf("first profile = %#v", profiles[0])
	}
	if profiles[1].ID != "trusted" || profiles[1].Allowed {
		t.Fatalf("second profile = %#v", profiles[1])
	}
}

// TestRemoteTUIListPermissionProfilesRejectsDuplicates covers the duplicate
// guard from permission_discovery::fetch.
func TestRemoteTUIListPermissionProfilesRejectsDuplicates(t *testing.T) {
	client := newPermissionProfileClient(t, []map[string]any{
		{"data": []any{
			map[string]any{"id": "dup", "allowed": true},
			map[string]any{"id": "dup", "allowed": true},
		}, "nextCursor": nil},
	}, 0)
	_, err := remoteTUIListPermissionProfiles(context.Background(), client)
	if err == nil || err.Error() != "The server returned duplicate permission profiles." {
		t.Fatalf("duplicate error = %v", err)
	}
}

// TestRemoteTUIListPermissionProfilesUnsupported covers the older-server
// mapping.
func TestRemoteTUIListPermissionProfilesUnsupported(t *testing.T) {
	client := newPermissionProfileClient(t, []map[string]any{{}}, -32601)
	if _, err := remoteTUIListPermissionProfiles(context.Background(), client); !errors.Is(err, codextea.ErrNamedPermissionProfilesUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
}

// TestRemoteTUIPermissionRequestUnsupportedMapping pins the RPC error mapping.
func TestRemoteTUIPermissionRequestUnsupportedMapping(t *testing.T) {
	if !remoteTUIPermissionDiscoveryUnsupported(&remoteRPCError{Code: -32601, Message: "method not found"}) {
		t.Fatal("-32601 must map to an unsupported discovery")
	}
	if !remoteTUIPermissionDiscoveryUnsupported(&remoteRPCError{Code: -32600, Message: "permissionProfile/list requires experimentalApi capability"}) {
		t.Fatal("the permissionProfile/list invalid-request message must map to unsupported")
	}
	if remoteTUIPermissionDiscoveryUnsupported(&remoteRPCError{Code: -32602, Message: "invalid params"}) {
		t.Fatal("other errors must propagate")
	}
	if !remoteTUIPermissionUpdateUnsupported(&remoteRPCError{Code: -32600, Message: "thread/settings/update requires experimentalApi capability"}) {
		t.Fatal("the thread/settings/update invalid-request message must map to unsupported")
	}
	if remoteTUIPermissionUpdateUnsupported(errors.New("boom")) {
		t.Fatal("plain errors must propagate")
	}
}

var _ = appserver.MethodPermissionProfileList
