package codemode

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestInputAudioJSONShape(t *testing.T) {
	data, err := json.Marshal(InputAudio("data:audio/wav;base64,YXVkaW8="))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"type":"input_audio","audio_url":"data:audio/wav;base64,YXVkaW8="}` {
		t.Fatalf("audio json = %s", data)
	}
}

func TestRuntimeResponseAndWaitOutcomeJSON(t *testing.T) {
	detail := ImageDetailHigh
	response := Yielded(NewCellID("cell-1"), []ContentItem{
		InputText("hello"),
		InputImage("data:image/png;base64,abc", &detail),
	})
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"Yielded"`) || !strings.Contains(string(data), `"input_image"`) {
		t.Fatalf("response json = %s", data)
	}
	var decoded RuntimeResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, response) {
		t.Fatalf("decoded = %#v want %#v", decoded, response)
	}
	outcome := LiveCell(response)
	data, err = json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"LiveCell"`) {
		t.Fatalf("outcome json = %s", data)
	}
}

func TestProtocolIdentifiersAndHelloValidation(t *testing.T) {
	if _, ok := NewProtocolVersion(0); ok {
		t.Fatalf("zero protocol version should be invalid")
	}
	required, err := NewCapability("required")
	if err != nil {
		t.Fatal(err)
	}
	requiredSet, err := NewCapabilitySet(required)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCapabilitySet(required, required); err == nil {
		t.Fatalf("duplicate capability should fail")
	}
	versions, err := NewSupportedProtocolVersions(ProtocolV1)
	if err != nil {
		t.Fatal(err)
	}
	if !(&versions).Contains(ProtocolV1) {
		t.Fatalf("version not found")
	}
	if _, err := NewClientHello(versions, requiredSet, requiredSet); err == nil {
		t.Fatalf("overlapping capabilities should fail")
	}
}

func TestClientAndHostMessagesMarshalPinnedShapes(t *testing.T) {
	sessionID, err := NewSessionID("session-1")
	if err != nil {
		t.Fatal(err)
	}
	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, err := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ClientHelloMessage(hello))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"connection/hello"`) || !strings.Contains(string(data), `"supportedVersions":[1]`) {
		t.Fatalf("client hello = %s", data)
	}
	execRequest := ExecuteRequest{ToolCallID: "call-1", Source: "text('hello');"}
	data, err = json.Marshal(OperationRequest(1, ExecuteSessionRequest(sessionID, execRequest)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"method":"session/execute"`) || !strings.Contains(string(data), `"tool_call_id":"call-1"`) {
		t.Fatalf("execute request = %s", data)
	}
	data, err = json.Marshal(HostOperationResponse(2, ResultOK(SessionReady(sessionID))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"operation/response"`) || !strings.Contains(string(data), `"session/ready"`) {
		t.Fatalf("host response = %s", data)
	}
}

func TestNormalizeIdentifierAndMetadata(t *testing.T) {
	if got := NormalizeIdentifier("hidden-dynamic-tool"); got != "hidden_dynamic_tool" {
		t.Fatalf("identifier = %q", got)
	}
	if got := NormalizeIdentifier("1bad"); got != "_bad" {
		t.Fatalf("leading digit identifier = %q", got)
	}
	definition := ProtocolToolDefinition{Name: "tool-a", ToolName: PlainToolName("tool-a"), Description: "desc", Kind: ProtocolToolKindFunction}
	metadata := EnabledMetadata(definition)
	if metadata.GlobalName != "tool_a" || metadata.ToolName.Name != "tool-a" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestDualWebSocketHelloAndLanes(t *testing.T) {
	dual, err := NewCapability(DualWebSocketCapability)
	if err != nil {
		t.Fatal(err)
	}
	token := "tok-1"
	data, err := json.Marshal(HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{dual}, BulkConnectionToken: &token}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"bulkConnectionToken":"tok-1"`) {
		t.Fatalf("host hello with bulk token = %s", data)
	}
	var decoded HostToClient
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Hello == nil || decoded.Hello.BulkConnectionToken == nil || *decoded.Hello.BulkConnectionToken != "tok-1" {
		t.Fatalf("decoded hello = %#v", decoded.Hello)
	}

	notify := DelegateResponseMessage(1, ResultOK(NotificationDeliveredResponse()))
	toolResult := DelegateResponseMessage(2, ResultOK(ToolResultResponse(json.RawMessage(`{"ok":true}`))))
	if notify.transportLane() != TransportLaneControl || toolResult.transportLane() != TransportLaneBulk || OperationRequest(1, WaitSessionRequest("s", WaitRequest{CellID: "c"})).transportLane() != TransportLaneControl {
		t.Fatalf("client lanes: notify=%s result=%s request=%s", notify.transportLane(), toolResult.transportLane(), OperationRequest(1, WaitSessionRequest("s", WaitRequest{CellID: "c"})).transportLane())
	}
	invoke := HostToClient{Type: "delegate/request", DelegateID: 3, Request: &DelegateRequest{Type: "tool/invoke"}}
	cancel := HostToClient{Type: "delegate/cancel", DelegateID: 4}
	hostNotify := HostToClient{Type: "delegate/request", DelegateID: 5, Request: &DelegateRequest{Type: "notification/send"}}
	if invoke.transportLane() != TransportLaneBulk || cancel.transportLane() != TransportLaneBulk || hostNotify.transportLane() != TransportLaneControl {
		t.Fatalf("host lanes: invoke=%s cancel=%s notify=%s", invoke.transportLane(), cancel.transportLane(), hostNotify.transportLane())
	}
}

func TestSessionRuntimeExecuteWaitTerminateAndHost(t *testing.T) {
	runtime := NewSessionRuntime()
	ctx := context.Background()
	started, err := runtime.Execute(ctx, &ExecuteRequest{
		ToolCallID:  "call-1",
		Source:      "text('hello')",
		YieldTimeMS: uint64Ptr(100),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if started.CellID.String() != "1" || started.InitialResponse.Variant != "Result" {
		t.Fatalf("started = %#v", started)
	}

	yielded, err := runtime.Execute(ctx, &ExecuteRequest{
		ToolCallID:  "call-2",
		Source:      "// @exec: yield\nawait text('later')",
		YieldTimeMS: uint64Ptr(0),
	})
	if err != nil {
		t.Fatalf("Execute(yielded) error = %v", err)
	}
	if yielded.InitialResponse.Variant != "Yielded" {
		t.Fatalf("yielded = %#v", yielded.InitialResponse)
	}
	wait, err := runtime.Wait(ctx, &WaitRequest{CellID: yielded.CellID, YieldTimeMS: 1000})
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if wait.Response.Variant != "Result" || !strings.Contains(wait.Response.ContentItems[0].Text, "later") {
		t.Fatalf("wait = %#v", wait)
	}

	terminated, err := runtime.Terminate(NewCellID("missing"))
	if err != nil {
		t.Fatalf("Terminate(missing) error = %v", err)
	}
	if terminated.Variant != "MissingCell" {
		t.Fatalf("terminated = %#v", terminated)
	}

	sessionID, _ := NewSessionID("session-1")
	host := NewSessionHost(runtime)
	response, initial, err := host.Handle(ctx, &HostRequest{Method: "session/execute", SessionID: sessionID, Request: &ExecuteRequest{ToolCallID: "call-3", Source: "text('host')"}})
	if err != nil {
		t.Fatalf("Handle(execute) error = %v", err)
	}
	if response.Type != "execution/started" || initial == nil || initial.Variant != "Result" {
		t.Fatalf("response=%#v initial=%#v", response, initial)
	}
}

func TestCellExecutionLimitsClampAndWireLikeRust(t *testing.T) {
	maxYield := uint64(2500)
	limits := &CellExecutionLimits{MaxYieldTimeMS: &maxYield}
	if limits.IsZero() {
		t.Fatal("limits with max_yield_time_ms reported zero")
	}
	if (&CellExecutionLimits{}).IsZero() == false {
		t.Fatal("empty limits must be zero")
	}
	for _, tc := range []struct {
		name   string
		yield  uint64
		want   uint64
		limits *CellExecutionLimits
	}{
		{name: "nil limits pass through", yield: 10000, want: 10000, limits: nil},
		{name: "above limit clamps", yield: 30000, want: 2500, limits: limits},
		{name: "below limit passes", yield: 1000, want: 1000, limits: limits},
		{name: "equal limit passes", yield: 2500, want: 2500, limits: limits},
		{name: "zero stays zero", yield: 0, want: 0, limits: limits},
		{name: "empty limits pass through", yield: 5000, want: 5000, limits: &CellExecutionLimits{}},
	} {
		if got := tc.limits.ClampYieldTimeMS(tc.yield); got != tc.want {
			t.Fatalf("%s: ClampYieldTimeMS(%d) = %d, want %d", tc.name, tc.yield, got, tc.want)
		}
	}

	// session/open carries limits only when non-default.
	sessionID, _ := NewSessionID("session-limits")
	withLimits := OpenSessionRequestWithLimits(sessionID, limits)
	payload, err := json.Marshal(withLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"cellExecutionLimits":{"max_yield_time_ms":2500}`) {
		t.Fatalf("open-with-limits payload = %s", payload)
	}
	var decoded HostRequest
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CellExecutionLimits == nil || decoded.CellExecutionLimits.MaxYieldTimeMS == nil || *decoded.CellExecutionLimits.MaxYieldTimeMS != 2500 {
		t.Fatalf("decoded limits = %#v", decoded.CellExecutionLimits)
	}

	unlimited := OpenSessionRequestWithLimits(sessionID, &CellExecutionLimits{})
	payload, err = json.Marshal(unlimited)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "cellExecutionLimits") {
		t.Fatalf("unlimited open payload must omit limits: %s", payload)
	}
}

func TestSessionHostClampsYieldTimeToSessionLimitsLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	host := NewSessionHost(runtime)
	ctx := context.Background()
	sessionID, _ := NewSessionID("session-limited")
	maxYield := uint64(10)
	open := OpenSessionRequestWithLimits(sessionID, &CellExecutionLimits{MaxYieldTimeMS: &maxYield})
	if _, _, err := host.Handle(ctx, &open); err != nil {
		t.Fatalf("Handle(open) error = %v", err)
	}
	request := ExecuteSessionRequest(sessionID, ExecuteRequest{
		ToolCallID:  "call-limited",
		Source:      "text('clamped')",
		YieldTimeMS: uint64Ptr(10000),
	})
	// The clamped request must still execute; the yield value itself is
	// capped at max_yield_time_ms by the host before reaching the runtime.
	response, initial, err := host.Handle(ctx, &request)
	if err != nil {
		t.Fatalf("Handle(execute with limits) error = %v", err)
	}
	if response == nil || response.Type != "execution/started" || initial == nil {
		t.Fatalf("response=%#v initial=%#v", response, initial)
	}
}

func TestFramedCodecRoundTripsAndRejectsOversizedFrames(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFramedWriter(&buf)
	value := map[string]any{"value": float64(1)}
	if err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"value":1}`)
	expected := make([]byte, 4)
	binary.LittleEndian.PutUint32(expected, uint32(len(payload)))
	expected = append(expected, payload...)
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Fatalf("frame bytes = %v want %v", buf.Bytes(), expected)
	}
	var decoded map[string]any
	ok, err := NewFramedReader(bytes.NewReader(buf.Bytes())).Read(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decoded["value"] != float64(1) {
		t.Fatalf("decoded = %v %v", ok, decoded)
	}
	var oversized bytes.Buffer
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(ProtocolMaxFrameBytes+1))
	oversized.Write(header)
	ok, err = NewFramedReader(&oversized).Read(&decoded)
	if err == nil || ok {
		t.Fatalf("expected oversized error, got ok=%v err=%v", ok, err)
	}
}

func uint64Ptr(value uint64) *uint64 {
	return &value
}

func TestExecuteRequestTraceContextRoundTripsLikeRust(t *testing.T) {
	req := ExecuteRequest{
		ToolCallID:   "call-trace",
		Source:       "text('hi')",
		TraceContext: &CodeModeTraceContext{Traceparent: "00-abcdef1234567890abcdef1234567890-1234567890abcdef-01"},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal execute request: %v", err)
	}
	var decoded ExecuteRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal execute request: %v", err)
	}
	if decoded.TraceContext == nil || decoded.TraceContext.Traceparent != "00-abcdef1234567890abcdef1234567890-1234567890abcdef-01" {
		t.Fatalf("decoded TraceContext = %#v", decoded.TraceContext)
	}
}
