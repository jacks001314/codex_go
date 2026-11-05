package cloudclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"google.golang.org/protobuf/encoding/protowire"
)

func appendActivationString(out []byte, number protowire.Number, value string) []byte {
	out = protowire.AppendTag(out, number, protowire.BytesType)
	return protowire.AppendString(out, value)
}

func appendActivationBytes(out []byte, number protowire.Number, value []byte) []byte {
	out = protowire.AppendTag(out, number, protowire.BytesType)
	return protowire.AppendBytes(out, value)
}

func appendActivationVarint(out []byte, number protowire.Number, value uint64) []byte {
	out = protowire.AppendTag(out, number, protowire.VarintType)
	return protowire.AppendVarint(out, value)
}

func appendActivationStringList(out []byte, number protowire.Number, values []string) []byte {
	var list []byte
	for _, value := range values {
		list = appendActivationString(list, 1, value)
	}
	return appendActivationBytes(out, number, list)
}

func activationTestThread(id string) []byte {
	var thread []byte
	thread = appendActivationString(thread, 4, "1.0.0")
	thread = appendActivationVarint(thread, 5, 100)
	thread = appendActivationString(thread, 6, "/workspace")
	thread = appendActivationVarint(thread, 9, 0)
	thread = appendActivationVarint(thread, 13, 1)
	thread = appendActivationString(thread, 14, id)
	thread = appendActivationString(thread, 16, "openai")
	thread = appendActivationString(thread, 21, "preview")
	thread = appendActivationString(thread, 27, "session-1")
	var source []byte
	source = appendActivationVarint(source, 1, 1)
	thread = appendActivationBytes(thread, 28, source)
	var status []byte
	status = appendActivationBytes(status, 2, nil)
	thread = appendActivationBytes(thread, 29, status)
	thread = appendActivationVarint(thread, 32, 200)
	return thread
}

func activationTestResponse(threadID string) []byte {
	var response []byte
	var profile []byte
	profile = appendActivationString(profile, 1, "base")
	profile = appendActivationString(profile, 2, "profile-1")
	response = appendActivationBytes(response, 1, profile)
	var approval []byte
	approval = appendActivationVarint(approval, 1, 2)
	response = appendActivationBytes(response, 2, approval)
	response = appendActivationVarint(response, 3, 1)
	var collaboration []byte
	collaboration = appendActivationVarint(collaboration, 1, 2)
	var settings []byte
	settings = appendActivationString(settings, 2, "gpt-planning")
	settings = appendActivationString(settings, 3, "high")
	collaboration = appendActivationBytes(collaboration, 2, settings)
	response = appendActivationBytes(response, 4, collaboration)
	response = appendActivationString(response, 5, "/workspace")
	response = appendActivationStringList(response, 8, []string{"AGENTS.md"})
	response = appendActivationString(response, 9, "items-cursor")
	response = appendActivationString(response, 10, "gpt-5")
	response = appendActivationString(response, 11, "openai")
	response = appendActivationString(response, 13, "medium")
	response = appendActivationStringList(response, 14, []string{"/workspace"})
	var sandbox []byte
	sandbox = appendActivationBytes(sandbox, 1, nil)
	response = appendActivationBytes(response, 15, sandbox)
	var serviceTier []byte
	serviceTier = appendActivationString(serviceTier, 1, "priority")
	response = appendActivationBytes(response, 16, serviceTier)
	response = appendActivationBytes(response, 17, activationTestThread(threadID))
	response = appendActivationString(response, 18, "turns-cursor")
	return response
}

func TestUpdateConfigActivationRequestEncodesThreadAndExcludesTurnsLikeRust(t *testing.T) {
	fields, err := parseWireFields(encodeUpdateConfigRequest("thread-1"))
	if err != nil {
		t.Fatalf("parseWireFields() error = %v", err)
	}
	exclude, ok := firstField(fields, 7)
	if !ok || exclude.typ != protowire.VarintType || exclude.value != 1 {
		t.Fatalf("exclude_turns = %#v", exclude)
	}
	thread, ok := firstField(fields, 18)
	if !ok || string(thread.bytes) != "thread-1" {
		t.Fatalf("thread_id = %#v", thread)
	}
}

func TestDecodeThreadActivationResolvesSettingsAndMetadataLikeRust(t *testing.T) {
	payload := activationTestResponse("thread-1")
	activation, err := decodeThreadActivation(payload, "thread-1")
	if err != nil {
		t.Fatalf("decodeThreadActivation() error = %v", err)
	}
	if activation.Thread.ID != "thread-1" || activation.Thread.SessionID != "session-1" || activation.Thread.HistoryMode != "legacy" || activation.Thread.Source != "cli" {
		t.Fatalf("thread = %#v", activation.Thread)
	}
	if activation.Model != "gpt-5" || activation.ModelProvider != "openai" || activation.CWD != "/workspace" || activation.ApprovalsReviewer != "user" || activation.ApprovalPolicy != "on-request" {
		t.Fatalf("settings = %#v", activation)
	}
	if activation.ServiceTier == nil || *activation.ServiceTier != "priority" || activation.ReasoningEffort == nil || *activation.ReasoningEffort != "medium" {
		t.Fatalf("optional settings = %#v", activation)
	}
	if activation.CollaborationMode == nil || activation.CollaborationMode.Mode != "default" || activation.CollaborationMode.Settings.Model != "gpt-planning" {
		t.Fatalf("collaboration = %#v", activation.CollaborationMode)
	}
	if activation.ActivePermissionProfile == nil || activation.ActivePermissionProfile.ID != "profile-1" {
		t.Fatalf("profile = %#v", activation.ActivePermissionProfile)
	}
	if activation.TurnsBackwardsCursor == nil || *activation.TurnsBackwardsCursor != "turns-cursor" || activation.ItemsBackwardsCursor == nil || *activation.ItemsBackwardsCursor != "items-cursor" {
		t.Fatalf("cursors = %#v/%#v", activation.TurnsBackwardsCursor, activation.ItemsBackwardsCursor)
	}
	if string(activation.Thread.Proto()) != string(activationTestThread("thread-1")) {
		t.Fatal("raw thread protobuf was not preserved")
	}
	if len(activation.Thread.Turns) != 0 {
		t.Fatalf("activation hydrated turns: %#v", activation.Thread.Turns)
	}
	data, err := json.Marshal(activation)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "rawProtobuf") || !strings.Contains(string(data), `"turns":[]`) {
		t.Fatalf("activation JSON = %s", data)
	}
}

func TestDecodeThreadActivationRejectsMismatchedOrIncompleteResponsesLikeRust(t *testing.T) {
	if _, err := decodeThreadActivation(activationTestResponse("thread-1"), "thread-2"); err == nil {
		t.Fatal("mismatched requested thread was accepted")
	}
	payload := activationTestResponse("thread-1")
	fields, err := parseWireFields(payload)
	if err != nil {
		t.Fatalf("parseWireFields() error = %v", err)
	}
	var incomplete []byte
	for _, field := range fields {
		if field.number == 10 {
			continue
		}
		switch field.typ {
		case protowire.BytesType:
			incomplete = appendActivationBytes(incomplete, field.number, field.bytes)
		case protowire.VarintType:
			incomplete = appendActivationVarint(incomplete, field.number, field.value)
		}
	}
	if _, err := decodeThreadActivation(incomplete, "thread-1"); err == nil {
		t.Fatal("missing model was accepted")
	}
}

func TestDecodeThreadActivationSerializesGranularAndWorkspacePoliciesLikeRust(t *testing.T) {
	thread := activationTestThread("thread-1")
	thread = appendActivationBytes(thread, 8, nil)

	var granular []byte
	granular = appendActivationVarint(granular, 1, 1)
	granular = appendActivationVarint(granular, 2, 1)
	granular = appendActivationVarint(granular, 3, 0)
	granular = appendActivationVarint(granular, 4, 1)
	var wrapper []byte
	wrapper = appendActivationBytes(wrapper, 1, granular)
	var approval []byte
	approval = appendActivationBytes(approval, 2, wrapper)

	var workspace []byte
	workspace = appendActivationVarint(workspace, 1, 1)
	workspace = appendActivationVarint(workspace, 2, 0)
	workspace = appendActivationVarint(workspace, 3, 1)
	workspace = appendActivationStringList(workspace, 4, []string{"/workspace"})
	var sandbox []byte
	sandbox = appendActivationBytes(sandbox, 4, workspace)

	var response []byte
	response = appendActivationBytes(response, 2, approval)
	response = appendActivationVarint(response, 3, 2)
	response = appendActivationString(response, 5, "/workspace")
	response = appendActivationString(response, 10, "gpt-5")
	response = appendActivationString(response, 11, "openai")
	response = appendActivationBytes(response, 15, sandbox)
	response = appendActivationBytes(response, 17, thread)

	activation, err := decodeThreadActivation(response, "thread-1")
	if err != nil {
		t.Fatalf("decodeThreadActivation() error = %v", err)
	}
	if activation.Thread.Environments == nil || len(*activation.Thread.Environments) != 0 {
		t.Fatalf("environments = %#v, want an explicit empty list", activation.Thread.Environments)
	}
	if activation.ApprovalsReviewer != "auto_review" {
		t.Fatalf("reviewer = %q", activation.ApprovalsReviewer)
	}
	data, err := json.Marshal(activation)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	granularJSON := value["approvalPolicy"].(map[string]any)["granular"].(map[string]any)
	if granularJSON["sandbox_approval"] != true || granularJSON["rules"] != false ||
		granularJSON["skill_approval"] != false || granularJSON["request_permissions"] != true ||
		granularJSON["mcp_elicitations"] != true {
		t.Fatalf("granular approval = %#v", granularJSON)
	}
	sandboxJSON := value["sandbox"].(map[string]any)
	if sandboxJSON["type"] != "workspaceWrite" || sandboxJSON["networkAccess"] != true ||
		sandboxJSON["excludeSlashTmp"] != true || sandboxJSON["excludeTmpdirEnvVar"] != false {
		t.Fatalf("workspace sandbox = %#v", sandboxJSON)
	}
	environmentsJSON, ok := value["thread"].(map[string]any)["environments"].([]any)
	if !ok || len(environmentsJSON) != 0 {
		t.Fatalf("environments JSON = %#v", value["thread"].(map[string]any)["environments"])
	}
}

func TestClientActivateAndPrewarmUseNativeMethodsLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	activationResponse := activationTestResponse("thread-1")
	prewarmResponse := prewarmTestResponse(true)
	var prewarmCalls int
	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "oaiproto.codex_app_server_backend.ThreadService",
		HandlerType: (*testThreadService)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "UpdateConfig",
				Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					var message rawMessage
					if err := decode(&message); err != nil {
						return nil, err
					}
					if string(message) != string(encodeUpdateConfigRequest("thread-1")) {
						t.Fatalf("UpdateConfig request = %x", []byte(message))
					}
					return rawMessage(activationResponse), nil
				},
			},
			{
				MethodName: "Prewarm",
				Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					var message rawMessage
					if err := decode(&message); err != nil {
						return nil, err
					}
					if string(message) != string(encodePrewarmRequest("gpt-5", "openai")) {
						t.Fatalf("Prewarm request = %x", []byte(message))
					}
					prewarmCalls++
					return rawMessage(prewarmResponse), nil
				},
			},
		},
	}, &struct{}{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := New("http://"+listener.Addr().String(), Credentials{BearerToken: "token", AccountID: "account"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Activate(ctx, "   "); err == nil || err.(*MutationError).Kind != MutationInvalidRequest {
		t.Fatalf("blank activation error = %v, want local invalid request", err)
	}
	activation, err := client.Activate(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if activation.Thread.ID != "thread-1" || activation.Model != "gpt-5" {
		t.Fatalf("activation = %#v", activation)
	}
	warm, err := client.PrewarmDefaultThread(ctx, "gpt-5", "openai")
	if err != nil {
		t.Fatalf("PrewarmDefaultThread() error = %v", err)
	}
	if prewarmCalls != 1 || warm.Thread.ID != "thread-1" || warm.ServiceTier == nil || *warm.ServiceTier != "fast" {
		t.Fatalf("prewarm calls=%d activation=%#v", prewarmCalls, warm)
	}
}

func TestClientActivationRejectsEmptyStoredIdentityLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		sessionID  string
		providerID string
	}{
		{name: "empty session", sessionID: "", providerID: "openai"},
		{name: "empty provider", sessionID: "session-1", providerID: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			thread := activationTestThread("thread-1")
			thread = appendActivationString(thread, 27, testCase.sessionID)
			thread = appendActivationString(thread, 16, testCase.providerID)
			response := activationTestResponse("thread-1")
			response = appendActivationBytes(response, 17, thread)
			if _, err := decodeThreadActivation(response, "thread-1"); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("decodeThreadActivation() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}
