package provisionservice

import (
	"bytes"
	"strings"
	"testing"

	json "github.com/goccy/go-json"
)

func TestFrameWritesRustProvisioningRequestShape(t *testing.T) {
	frame := &Frame{Version: ProtocolVersion, Message: Message{
		Kind: KindProvisionSandboxRequest,
		Request: &ProvisionRequest{
			CodexHome:      `C:\home`,
			RegisteredCore: true,
			Settings:       Settings{ProxyPorts: []uint16{7897}},
			Listeners:      Listeners{HTTPPorts: []uint16{7897}},
		},
	}}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, frame); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	decoded, err := ReadFrame(&buffer)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if decoded.Version != ProtocolVersion || decoded.Message.Kind != KindProvisionSandboxRequest {
		t.Fatalf("decoded frame = %#v", decoded)
	}
	request := decoded.Message.Request
	if request == nil {
		t.Fatal("request payload missing")
	}
	if request.CodexHome != `C:\home` || !request.RegisteredCore || request.RefreshOnly {
		t.Fatalf("request = %#v", request)
	}
	if len(request.Settings.ProxyPorts) != 1 || request.Settings.ProxyPorts[0] != 7897 {
		t.Fatalf("settings = %#v", request.Settings)
	}
	if len(request.Listeners.HTTPPorts) != 1 || request.Listeners.HTTPPorts[0] != 7897 {
		t.Fatalf("listeners = %#v", request.Listeners)
	}
}

func TestFrameRequestJSONUsesSnakeCaseAndEmptySlices(t *testing.T) {
	frame := Frame{Version: ProtocolVersion, Message: Message{
		Kind:    KindProvisionSandboxRequest,
		Request: &ProvisionRequest{CodexHome: "home"},
	}}
	payload, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(payload)
	for _, want := range []string{
		`"version":1`,
		`"type":"provision_sandbox_request"`,
		`"codex_home":"home"`,
		`"settings":{"proxy_ports":[],"allow_local_binding":false}`,
		`"listeners":{"http_ports":[],"socks_ports":[]}`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload %s missing %s", text, want)
		}
	}
	if strings.Contains(text, "registered_core") || strings.Contains(text, "refresh_only") {
		t.Fatalf("false routing flags must be omitted: %s", text)
	}
}

func TestFrameRegisterInstallationKeepsTopLevelCodexHome(t *testing.T) {
	frame := Frame{Version: ProtocolVersion, Message: Message{
		Kind:      KindRegisterInstallationRequest,
		CodexHome: `C:\home`,
	}}
	payload, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"version":1,"type":"register_installation_request","codex_home":"C:\\home"}`
	if string(payload) != want {
		t.Fatalf("payload = %s, want %s", payload, want)
	}
	var decoded Frame
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Message.CodexHome != `C:\home` {
		t.Fatalf("decoded = %#v", decoded.Message)
	}
	var missingHome Frame
	if err := json.Unmarshal([]byte(`{"version":1,"type":"register_installation_request"}`), &missingHome); err == nil {
		t.Fatal("register request without codex_home must not decode")
	}
	var nullPayload Frame
	if err := json.Unmarshal([]byte(`{"version":1,"type":"provision_sandbox_request","payload":null}`), &nullPayload); err == nil {
		t.Fatal("null request payload must not decode")
	}
	var trailingPayload Frame
	trailing := `{"version":1,"type":"provision_sandbox_request","payload":{"codex_home":"home","settings":{"proxy_ports":[],"allow_local_binding":false},"listeners":{"http_ports":[],"socks_ports":[]}}}{"extra":true}`
	if err := json.Unmarshal([]byte(trailing), &trailingPayload); err == nil {
		t.Fatal("trailing data must not decode")
	}
}

func TestResponseJSONMatchesRustStatusTags(t *testing.T) {
	cases := []struct {
		response Response
		want     string
	}{
		{Response{Kind: ResponseOK}, `{"status":"ok"}`},
		{Response{Kind: ResponseUnavailable}, `{"status":"unavailable"}`},
		{Response{Kind: ResponseError, Message: "refused"}, `{"status":"error","message":"refused"}`},
	}
	for _, testCase := range cases {
		payload, err := json.Marshal(testCase.response)
		if err != nil {
			t.Fatalf("Marshal(%v) error = %v", testCase.response.Kind, err)
		}
		if string(payload) != testCase.want {
			t.Fatalf("payload = %s, want %s", payload, testCase.want)
		}
		var decoded Response
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", payload, err)
		}
		if decoded != testCase.response {
			t.Fatalf("decoded = %#v, want %#v", decoded, testCase.response)
		}
	}
	if _, err := json.Marshal(Response{Kind: "bogus"}); err == nil {
		t.Fatal("unknown response status must fail")
	}
	var unknown Response
	if err := json.Unmarshal([]byte(`{"status":"bogus"}`), &unknown); err == nil {
		t.Fatal("unknown response status must not decode")
	}
}

func TestProvisionRequestRejectsUnknownFieldsLikeSerde(t *testing.T) {
	payload := []byte(`{"codex_home":"home","settings":{"proxy_ports":[],"allow_local_binding":false},"listeners":{"http_ports":[],"socks_ports":[]},"unexpected":true}`)
	frame := []byte(`{"version":1,"type":"provision_sandbox_request","payload":` + string(payload) + `}`)
	var decoded Frame
	if err := json.Unmarshal(frame, &decoded); err == nil {
		t.Fatal("unknown request fields must be rejected")
	}
}

func TestReadFrameReportsCleanEOFAndOversizedFrames(t *testing.T) {
	frame, err := ReadFrame(bytes.NewReader(nil))
	if err != nil || frame != nil {
		t.Fatalf("ReadFrame(empty) = %#v, %v", frame, err)
	}
	oversized := []byte{0xff, 0xff, 0xff, 0xff}
	if _, err := ReadFrame(bytes.NewReader(oversized)); err == nil {
		t.Fatal("oversized frame must be rejected")
	}
	if err := WriteFrame(&bytes.Buffer{}, &Frame{Version: 1, Message: Message{Kind: "unknown"}}); err == nil {
		t.Fatal("unknown message kind must not be written")
	}
}
