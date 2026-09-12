package parity

import (
	"encoding/binary"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"codex_go/realtime"
	"codex_go/voicehost"
)

// voiceMessageTypes is the closed inherited-stdio control set. It must stay
// byte-identical to the Rust helper's `Message` enum so a same-build parent and
// helper cannot desynchronize.
var voiceMessageTypes = []string{
	string(voicehost.TypeHello),
	string(voicehost.TypeReady),
	string(voicehost.TypeInitializeRuntime),
	string(voicehost.TypeRuntimeReady),
	string(voicehost.TypeStartTransport),
	string(voicehost.TypeOffer),
	string(voicehost.TypeApplyAnswer),
	string(voicehost.TypeTransportReady),
	string(voicehost.TypeTransportTimedOut),
	string(voicehost.TypeOpenDevices),
	string(voicehost.TypeDevicesOpened),
	string(voicehost.TypeSetAudioControls),
	string(voicehost.TypeAudioControlsApplied),
	string(voicehost.TypeInspectAudio),
	string(voicehost.TypeAudioState),
	string(voicehost.TypeClose),
	string(voicehost.TypeClosed),
}

// TestRustVoiceControlProtocolSurfaceAgainstGo pins the voice control contract
// against Rust `realtime-webrtc/src/protocol.rs`: the frame bound, the SDP
// bound, the closed message set, a big-endian frame round-trip for every
// message type, and the fixed native-runtime environment.
func TestRustVoiceControlProtocolSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "realtime-webrtc", "src", "protocol.rs")))

	bound := regexp.MustCompile(`pub const MAX_FRAME_BYTES: usize = ([0-9]+) \* ([0-9]+);`).FindStringSubmatch(source)
	if bound == nil {
		t.Fatal("Rust MAX_FRAME_BYTES declaration not found")
	}
	left, err := strconv.Atoi(bound[1])
	if err != nil {
		t.Fatal(err)
	}
	right, err := strconv.Atoi(bound[2])
	if err != nil {
		t.Fatal(err)
	}
	if voicehost.MaxFrameBytes != left*right {
		t.Fatalf("MaxFrameBytes = %d, Rust bound = %d", voicehost.MaxFrameBytes, left*right)
	}

	if _, err := voicehost.NewSessionDescription(strings.Repeat("a", 64*1024)); err != nil {
		t.Fatalf("64 KiB SDP rejected: %v", err)
	}
	if _, err := voicehost.NewSessionDescription(strings.Repeat("a", 64*1024+1)); err == nil {
		t.Fatal("oversized SDP accepted")
	}
	if _, err := voicehost.NewSessionDescription(""); err == nil {
		t.Fatal("empty SDP accepted")
	}

	rustTypes := rustVoiceMessageTypes(t, source)
	goTypes := append([]string(nil), voiceMessageTypes...)
	sort.Strings(rustTypes)
	sort.Strings(goTypes)
	if !reflect.DeepEqual(goTypes, rustTypes) {
		t.Fatalf("voice control message surface drift\nGo:   %v\nRust: %v", goTypes, rustTypes)
	}

	for _, message := range voiceRoundTripMessages(t) {
		frame, err := voicehost.EncodeFrame(message)
		if err != nil {
			t.Fatalf("EncodeFrame(%s): %v", message.Type, err)
		}
		if len(frame) < 4 || int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-4 {
			t.Fatalf("frame for %s does not start with a big-endian u32 length", message.Type)
		}
		decoded, err := voicehost.DecodeFrame(frame)
		if err != nil {
			t.Fatalf("DecodeFrame(%s): %v", message.Type, err)
		}
		if decoded.Type != message.Type {
			t.Fatalf("round-trip type = %s, want %s", decoded.Type, message.Type)
		}
	}

	literal := regexp.MustCompile(`\("(GST_[A-Z0-9_]+)", "([^"]*)"\)`).FindAllStringSubmatch(source, -1)
	wantValues := map[string]string{}
	for _, match := range literal {
		wantValues[match[1]] = match[2]
	}
	rustKeys := uniqueStringsInOrder(regexp.MustCompile(`"(GST_[A-Z0-9_]+)"`).FindAllStringSubmatch(source, -1))
	goEnvironment := voicehost.RuntimeEnvironment()
	if len(goEnvironment) != len(rustKeys) {
		t.Fatalf("runtime environment size = %d, Rust keys = %v", len(goEnvironment), rustKeys)
	}
	for index, pair := range goEnvironment {
		if pair[0] != rustKeys[index] {
			t.Fatalf("runtime environment key %d = %q, Rust = %q", index, pair[0], rustKeys[index])
		}
		if pair[0] == "GST_REGISTRY" {
			// Rust declares this one with a platform conditional.
			if pair[1] != "NUL" && pair[1] != "/dev/null" {
				t.Fatalf("GST_REGISTRY = %q, want NUL or /dev/null", pair[1])
			}
			continue
		}
		want, ok := wantValues[pair[0]]
		if !ok {
			t.Fatalf("Rust runtime environment is missing %s", pair[0])
		}
		if pair[1] != want {
			t.Fatalf("runtime environment %s = %q, Rust = %q", pair[0], pair[1], want)
		}
	}
}

// TestRustVoiceHelperExitStagesAgainstGo pins the same-build helper exit codes
// in both directions: every Rust stage maps to the Go code and back.
func TestRustVoiceHelperExitStagesAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "realtime-webrtc", "src", "helper_exit.rs")))
	matches := regexp.MustCompile(`(?m)^\s*([A-Z][A-Za-z0-9]*) = ([0-9]+),`).FindAllStringSubmatch(source, -1)
	codes := map[string]int{}
	for _, match := range matches {
		code, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatal(err)
		}
		codes[match[1]] = code
	}

	cases := []struct {
		rust    string
		goStage voicehost.HelperExitStage
	}{
		{"ControlRead", voicehost.HelperExitControlRead},
		{"ControlQueue", voicehost.HelperExitControlQueue},
		{"ParentGone", voicehost.HelperExitParentGone},
		{"Runtime", voicehost.HelperExitRuntime},
		{"Transport", voicehost.HelperExitTransport},
		{"OpenDevices", voicehost.HelperExitOpenDevices},
		{"AudioIngress", voicehost.HelperExitAudioIngress},
		{"AudioService", voicehost.HelperExitAudioService},
		{"InspectAudio", voicehost.HelperExitInspectAudio},
		{"AudioControls", voicehost.HelperExitAudioControls},
		{"Reply", voicehost.HelperExitReply},
		{"Shutdown", voicehost.HelperExitShutdown},
		{"ControlSequence", voicehost.HelperExitControlSequence},
		{"Playout", voicehost.HelperExitPlayout},
		{"Render", voicehost.HelperExitRender},
		{"Capture", voicehost.HelperExitCapture},
		{"Device", voicehost.HelperExitDevice},
		{"Send", voicehost.HelperExitSend},
	}
	if len(codes) != len(cases) {
		t.Fatalf("Rust helper exit stage count = %d, want %d", len(codes), len(cases))
	}
	for _, testCase := range cases {
		code, ok := codes[testCase.rust]
		if !ok {
			t.Fatalf("Rust helper exit stage %q not found", testCase.rust)
		}
		if testCase.goStage.Code() != code {
			t.Fatalf("helper exit stage %s = %d, Rust %s = %d",
				testCase.goStage, testCase.goStage.Code(), testCase.rust, code)
		}
		stage, ok := voicehost.HelperExitStageFromCode(code)
		if !ok || stage != testCase.goStage {
			t.Fatalf("HelperExitStageFromCode(%d) = %v/%v, want %v", code, stage, ok, testCase.goStage)
		}
	}
}

// TestRustThreadRealtimeSurfaceAgainstGo pins the app-server realtime RPC and
// notification names against Rust
// `app-server-protocol/src/protocol/common.rs`.
func TestRustThreadRealtimeSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "app-server-protocol", "src", "protocol", "common.rs")))
	quoted := regexp.MustCompile(`"(thread/realtime/[a-zA-Z/]+)"`).FindAllStringSubmatch(source, -1)
	rustNames := uniqueStringsInOrder(quoted)
	goNames := []string{
		string(realtime.MethodStart),
		string(realtime.MethodAppendAudio),
		string(realtime.MethodAppendText),
		string(realtime.MethodAppendSpeech),
		string(realtime.MethodStop),
		string(realtime.MethodListVoices),
		string(realtime.NotificationStarted),
		string(realtime.NotificationItemAdded),
		string(realtime.NotificationItemStarted),
		string(realtime.NotificationItemTranscriptDelta),
		string(realtime.NotificationItemCompleted),
		string(realtime.NotificationTranscriptDelta),
		string(realtime.NotificationTranscriptDone),
		string(realtime.NotificationOutputAudioDelta),
		string(realtime.NotificationSDP),
		string(realtime.NotificationError),
		string(realtime.NotificationClosed),
	}
	sort.Strings(rustNames)
	sort.Strings(goNames)
	if !reflect.DeepEqual(goNames, rustNames) {
		t.Fatalf("thread/realtime surface drift\nGo:   %v\nRust: %v", goNames, rustNames)
	}
}

func rustVoiceMessageTypes(t *testing.T, source string) []string {
	t.Helper()
	start := strings.Index(source, "pub enum Message {")
	if start < 0 {
		t.Fatal("Rust voice Message enum not found")
	}
	rest := source[start:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatal("Rust voice Message enum end not found")
	}
	variants := regexp.MustCompile(`(?m)^    ([A-Z][A-Za-z0-9_]*)`).FindAllStringSubmatch(rest[:end], -1)
	types := make([]string, 0, len(variants))
	for _, variant := range variants {
		name := variant[1]
		types = append(types, strings.ToLower(name[:1])+name[1:])
	}
	return types
}

func voiceRoundTripMessages(t *testing.T) []voicehost.Message {
	t.Helper()
	sdp, err := voicehost.NewSessionDescription("v=0\r\n")
	if err != nil {
		t.Fatal(err)
	}
	protocol := uint32(1)
	controls := voicehost.AudioControls{}
	state := voicehost.AudioState{}
	return []voicehost.Message{
		{Type: voicehost.TypeHello, Protocol: &protocol, BuildCommit: "test"},
		{Type: voicehost.TypeReady},
		{Type: voicehost.TypeInitializeRuntime},
		{Type: voicehost.TypeRuntimeReady},
		{Type: voicehost.TypeStartTransport},
		{Type: voicehost.TypeOffer, SDP: &sdp},
		{Type: voicehost.TypeApplyAnswer, SDP: &sdp},
		{Type: voicehost.TypeTransportReady},
		{Type: voicehost.TypeTransportTimedOut},
		{Type: voicehost.TypeOpenDevices},
		{Type: voicehost.TypeDevicesOpened},
		{Type: voicehost.TypeSetAudioControls, Controls: &controls},
		{Type: voicehost.TypeAudioControlsApplied},
		{Type: voicehost.TypeInspectAudio},
		{Type: voicehost.TypeAudioState, State: &state},
		{Type: voicehost.TypeClose},
		{Type: voicehost.TypeClosed},
	}
}

func uniqueStringsInOrder(matches [][]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, match := range matches {
		if len(match) < 2 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		out = append(out, match[1])
	}
	return out
}
