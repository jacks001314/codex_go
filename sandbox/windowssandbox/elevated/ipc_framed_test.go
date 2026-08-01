package elevated

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
	json "github.com/goccy/go-json"
)

func TestFramedRoundTrip(t *testing.T) {
	msg := &FramedMessage{
		Version: IPCProtocolVersion,
		Message: Message{Output: &OutputPayload{
			DataB64: EncodeBytes([]byte("hello")),
			Stream:  OutputStreamStdout,
		}},
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, msg); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	if got := binary.LittleEndian.Uint32(buf.Bytes()[:4]); int(got) != buf.Len()-4 {
		t.Fatalf("frame len prefix = %d, payload len %d", got, buf.Len()-4)
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if got == nil || got.Version != IPCProtocolVersion || got.Message.Output == nil {
		t.Fatalf("decoded frame = %#v", got)
	}
	data, err := DecodeBytes(got.Message.Output.DataB64)
	if err != nil {
		t.Fatalf("DecodeBytes() error = %v", err)
	}
	if string(data) != "hello" || got.Message.Output.Stream != OutputStreamStdout {
		t.Fatalf("decoded output = stream %q data %q", got.Message.Output.Stream, data)
	}
}

func TestFramedJSONUsesRustTaggedShape(t *testing.T) {
	code := uint32(1312)
	msg := &FramedMessage{
		Version: IPCProtocolVersion,
		Message: Message{Error: &ErrorPayload{
			Message:          "CreateProcessAsUserW failed",
			Stage:            ErrorStageSpawnChild,
			WindowsErrorCode: &code,
		}},
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got := string(payload)
	for _, want := range []string{
		`"version":4`,
		`"type":"error"`,
		`"stage":"spawn_child"`,
		`"windows_error_code":1312`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("encoded frame %s does not contain %s", got, want)
		}
	}
	var decoded FramedMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Message.Error == nil || decoded.Message.Error.WindowsErrorCode == nil || *decoded.Message.Error.WindowsErrorCode != code {
		t.Fatalf("decoded error = %#v", decoded.Message.Error)
	}
}

func TestSpawnRequestSerializesPermissionProfile(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = []string{`C:\cache`}
	timeout := uint64(1000)
	msg := &FramedMessage{
		Version: IPCProtocolVersion,
		Message: Message{SpawnRequest: &SpawnRequest{
			Command:           []string{"cmd.exe", "/c", "ver"},
			CWD:               `C:\workspace`,
			Env:               map[string]string{"TEMP": `C:\tmp`},
			PermissionProfile: &profile,
			WorkspaceRoots:    []string{`C:\workspace`},
			CodexHome:         `C:\codex`,
			RealCodexHome:     `C:\Users\codex`,
			CapSIDs:           []string{"S-1-15-3-1024-1"},
			TimeoutMS:         &timeout,
		}},
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("Unmarshal raw error = %v", err)
	}
	if raw["type"] != string(MessageTypeSpawnRequest) {
		t.Fatalf("type = %#v", raw["type"])
	}
	spawnPayload := raw["payload"].(map[string]any)
	permissionProfile := spawnPayload["permission_profile"].(map[string]any)
	if permissionProfile["type"] != "managed" || permissionProfile["network"] != "restricted" {
		t.Fatalf("permission profile = %#v", permissionProfile)
	}
	fileSystem := permissionProfile["file_system"].(map[string]any)
	if fileSystem["type"] != "restricted" {
		t.Fatalf("file_system = %#v", fileSystem)
	}
	entries := fileSystem["entries"].([]any)
	if len(entries) < 6 {
		t.Fatalf("entries = %#v", entries)
	}
	var decoded FramedMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Message.SpawnRequest == nil || decoded.Message.SpawnRequest.PermissionProfile == nil {
		t.Fatalf("decoded spawn request = %#v", decoded.Message.SpawnRequest)
	}
	if decoded.Message.SpawnRequest.PermissionProfile.SandboxPolicy.Kind != coresandbox.SandboxWorkspaceWrite {
		t.Fatalf("decoded profile = %#v", decoded.Message.SpawnRequest.PermissionProfile)
	}
}

func TestCanonicalPermissionProfileRoundTripIgnoresSymbolicSlashTmpOnWindows(t *testing.T) {
	readOnlyRaw := `{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"},{"path":{"type":"special","value":{"kind":"slash_tmp"}},"access":"write"}]},"network":"restricted"}`
	readOnly, err := coresandbox.ParseRuntimePermissionProfileJSON(readOnlyRaw)
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v", err)
	}
	wire := rustPermissionProfileFromSandbox(readOnly)
	if wire == nil || wire.FileSystem == nil || len(wire.FileSystem.Entries) != 2 {
		t.Fatalf("wire profile = %#v", wire)
	}
	roundTrip := sandboxPermissionProfileFromRust(wire)
	if roundTrip == nil || roundTrip.SandboxPolicy == nil || roundTrip.SandboxPolicy.Kind != coresandbox.SandboxReadOnly {
		t.Fatalf("round-trip profile = %#v", roundTrip)
	}

	rootWriteRaw := `{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"write"},{"path":{"type":"special","value":{"kind":"slash_tmp"}},"access":"deny"}]},"network":"restricted"}`
	rootWrite, err := coresandbox.ParseRuntimePermissionProfileJSON(rootWriteRaw)
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON(root write) error = %v", err)
	}
	rootRoundTrip := sandboxPermissionProfileFromRust(rustPermissionProfileFromSandbox(rootWrite))
	if rootRoundTrip == nil || rootRoundTrip.SandboxPolicy == nil || !rootRoundTrip.SandboxPolicy.HasFullDiskWriteAccess() || rootRoundTrip.HasDenyReadEntries() {
		t.Fatalf("root-write round-trip profile = %#v", rootRoundTrip)
	}
}

func TestReadFrameEOFAndOversize(t *testing.T) {
	got, err := ReadFrame(bytes.NewReader(nil))
	if err != nil || got != nil {
		t.Fatalf("ReadFrame(empty) = (%#v, %v), want nil nil", got, err)
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], MaxFrameLen+1)
	_, err = ReadFrame(bytes.NewReader(lenBuf[:]))
	if err == nil || !strings.Contains(err.Error(), "frame too large") {
		t.Fatalf("ReadFrame(oversize) error = %v", err)
	}
}
