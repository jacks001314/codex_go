package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
)

func TestServerRequestBrokerResolvesResponse(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent <- request
	}))

	done := make(chan *authRefreshTestResponse, 1)
	go func() {
		var response authRefreshTestResponse
		if err := broker.Request(context.Background(), ServerRequestChatGPTAuthTokensRefresh, map[string]string{"reason": "unauthorized"}, &response); err != nil {
			t.Errorf("Request() error = %v", err)
			return
		}
		done <- &response
	}()

	request := <-sent
	if request.Method != ServerRequestChatGPTAuthTokensRefresh || request.ID.String() == "" {
		t.Fatalf("server request = %+v", request)
	}
	if resolved, err := broker.Resolve(OK(request.ID, authRefreshTestResponse{AccessToken: "token"})); err != nil || !resolved {
		t.Fatalf("Resolve() resolved=%v error=%v", resolved, err)
	}
	select {
	case response := <-done:
		if response.AccessToken != "token" {
			t.Fatalf("response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker response")
	}
}

func TestServerRequestBrokerRejectsPendingOnConnectionClose(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) { sent <- request }))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	var response authRefreshTestResponse
	go func() {
		done <- broker.Request(ctx, ServerRequestChatGPTAuthTokensRefresh, map[string]string{"reason": "test"}, &response)
	}()
	<-sent

	rejected := broker.RejectPending("", errors.New("server request failed: connection closed"))
	if rejected != 1 {
		t.Fatalf("RejectPending() rejected %d requests, want 1", rejected)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "connection closed") {
			t.Fatalf("Request() error = %v, want connection-closed rejection", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending request was not rejected promptly on connection close")
	}
}

func TestServerRequestBrokerIntersectsPermissionsApprovalLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific symbolic slash_tmp semantics")
	}
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) { sent <- request }))
	done := make(chan error, 1)
	var response PermissionsRequestApprovalResponse
	go func() {
		done <- broker.Request(context.Background(), ServerRequestPermissionsApproval, &PermissionsRequestApprovalParams{
			CWD: t.TempDir(),
			Permissions: map[string]any{
				"fileSystem": map[string]any{"write": []string{"/tmp"}},
			},
		}, &response)
	}()

	request := <-sent
	slashTmpGrant := map[string]any{
		"path":   map[string]any{"type": "special", "value": map[string]any{"kind": "slash_tmp"}},
		"access": "write",
	}
	if ok, err := broker.Resolve(OK(request.ID, &PermissionsRequestApprovalResponse{
		Permissions: &GrantedPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []any{slashTmpGrant}}},
		Scope:       PermissionGrantScopeTurn,
	})); err != nil || !ok {
		t.Fatalf("Resolve() ok=%v error=%v", ok, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if response.Permissions == nil || response.Permissions.FileSystem != nil || response.Permissions.Network != nil {
		t.Fatalf("intersected response = %#v", response)
	}
}

func TestServerRequestBrokerRejectsSessionScopedStrictAutoReviewLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) { sent <- request }))
	done := make(chan error, 1)
	var response PermissionsRequestApprovalResponse
	go func() {
		done <- broker.Request(context.Background(), ServerRequestPermissionsApproval, &PermissionsRequestApprovalParams{
			Permissions: map[string]any{"network": map[string]any{"enabled": true}},
		}, &response)
	}()

	request := <-sent
	enabled := true
	strict := true
	if ok, err := broker.Resolve(OK(request.ID, &PermissionsRequestApprovalResponse{
		Permissions:      &GrantedPermissionProfile{Network: &AdditionalNetworkPermissions{Enabled: &enabled}},
		Scope:            PermissionGrantScopeSession,
		StrictAutoReview: &strict,
	})); err != nil || !ok {
		t.Fatalf("Resolve() ok=%v error=%v", ok, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if response.Scope != PermissionGrantScopeTurn || response.StrictAutoReview == nil || *response.StrictAutoReview || response.Permissions == nil || response.Permissions.Network != nil {
		t.Fatalf("normalized response = %#v", response)
	}
}

func TestServerRequestBrokerPropagatesErrorResponse(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent <- request
	}))
	errs := make(chan error, 1)
	go func() {
		var response authRefreshTestResponse
		errs <- broker.Request(context.Background(), ServerRequestChatGPTAuthTokensRefresh, nil, &response)
	}()
	request := <-sent
	if resolved, err := broker.Resolve(ErrorResponse(request.ID, -32000, "nope", nil)); err != nil || !resolved {
		t.Fatalf("Resolve() resolved=%v error=%v", resolved, err)
	}
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("Request() error = nil, want error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker error")
	}
}

func TestServerRequestBrokerTargetsConnectionWhenSinkSupportsIt(t *testing.T) {
	broker := NewServerRequestBroker()
	sink := &targetedServerRequestTestSink{requests: make(chan *ServerRequest, 1), connectionIDs: make(chan string, 1)}
	broker.SetSink(sink)
	done := make(chan *authRefreshTestResponse, 1)
	go func() {
		var response authRefreshTestResponse
		if err := broker.RequestToConnection(context.Background(), "conn-a", ServerRequestChatGPTAuthTokensRefresh, nil, &response); err != nil {
			t.Errorf("RequestToConnection() error = %v", err)
			return
		}
		done <- &response
	}()

	request := <-sink.requests
	connectionID := <-sink.connectionIDs
	if connectionID != "conn-a" {
		t.Fatalf("connectionID = %q", connectionID)
	}
	if resolved, err := broker.Resolve(OK(request.ID, authRefreshTestResponse{AccessToken: "token"})); err != nil || !resolved {
		t.Fatalf("Resolve() resolved=%v error=%v", resolved, err)
	}
	select {
	case response := <-done:
		if response.AccessToken != "token" {
			t.Fatalf("response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker response")
	}
}

func TestConnectionServerRequestSinkHonorsTargetConnection(t *testing.T) {
	sent := make(chan *ServerRequest, 1)
	sink := connectionServerRequestSink{
		connectionID: "conn-a",
		send: func(request *ServerRequest) {
			sent <- request
		},
	}

	broker := NewServerRequestBroker()
	broker.SetSink(sink)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var response authRefreshTestResponse
	err := broker.RequestToConnection(ctx, "conn-b", ServerRequestChatGPTAuthTokensRefresh, nil, &response)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mismatched target error = %v, want deadline exceeded", err)
	}
	select {
	case request := <-sent:
		t.Fatalf("mismatched target sent request = %+v", request)
	default:
	}

	done := make(chan error, 1)
	go func() {
		done <- broker.RequestToConnection(context.Background(), "conn-a", ServerRequestChatGPTAuthTokensRefresh, nil, &response)
	}()
	request := <-sent
	if request.Method != ServerRequestChatGPTAuthTokensRefresh {
		t.Fatalf("request method = %s", request.Method)
	}
	if resolved, err := broker.Resolve(OK(request.ID, authRefreshTestResponse{AccessToken: "token"})); err != nil || !resolved {
		t.Fatalf("Resolve() resolved=%v error=%v", resolved, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("matched target error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matched target response")
	}
	if response.AccessToken != "token" {
		t.Fatalf("response = %+v", response)
	}

	go func() {
		done <- broker.Request(context.Background(), ServerRequestChatGPTAuthTokensRefresh, nil, &response)
	}()
	request = <-sent
	if resolved, err := broker.Resolve(OK(request.ID, authRefreshTestResponse{AccessToken: "broadcast-token"})); err != nil || !resolved {
		t.Fatalf("broadcast Resolve() resolved=%v error=%v", resolved, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("broadcast request error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast response")
	}
	if response.AccessToken != "broadcast-token" {
		t.Fatalf("broadcast response = %+v", response)
	}
}

func TestServerRequestBrokerResolvedCallback(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	resolved := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent <- request
	}))
	broker.SetResolvedCallback(func(request *ServerRequest) {
		resolved <- request
	})
	done := make(chan error, 1)
	go func() {
		var response ToolRequestUserInputResponse
		done <- broker.Request(context.Background(), ServerRequestToolUserInput, &ToolRequestUserInputParams{ThreadID: "thread-1"}, &response)
	}()

	request := <-sent
	if ok, err := broker.Resolve(OK(request.ID, &ToolRequestUserInputResponse{})); err != nil || !ok {
		t.Fatalf("Resolve() resolved=%v error=%v", ok, err)
	}
	select {
	case request := <-resolved:
		if request.Method != ServerRequestToolUserInput || serverRequestThreadID(request) != "thread-1" {
			t.Fatalf("resolved request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolved callback")
	}
	if err := <-done; err != nil {
		t.Fatalf("Request() error = %v", err)
	}
}

func TestServerRequestBrokerResolvedCallbackOnContextCancel(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 1)
	resolved := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent <- request
	}))
	broker.SetResolvedCallback(func(request *ServerRequest) {
		resolved <- request
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- broker.Request(ctx, ServerRequestToolUserInput, &ToolRequestUserInputParams{ThreadID: "thread-1"}, nil)
	}()
	request := <-sent
	cancel()

	select {
	case request := <-resolved:
		if request.ID.String() == "" || serverRequestThreadID(request) != "thread-1" {
			t.Fatalf("resolved request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolved callback")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Request() error = %v", err)
	}
	if ok, err := broker.Resolve(OK(request.ID, &ToolRequestUserInputResponse{})); err != nil || ok {
		t.Fatalf("late Resolve() resolved=%v error=%v", ok, err)
	}
}

func TestServerRequestBrokerCancelRemovesOnlyMatchingPendingRequest(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan *ServerRequest, 2)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent <- request
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- broker.Request(ctx, ServerRequestMCPElicitation, map[string]string{"name": "cancelled"}, nil)
	}()
	completed := make(chan error, 1)
	go func() {
		completed <- broker.Request(context.Background(), ServerRequestMCPElicitation, map[string]string{"name": "active"}, nil)
	}()

	requests := map[string]*ServerRequest{}
	for range 2 {
		request := <-sent
		params, ok := request.Params.(map[string]string)
		if !ok {
			t.Fatalf("request params = %#v", request.Params)
		}
		requests[params["name"]] = request
	}
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Request() error = %v", err)
	}
	if ok, err := broker.Resolve(OK(requests["cancelled"].ID, map[string]any{})); err != nil || ok {
		t.Fatalf("cancelled Resolve() resolved=%v error=%v", ok, err)
	}
	if ok, err := broker.Resolve(OK(requests["active"].ID, map[string]any{})); err != nil || !ok {
		t.Fatalf("active Resolve() resolved=%v error=%v", ok, err)
	}
	if err := <-completed; err != nil {
		t.Fatalf("active Request() error = %v", err)
	}
}

type authRefreshTestResponse struct {
	AccessToken string `json:"accessToken"`
}

type targetedServerRequestTestSink struct {
	requests      chan *ServerRequest
	connectionIDs chan string
}

func (s *targetedServerRequestTestSink) SendServerRequest(request *ServerRequest) {
	s.requests <- request
	s.connectionIDs <- ""
}

func (s *targetedServerRequestTestSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	s.requests <- request
	s.connectionIDs <- connectionID
}

func TestServerRequestMarshalShape(t *testing.T) {
	request := &ServerRequest{ID: StringID("server-request-1"), Method: ServerRequestChatGPTAuthTokensRefresh, Params: map[string]string{"reason": "unauthorized", "previousAccountId": "org-123"}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"id":"server-request-1","method":"account/chatgptAuthTokens/refresh","params":{"previousAccountId":"org-123","reason":"unauthorized"}}` {
		t.Fatalf("json = %s", data)
	}

	request = &ServerRequest{ID: StringID("server-request-2"), Method: ServerRequestChatGPTAuthTokensRefresh, Params: &auth.ChatGPTAuthTokensRefreshParams{Reason: auth.RefreshUnauthorized}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal refresh request nil previous account error = %v", err)
	}
	if string(data) != `{"id":"server-request-2","method":"account/chatgptAuthTokens/refresh","params":{"reason":"unauthorized","previousAccountId":null}}` {
		t.Fatalf("refresh nil previous account json = %s", data)
	}

	data, err = json.Marshal(&ChatGPTAuthTokensRefreshResponse{
		AccessToken:      "token",
		ChatGPTAccountID: "account",
	})
	if err != nil {
		t.Fatalf("Marshal ChatGPTAuthTokensRefreshResponse error = %v", err)
	}
	if string(data) != `{"accessToken":"token","chatgptAccountId":"account","chatgptPlanType":null}` {
		t.Fatalf("ChatGPTAuthTokensRefreshResponse json = %s", data)
	}

	request = &ServerRequest{ID: StringID("patch-approval-1"), Method: ServerRequestApplyPatchApproval, Params: &ApplyPatchApprovalParams{
		ConversationID: "thread-1",
		CallID:         "patch-1",
	}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal apply patch approval error = %v", err)
	}
	if string(data) != `{"id":"patch-approval-1","method":"applyPatchApproval","params":{"conversationId":"thread-1","callId":"patch-1","fileChanges":{},"reason":null,"grantRoot":null}}` {
		t.Fatalf("apply patch approval json = %s", data)
	}

	request = &ServerRequest{ID: IntID(9), Method: ServerRequestCurrentTimeRead, Params: &CurrentTimeReadParams{ThreadID: "thread-1"}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal current time error = %v", err)
	}
	if string(data) != `{"id":9,"method":"currentTime/read","params":{"threadId":"thread-1"}}` {
		t.Fatalf("current time json = %s", data)
	}

	timeout := uint64(60000)
	request = &ServerRequest{ID: StringID("input-1"), Method: ServerRequestToolUserInput, Params: &ToolRequestUserInputParams{
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		ItemID:           "request-user-input-turn-1",
		AutoResolutionMS: &timeout,
		Questions: []ToolRequestUserInputQuestion{{
			ID:       "choice",
			Header:   "Choice",
			Question: "Pick one?",
			Options: []ToolRequestUserInputOption{{
				ID:          "internal-a",
				Label:       "A",
				Description: "Use A",
			}},
		}},
	}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal user input request error = %v", err)
	}
	if string(data) != `{"id":"input-1","method":"item/tool/requestUserInput","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"request-user-input-turn-1","questions":[{"id":"choice","header":"Choice","question":"Pick one?","isOther":false,"isSecret":false,"options":[{"label":"A","description":"Use A"}]}],"autoResolutionMs":60000}}` {
		t.Fatalf("user input json = %s", data)
	}

	reason := "needs write access"
	grantRoot := "D:/repo"
	request = &ServerRequest{ID: StringID("approval-1"), Method: ServerRequestFileChangeApproval, Params: &FileChangeRequestApprovalParams{
		ThreadID:    "thread-1",
		TurnID:      "turn-1",
		ItemID:      "patch-1",
		StartedAtMS: 42,
		Reason:      &reason,
		GrantRoot:   &grantRoot,
	}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal file change approval error = %v", err)
	}
	if string(data) != `{"id":"approval-1","method":"item/fileChange/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"patch-1","startedAtMs":42,"reason":"needs write access","grantRoot":"D:/repo"}}` {
		t.Fatalf("file change approval json = %s", data)
	}

	request = &ServerRequest{ID: StringID("dynamic-1"), Method: ServerRequestDynamicToolCall, Params: &DynamicToolCallParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		CallID:   "call-1",
		ToolName: "legacy_tool",
		Input:    map[string]any{"city": "Paris"},
	}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal dynamic tool call error = %v", err)
	}
	if string(data) != `{"id":"dynamic-1","method":"item/tool/call","params":{"threadId":"thread-1","turnId":"turn-1","callId":"call-1","namespace":null,"tool":"legacy_tool","arguments":{"city":"Paris"}}}` {
		t.Fatalf("dynamic tool call json = %s", data)
	}

	request = &ServerRequest{ID: StringID("command-approval-1"), Method: ServerRequestCommandExecutionApproval, Params: &CommandExecutionRequestApprovalParams{
		ThreadID:                    "thread-1",
		TurnID:                      "turn-1",
		ItemID:                      "exec-1",
		StartedAtMS:                 99,
		NetworkApprovalContext:      &NetworkApprovalContext{Host: "example.test", Protocol: NetworkApprovalSocks5TCP},
		ProposedExecPolicyAmendment: map[string]any{"type": "askUser"},
		Action:                      map[string]any{"type": "internal"},
		SuggestedProfile:            stringPointerForTest("read-only"),
		SandboxDenied:               true,
		UserApprovalMessage:         stringPointerForTest("internal"),
	}}
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal command approval error = %v", err)
	}
	var commandApproval map[string]any
	if err := json.Unmarshal(data, &commandApproval); err != nil {
		t.Fatalf("Unmarshal command approval returned error: %v", err)
	}
	params, ok := commandApproval["params"].(map[string]any)
	if !ok {
		t.Fatalf("command approval params = %#v", commandApproval["params"])
	}
	if _, ok := params["proposedExecpolicyAmendment"]; !ok {
		t.Fatalf("missing Rust proposedExecpolicyAmendment: %s", data)
	}
	networkContext, ok := params["networkApprovalContext"].(map[string]any)
	if !ok || networkContext["protocol"] != "socks5_tcp" {
		t.Fatalf("network approval context should use Rust snake_case protocol: %s", data)
	}
	if _, ok := params["proposedExecPolicyAmendment"]; ok {
		t.Fatalf("non-Rust proposedExecPolicyAmendment should not be emitted: %s", data)
	}
	for _, key := range []string{"action", "suggestedProfile", "sandboxDenied", "userApprovalMessage"} {
		if _, ok := params[key]; ok {
			t.Fatalf("internal command approval field %s should not be emitted: %s", key, data)
		}
	}

	data, err = json.Marshal(&AdditionalFileSystemPermissions{})
	if err != nil {
		t.Fatalf("Marshal additional file system permissions error = %v", err)
	}
	if string(data) != `{"read":[],"write":[]}` {
		t.Fatalf("additional file system permissions json = %s", data)
	}

	data, err = json.Marshal(&GrantedPermissionProfile{})
	if err != nil {
		t.Fatalf("Marshal granted permission profile error = %v", err)
	}
	var permissions map[string]any
	if err := json.Unmarshal(data, &permissions); err != nil {
		t.Fatalf("Unmarshal permissions returned error: %v", err)
	}
	for _, key := range []string{"network", "fileSystem"} {
		if _, ok := permissions[key]; !ok || permissions[key] != nil {
			t.Fatalf("%s = %#v in %s", key, permissions[key], data)
		}
	}
}

func stringPointerForTest(value string) *string {
	return &value
}
