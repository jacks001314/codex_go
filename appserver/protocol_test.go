package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/session"
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

// Rust parity: app-server/src/message_processor.rs reject_removed_permission_profile
// (#38919). The obsolete `permissionProfile` field is rejected with
// invalid_params (-32602) on thread/start, thread/resume, thread/fork and
// turn/start; unrelated unknown fields stay accepted for forward compatibility.
func TestRejectObsoletePermissionProfileLikeRust(t *testing.T) {
	methods := []string{"thread/start", "thread/resume", "thread/fork", "turn/start"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			raw := fmt.Sprintf(`{"id":1,"method":%q,"params":{"permissionProfile":"workspace"}}`, method)
			_, err := ParseRequest([]byte(raw))
			if err == nil {
				t.Fatalf("ParseRequest(%s) succeeded, want obsolete permissionProfile rejection", raw)
			}
			if !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("ParseRequest(%s) error = %v, want ErrInvalidParams", raw, err)
			}
			if code := requestValidationErrorCode(err); code != JSONRPCInvalidParamsErrorCode {
				t.Fatalf("requestValidationErrorCode() = %d, want %d", code, JSONRPCInvalidParamsErrorCode)
			}
			wantMessage := fmt.Sprintf("`permissionProfile` is no longer supported for `%s`; use `permissions` with a named profile id instead", method)
			if err.Error() != wantMessage {
				t.Fatalf("error message = %q, want %q", err.Error(), wantMessage)
			}
		})
	}

	// Unknown fields are still accepted when combined with named permissions.
	request, err := ParseRequest([]byte(`{"id":2,"method":"thread/start","params":{"permissions":{"id":"workspace"},"futureField":42}}`))
	if err != nil {
		t.Fatalf("thread/start with unknown field rejected: %v", err)
	}
	if request == nil {
		t.Fatal("ParseRequest returned nil request")
	}

	// Methods not in the obsolete set still accept permissionProfile
	// (command/exec legitimately carries permissionProfile).
	if _, err := ParseRequest([]byte(`{"id":3,"method":"command/exec","params":{"permissionProfile":"workspace","command":["echo","hi"]}}`)); err != nil {
		t.Fatalf("command/exec permissionProfile rejected: %v", err)
	}

	// The connection remains usable: a valid request parses and is dispatched
	// (only the missing store surfaces, not a validation error).
	response := (&Router{}).Handle(&Request{
		ID:     IntID(4),
		Method: MethodThreadList,
		Params: json.RawMessage(`{}`),
	})
	if response == nil || response.Error == nil || response.Error.Code != JSONRPCInternalErrorCode {
		t.Fatalf("post-rejection connection usability response = %+v, want -32603 store error", response)
	}
}

func TestThreadSectionProtocolParity(t *testing.T) {
	request, err := ParseRequest([]byte(`{"id":1,"method":"thread/section/move","params":{"threadId":"thread-1","sectionId":null}}`))
	if err != nil {
		t.Fatalf("ParseRequest error = %v", err)
	}
	var params ThreadSectionMoveParams
	if err := request.DecodeParams(&params); err != nil {
		t.Fatalf("DecodeParams error = %v", err)
	}
	if !params.SectionID.Set || params.SectionID.Value != nil {
		t.Fatalf("sectionId = %#v, want explicit null", params.SectionID)
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	pinnedID := session.PinnedThreadSectionID
	options, err := BuildListOptions(&ThreadListParams{
		SectionID: OptionalString{Set: true, Value: &pinnedID},
		SortKey:   SortSectionPosition,
	})
	if err != nil || !options.SectionSet || options.SectionID == nil || *options.SectionID != pinnedID || options.SortKey != session.SortSectionPosition || options.SortDirection != session.SortAsc {
		t.Fatalf("BuildListOptions() = %#v, %v", options, err)
	}
	enteredAt := int64(123)
	data, err := json.Marshal(&Thread{ID: "thread-1", Section: &ThreadSection{ID: pinnedID, Name: session.PinnedThreadSectionName}, SectionEnteredAt: &enteredAt})
	if err != nil {
		t.Fatalf("Marshal(Thread) error = %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("thread JSON = %s, %v", data, err)
	}
	section, _ := encoded["section"].(map[string]any)
	if section["id"] != pinnedID {
		t.Fatalf("thread JSON = %s, %v", data, err)
	}
	if encoded["sectionEnteredAt"] != float64(enteredAt) {
		t.Fatalf("thread JSON sectionEnteredAt = %#v, want %d", encoded["sectionEnteredAt"], enteredAt)
	}
	if _, ok := encoded["isPinned"]; ok {
		t.Fatalf("thread JSON retained removed isPinned field: %s", data)
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

func TestThreadSectionUpdateParamsAppearanceDoubleOption(t *testing.T) {
	// Omitted appearance -> preserved (AppearanceSet false).
	var omitted ThreadSectionUpdateParams
	if err := json.Unmarshal([]byte(`{"sectionId":"s1","name":"Work"}`), &omitted); err != nil {
		t.Fatalf("Unmarshal(omit) error = %v", err)
	}
	if omitted.AppearanceSet || omitted.Appearance != nil {
		t.Fatalf("omitted appearance = set:%v %#v", omitted.AppearanceSet, omitted.Appearance)
	}
	// Null appearance -> clear (AppearanceSet true, Appearance nil).
	var cleared ThreadSectionUpdateParams
	if err := json.Unmarshal([]byte(`{"sectionId":"s1","name":"Work","appearance":null}`), &cleared); err != nil {
		t.Fatalf("Unmarshal(null) error = %v", err)
	}
	if !cleared.AppearanceSet || cleared.Appearance != nil {
		t.Fatalf("null appearance = set:%v %#v", cleared.AppearanceSet, cleared.Appearance)
	}
	// Value appearance -> replace.
	icon := "work"
	var replaced ThreadSectionUpdateParams
	if err := json.Unmarshal([]byte(`{"sectionId":"s1","name":"Work","appearance":{"icon":"work","color":"#ff0000"}}`), &replaced); err != nil {
		t.Fatalf("Unmarshal(value) error = %v", err)
	}
	if !replaced.AppearanceSet || replaced.Appearance == nil || replaced.Appearance.Icon == nil || *replaced.Appearance.Icon != icon {
		t.Fatalf("value appearance = set:%v %#v", replaced.AppearanceSet, replaced.Appearance)
	}
	// 64-byte validation.
	long := strings.Repeat("x", 65)
	if err := (&ThreadSectionUpdateParams{SectionID: "s1", Name: "Work", Appearance: &ThreadSectionAppearance{Icon: &long}, AppearanceSet: true}).Validate(); err == nil {
		t.Fatal("expected 64-byte icon rejection")
	}
	if err := (&ThreadSectionCreateParams{Name: "Work", Appearance: &ThreadSectionAppearance{Color: &long}}).Validate(); err == nil {
		t.Fatal("expected 64-byte color rejection")
	}
}

func TestThreadOriginatorSurfaceAndListFilter(t *testing.T) {
	record := &session.Record{
		ID:        "thread-originator",
		SessionID: "thread-originator",
		Metadata:  session.Metadata{CWD: t.TempDir(), ModelProvider: "openai", Originator: "codex_vscode", HistoryMode: "legacy"},
	}
	thread := BuildThread(record, "", false)
	if thread == nil || thread.Originator == nil || *thread.Originator != "codex_vscode" {
		t.Fatalf("thread originator = %#v", thread)
	}
	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshal thread: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("unmarshal thread: %v", err)
	}
	if encoded["originator"] != "codex_vscode" {
		t.Fatalf("thread JSON originator = %#v", encoded["originator"])
	}

	absent := BuildThread(&session.Record{ID: "thread-no-originator", SessionID: "thread-no-originator", Metadata: session.Metadata{CWD: t.TempDir(), ModelProvider: "openai"}}, "", false)
	if absent == nil || absent.Originator != nil {
		t.Fatalf("absent originator = %#v, want nil", absent)
	}
	absentData, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal absent originator thread: %v", err)
	}
	var absentEncoded map[string]any
	if err := json.Unmarshal(absentData, &absentEncoded); err != nil {
		t.Fatalf("unmarshal absent originator thread: %v", err)
	}
	if value, present := absentEncoded["originator"]; !present || value != nil {
		t.Fatalf("absent thread JSON originator = %#v, present=%v", value, present)
	}

	if err := (&ThreadListParams{}).Validate(); err != nil {
		t.Fatalf("empty originators should validate: %v", err)
	}
	if err := (&ThreadListParams{Originators: []string{"codex_vscode"}}).Validate(); err == nil || err.Error() != "originator filtering is not supported by the local app-server" {
		t.Fatalf("nonempty originators error = %v", err)
	}
}

func TestThreadModelAndReasoningEffortSurface(t *testing.T) {
	record := &session.Record{
		ID:        "thread-model",
		SessionID: "thread-model",
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			Model:         "gpt-test",
			ModelProvider: "openai",
			Extra:         map[string]any{"config": map[string]any{"model_reasoning_effort": "high"}},
		},
	}
	thread := BuildThread(record, "", false)
	if thread == nil || thread.Model == nil || *thread.Model != "gpt-test" {
		t.Fatalf("thread model = %#v", thread)
	}
	if thread.ReasoningEffort == nil || *thread.ReasoningEffort != "high" {
		t.Fatalf("thread reasoningEffort = %#v", thread)
	}
	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshal thread: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("unmarshal thread: %v", err)
	}
	if encoded["model"] != "gpt-test" || encoded["reasoningEffort"] != "high" {
		t.Fatalf("thread JSON model/reasoningEffort = %s", data)
	}

	absent := BuildThread(&session.Record{ID: "thread-no-model", SessionID: "thread-no-model", Metadata: session.Metadata{CWD: t.TempDir(), ModelProvider: "openai"}}, "", false)
	if absent == nil || absent.Model != nil || absent.ReasoningEffort != nil {
		t.Fatalf("absent model/reasoningEffort should be nil: %#v", absent)
	}
	absentData, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal absent thread: %v", err)
	}
	var absentEncoded map[string]any
	if err := json.Unmarshal(absentData, &absentEncoded); err != nil {
		t.Fatalf("unmarshal absent thread: %v", err)
	}
	if absentEncoded["model"] != nil || absentEncoded["reasoningEffort"] != nil {
		t.Fatalf("absent thread JSON model/reasoningEffort = %s", absentData)
	}
}

func TestThreadEnvironmentsFromRecord(t *testing.T) {
	record := &session.Record{
		ID:        "thread-env",
		SessionID: "thread-env",
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			ModelProvider: "openai",
			Extra: map[string]any{
				runtimeEnvironmentSelectionsExtraKey: []map[string]any{
					{"environment_id": "primary", "cwd": "/primary", "workspace_roots": []string{"/primary", "/shared"}},
				},
			},
		},
	}
	thread := BuildThread(record, "", false)
	if thread == nil || len(thread.Environments) != 1 {
		t.Fatalf("thread environments = %#v", thread)
	}
	env := thread.Environments[0]
	if env.EnvironmentID != "primary" || env.CWD != "/primary" || len(env.RuntimeWorkspaceRoots) != 2 || env.RuntimeWorkspaceRoots[1] != "/shared" {
		t.Fatalf("thread environment = %#v", env)
	}
	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshal thread: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("unmarshal thread: %v", err)
	}
	environments, ok := encoded["environments"].([]any)
	if !ok || len(environments) != 1 {
		t.Fatalf("thread JSON environments = %s", data)
	}
	first := environments[0].(map[string]any)
	if first["environmentId"] != "primary" || first["cwd"] != "/primary" {
		t.Fatalf("thread JSON first environment = %#v", first)
	}

	absent := BuildThread(&session.Record{ID: "thread-no-env", SessionID: "thread-no-env", Metadata: session.Metadata{CWD: t.TempDir(), ModelProvider: "openai"}}, "", false)
	if absent == nil || absent.Environments != nil {
		t.Fatalf("absent thread environments = %#v, want nil", absent)
	}
	absentData, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal absent thread: %v", err)
	}
	var absentEncoded map[string]any
	if err := json.Unmarshal(absentData, &absentEncoded); err != nil {
		t.Fatalf("unmarshal absent thread: %v", err)
	}
	if value, present := absentEncoded["environments"]; !present || value != nil {
		t.Fatalf("absent thread JSON environments = %#v, present=%v", value, present)
	}
}

func TestThreadDaybreakEnabledSurfaceAndPatch(t *testing.T) {
	enabled := true
	record := &session.Record{
		ID:        "thread-daybreak",
		SessionID: "thread-daybreak",
		Metadata:  session.Metadata{CWD: t.TempDir(), ModelProvider: "openai", DaybreakEnabled: &enabled},
	}
	thread := BuildThread(record, "", false)
	if thread == nil || thread.DaybreakEnabled == nil || !*thread.DaybreakEnabled {
		t.Fatalf("thread daybreakEnabled = %#v", thread)
	}
	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshal thread: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("unmarshal thread: %v", err)
	}
	if encoded["daybreakEnabled"] != true {
		t.Fatalf("thread JSON daybreakEnabled = %s", data)
	}

	disabled := false
	patch, err := MetadataPatchToSession(&ThreadMetadataUpdateParams{ThreadID: "thread-daybreak", DaybreakEnabled: &disabled})
	if err != nil {
		t.Fatalf("MetadataPatchToSession: %v", err)
	}
	if patch.DaybreakEnabled == nil || *patch.DaybreakEnabled {
		t.Fatalf("daybreak patch = %#v", patch.DaybreakEnabled)
	}
}
