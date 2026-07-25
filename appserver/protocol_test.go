package appserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/turn"
)

func TestOutgoingMessagesMatchRustJSONRPCShape(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "response",
			value: OK(IntID(7), map[string]any{"ok": true}),
			want:  `{"id":7,"result":{"ok":true}}`,
		},
		{
			name:  "error",
			value: ErrorResponse(IntID(7), -32000, "Server overloaded; retry later.", nil),
			want:  `{"id":7,"error":{"code":-32000,"message":"Server overloaded; retry later."}}`,
		},
		{
			name: "config warning notification",
			value: NewNotification(NotificationConfigWarning, &config.ConfigWarningNotification{
				Summary: "queued",
			}),
			want: `{"method":"configWarning","params":{"summary":"queued","details":null}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(data) != tc.want {
				t.Fatalf("encoded = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestThreadPinningProtocolParity(t *testing.T) {
	request, err := ParseRequest([]byte(`{"id":1,"method":"thread/metadata/update","params":{"threadId":"thread-1","isPinned":false}}`))
	if err != nil {
		t.Fatalf("ParseRequest error = %v", err)
	}
	var params ThreadMetadataUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		t.Fatalf("DecodeParams error = %v", err)
	}
	if params.IsPinned == nil || *params.IsPinned {
		t.Fatalf("isPinned = %#v, want explicit false", params.IsPinned)
	}
	patch, err := MetadataPatchToSession(&params)
	if err != nil || patch.IsPinned == nil || *patch.IsPinned {
		t.Fatalf("MetadataPatchToSession() = %#v, %v", patch, err)
	}
	pinned := true
	options, err := BuildListOptions(&ThreadListParams{IsPinned: &pinned})
	if err != nil || options.IsPinned == nil || !*options.IsPinned {
		t.Fatalf("BuildListOptions() = %#v, %v", options, err)
	}
	data, err := json.Marshal(&Thread{ID: "thread-1", IsPinned: true})
	if err != nil {
		t.Fatalf("Marshal(Thread) error = %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil || encoded["isPinned"] != true {
		t.Fatalf("thread JSON = %s, %v", data, err)
	}
}

func TestRustRequestIDParity(t *testing.T) {
	// Rust app-server-protocol/src/rpc.rs defines RequestId as string | i64.
	valid := []struct {
		name     string
		raw      string
		wantID   string
		wantJSON string
	}{
		{name: "integer", raw: `{"id":7,"method":"initialize"}`, wantID: "7", wantJSON: "7"},
		{name: "string", raw: `{"id":"request-7","method":"initialize"}`, wantID: "request-7", wantJSON: `"request-7"`},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			request, err := ParseRequest([]byte(tc.raw))
			if err != nil {
				t.Fatalf("ParseRequest(%s) error = %v", tc.raw, err)
			}
			if request.ID.String() != tc.wantID {
				t.Fatalf("request id = %q, want %q", request.ID.String(), tc.wantID)
			}
			data, err := json.Marshal(request.ID)
			if err != nil {
				t.Fatalf("Marshal(RequestID) error = %v", err)
			}
			if string(data) != tc.wantJSON {
				t.Fatalf("Marshal(RequestID) = %s, want %s", data, tc.wantJSON)
			}
		})
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{name: "null", raw: `{"id":null,"method":"initialize"}`},
		{name: "float", raw: `{"id":1.5,"method":"initialize"}`},
		{name: "bool", raw: `{"id":true,"method":"initialize"}`},
		{name: "object", raw: `{"id":{"value":7},"method":"initialize"}`},
		{name: "missing", raw: `{"method":"initialize"}`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRequest([]byte(tc.raw)); err == nil {
				t.Fatalf("ParseRequest(%s) returned nil error, want Rust RequestId rejection", tc.raw)
			}
		})
	}
}

func TestRustCoreProtocolMethodParity(t *testing.T) {
	// P0 surface from Rust protocol/common.rs and protocol/v2 thread/turn modules.
	tests := []struct {
		method Method
		want   string
	}{
		{method: MethodInitialize, want: "initialize"},
		{method: MethodThreadStart, want: "thread/start"},
		{method: MethodTurnStart, want: "turn/start"},
		{method: MethodThreadList, want: "thread/list"},
		{method: MethodThreadRead, want: "thread/read"},
	}
	for _, tt := range tests {
		if string(tt.method) != tt.want {
			t.Fatalf("method constant = %q, want %q", tt.method, tt.want)
		}
	}
}

func TestRustCoreProtocolParamsDecodeParity(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		assert func(t *testing.T, request *Request)
	}{
		{
			name: "initialize",
			raw:  `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"vscode","version":"1.0.0"},"capabilities":{"experimentalApi":true}}}`,
			assert: func(t *testing.T, request *Request) {
				var params InitializeParams
				if err := request.DecodeParams(&params); err != nil {
					t.Fatalf("DecodeParams initialize error = %v", err)
				}
				if err := params.Validate(); err != nil {
					t.Fatalf("Validate initialize error = %v", err)
				}
				if params.ClientInfo.Name != "vscode" || params.Capabilities == nil || !params.Capabilities.ExperimentalAPI {
					t.Fatalf("initialize params = %+v", params)
				}
			},
		},
		{
			name: "thread/start",
			raw:  `{"id":2,"method":"thread/start","params":{"cwd":"/repo","model":"gpt-5","modelProvider":"openai","permissions":"workspace-write","historyMode":"paginated","runtimeWorkspaceRoots":["/repo"],"allowProviderModelFallback":true}}`,
			assert: func(t *testing.T, request *Request) {
				var params ThreadStartParams
				if err := request.DecodeParams(&params); err != nil {
					t.Fatalf("DecodeParams thread/start error = %v", err)
				}
				if err := params.Validate(); err != nil {
					t.Fatalf("Validate thread/start error = %v", err)
				}
				if params.CWD != "/repo" || params.ModelProvider != "openai" || params.Permissions == nil || *params.Permissions != "workspace-write" {
					t.Fatalf("thread/start params = %+v", params)
				}
			},
		},
		{
			name: "turn/start",
			raw:  `{"id":3,"method":"turn/start","params":{"threadId":"thread-1","input":[{"type":"text","text":"hello","text_elements":[]}],"permissions":"workspace-write","runtimeWorkspaceRoots":["/repo"],"responsesapiClientMetadata":{"source":"parity"}}}`,
			assert: func(t *testing.T, request *Request) {
				var params turn.TurnStartParams
				if err := request.DecodeParams(&params); err != nil {
					t.Fatalf("DecodeParams turn/start error = %v", err)
				}
				if err := params.Validate(); err != nil {
					t.Fatalf("Validate turn/start error = %v", err)
				}
				if params.ThreadID != "thread-1" || params.Permissions == nil || *params.Permissions != "workspace-write" || params.ResponsesAPIMetadata["source"] != "parity" {
					t.Fatalf("turn/start params = %+v", params)
				}
			},
		},
		{
			name: "thread/list",
			raw:  `{"id":4,"method":"thread/list","params":{"limit":10,"sortKey":"updated_at","sortDirection":"desc","parentThreadId":"thread-parent"}}`,
			assert: func(t *testing.T, request *Request) {
				var params ThreadListParams
				if err := request.DecodeParams(&params); err != nil {
					t.Fatalf("DecodeParams thread/list error = %v", err)
				}
				if err := params.Validate(); err != nil {
					t.Fatalf("Validate thread/list error = %v", err)
				}
				if params.Limit == nil || *params.Limit != 10 || params.ParentThreadID == nil || *params.ParentThreadID != "thread-parent" {
					t.Fatalf("thread/list params = %+v", params)
				}
			},
		},
		{
			name: "thread/read",
			raw:  `{"id":5,"method":"thread/read","params":{"threadId":"thread-1","includeTurns":true}}`,
			assert: func(t *testing.T, request *Request) {
				var params ThreadReadParams
				if err := request.DecodeParams(&params); err != nil {
					t.Fatalf("DecodeParams thread/read error = %v", err)
				}
				if err := params.Validate(); err != nil {
					t.Fatalf("Validate thread/read error = %v", err)
				}
				if params.ThreadID != "thread-1" || !params.IncludeTurns {
					t.Fatalf("thread/read params = %+v", params)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, err := ParseRequest([]byte(tc.raw))
			if err != nil {
				t.Fatalf("ParseRequest error = %v", err)
			}
			tc.assert(t, request)
		})
	}
}

func TestJSONRPCErrorDataIncludesMCPRemoteErrorDetails(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &mcp.MCPRemoteError{
		Method:  "tools/call",
		Code:    -32001,
		Message: "remote denied request",
		Data:    json.RawMessage(`{"reason":"policy","retry":false}`),
	})

	data := jsonRPCErrorData(err)
	if data["type"] != "mcp_remote_error" || data["method"] != "tools/call" || data["message"] != "remote denied request" {
		t.Fatalf("error data = %#v", data)
	}
	if code, ok := data["code"].(int64); !ok || code != -32001 {
		t.Fatalf("error data code = %#v, want -32001", data["code"])
	}
	payload, ok := data["data"].(map[string]any)
	if !ok {
		t.Fatalf("error data payload = %#v, want object", data["data"])
	}
	if payload["reason"] != "policy" || payload["retry"] != false {
		t.Fatalf("error data payload = %#v", payload)
	}
	if code := runtimeErrorCode(err); code != -32001 {
		t.Fatalf("runtimeErrorCode() = %d, want -32001", code)
	}
}

func TestCommandExecutionItemIncludesPluginAttribution(t *testing.T) {
	item := &ThreadItem{
		ID:   "cmd-1",
		Type: "commandExecution",
		Data: map[string]any{
			"command":    "python scripts/run.py",
			"pluginId":   "sample@openai-curated",
			"scriptPath": "scripts/run.py",
		},
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["pluginId"] != "sample@openai-curated" || payload["scriptPath"] != "scripts/run.py" {
		t.Fatalf("command execution payload = %#v", payload)
	}
}

func TestSafeCommandPluginScriptPath(t *testing.T) {
	pluginID := "sample@openai-curated"
	safe := "scripts/run.py"
	if got := safeCommandPluginScriptPath(&pluginID, &safe); got == nil || *got != safe {
		t.Fatalf("safeCommandPluginScriptPath() = %#v", got)
	}
	for _, path := range []string{"/tmp/run.py", `scripts\run.py`, "scripts/../run.py"} {
		if got := safeCommandPluginScriptPath(&pluginID, &path); got != nil {
			t.Fatalf("unsafe path %q accepted as %#v", path, got)
		}
	}
}

func TestThreadStartResponseSandboxUsesRustResponseShape(t *testing.T) {
	got, ok := threadStartResponseSandbox("read-only").(map[string]any)
	if !ok || got["type"] != "readOnly" || got["networkAccess"] != false {
		t.Fatalf("read-only response = %#v, want Rust readOnly response object", got)
	}

	custom := map[string]any{"type": "externalSandbox", "networkAccess": "disabled"}
	if got := threadStartResponseSandbox(custom); !reflect.DeepEqual(got, custom) {
		t.Fatalf("custom sandbox response = %#v, want %#v", got, custom)
	}
}
