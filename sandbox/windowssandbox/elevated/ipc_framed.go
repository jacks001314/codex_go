package elevated

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	coresandbox "codex_go/sandbox"
	json "github.com/goccy/go-json"
)

const (
	MaxFrameLen        = 8 * 1024 * 1024
	IPCProtocolVersion = 4
)

type MessageType string

const (
	MessageTypeSpawnRequest MessageType = "spawn_request"
	MessageTypeSpawnReady   MessageType = "spawn_ready"
	MessageTypeOutput       MessageType = "output"
	MessageTypeStdin        MessageType = "stdin"
	MessageTypeCloseStdin   MessageType = "close_stdin"
	MessageTypeResize       MessageType = "resize"
	MessageTypeExit         MessageType = "exit"
	MessageTypeError        MessageType = "error"
	MessageTypeTerminate    MessageType = "terminate"
)

type OutputStream string

const (
	OutputStreamStdout OutputStream = "stdout"
	OutputStreamStderr OutputStream = "stderr"
)

type SpawnRequest struct {
	Command           []string                       `json:"command"`
	CWD               string                         `json:"cwd"`
	Env               map[string]string              `json:"env"`
	PermissionProfile *coresandbox.PermissionProfile `json:"permission_profile"`
	WorkspaceRoots    []string                       `json:"workspace_roots"`
	CodexHome         string                         `json:"codex_home"`
	RealCodexHome     string                         `json:"real_codex_home"`
	CapSIDs           []string                       `json:"cap_sids"`
	TimeoutMS         *uint64                        `json:"timeout_ms"`
	TTY               bool                           `json:"tty"`
	StdinOpen         bool                           `json:"stdin_open"`
	UsePrivateDesktop bool                           `json:"use_private_desktop"`
}

type SpawnReady struct {
	ProcessID uint32 `json:"process_id"`
}

type OutputPayload struct {
	Stream  OutputStream `json:"stream"`
	DataB64 string       `json:"data_b64"`
}

type StdinPayload struct {
	DataB64 string `json:"data_b64"`
}

type ExitPayload struct {
	ExitCode int  `json:"exit_code"`
	TimedOut bool `json:"timed_out"`
}

type ErrorStage string

const (
	ErrorStageReadSpawnRequest ErrorStage = "read_spawn_request"
	ErrorStageSpawnChild       ErrorStage = "spawn_child"
	ErrorStageWriteSpawnReady  ErrorStage = "write_spawn_ready"
)

type ErrorPayload struct {
	Message          string     `json:"message"`
	Stage            ErrorStage `json:"stage"`
	WindowsErrorCode *uint32    `json:"windows_error_code,omitempty"`
}

type ResizePayload struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type EmptyPayload struct{}

type Message struct {
	SpawnRequest *SpawnRequest  `json:"spawnRequest,omitempty"`
	SpawnReady   *SpawnReady    `json:"spawnReady,omitempty"`
	Output       *OutputPayload `json:"output,omitempty"`
	Stdin        *StdinPayload  `json:"stdin,omitempty"`
	CloseStdin   *EmptyPayload  `json:"closeStdin,omitempty"`
	Exit         *ExitPayload   `json:"exit,omitempty"`
	Error        *ErrorPayload  `json:"error,omitempty"`
	Resize       *ResizePayload `json:"resize,omitempty"`
	Terminate    *EmptyPayload  `json:"terminate,omitempty"`
}

type FramedMessage struct {
	Version int     `json:"version"`
	Message Message `json:"-"`
}

func ReadFrame(r io.Reader) (*FramedMessage, error) {
	if r == nil {
		return nil, errors.New("reader is nil")
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil
		}
		return nil, err
	}
	frameLen := binary.LittleEndian.Uint32(lenBuf[:])
	if frameLen > MaxFrameLen {
		return nil, fmt.Errorf("frame too large: %d", frameLen)
	}
	payload := make([]byte, frameLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	var msg FramedMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func WriteFrame(w io.Writer, msg *FramedMessage) error {
	if w == nil {
		return errors.New("writer is nil")
	}
	if msg == nil {
		return errors.New("message is nil")
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameLen {
		return fmt.Errorf("frame too large: %d", len(payload))
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if flusher, ok := w.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func EncodeBytes(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func DecodeBytes(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}

func (m Message) messageTypePayload() (MessageType, any, error) {
	var typ MessageType
	var payload any
	set := func(candidate MessageType, value any) error {
		if typ != "" {
			return fmt.Errorf("multiple IPC message variants set: %s and %s", typ, candidate)
		}
		typ = candidate
		payload = value
		return nil
	}
	if m.SpawnRequest != nil {
		if err := set(MessageTypeSpawnRequest, m.SpawnRequest); err != nil {
			return "", nil, err
		}
	}
	if m.SpawnReady != nil {
		if err := set(MessageTypeSpawnReady, m.SpawnReady); err != nil {
			return "", nil, err
		}
	}
	if m.Output != nil {
		if err := set(MessageTypeOutput, m.Output); err != nil {
			return "", nil, err
		}
	}
	if m.Stdin != nil {
		if err := set(MessageTypeStdin, m.Stdin); err != nil {
			return "", nil, err
		}
	}
	if m.CloseStdin != nil {
		if err := set(MessageTypeCloseStdin, m.CloseStdin); err != nil {
			return "", nil, err
		}
	}
	if m.Resize != nil {
		if err := set(MessageTypeResize, m.Resize); err != nil {
			return "", nil, err
		}
	}
	if m.Exit != nil {
		if err := set(MessageTypeExit, m.Exit); err != nil {
			return "", nil, err
		}
	}
	if m.Error != nil {
		if err := set(MessageTypeError, m.Error); err != nil {
			return "", nil, err
		}
	}
	if m.Terminate != nil {
		if err := set(MessageTypeTerminate, m.Terminate); err != nil {
			return "", nil, err
		}
	}
	if typ == "" {
		return "", nil, errors.New("empty IPC message")
	}
	return typ, payload, nil
}

func (m Message) MarshalJSON() ([]byte, error) {
	typ, payload, err := m.messageTypePayload()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Type    MessageType `json:"type"`
		Payload any         `json:"payload"`
	}{Type: typ, Payload: payload})
}

func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    MessageType     `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	return m.unmarshalVariant(raw.Type, raw.Payload)
}

func (m *Message) unmarshalVariant(typ MessageType, payload json.RawMessage) error {
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	*m = Message{}
	switch typ {
	case MessageTypeSpawnRequest:
		var value SpawnRequest
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.SpawnRequest = &value
	case MessageTypeSpawnReady:
		var value SpawnReady
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.SpawnReady = &value
	case MessageTypeOutput:
		var value OutputPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.Output = &value
	case MessageTypeStdin:
		var value StdinPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.Stdin = &value
	case MessageTypeCloseStdin:
		var value EmptyPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.CloseStdin = &value
	case MessageTypeResize:
		var value ResizePayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.Resize = &value
	case MessageTypeExit:
		var value ExitPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.Exit = &value
	case MessageTypeError:
		var value ErrorPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.Error = &value
	case MessageTypeTerminate:
		var value EmptyPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		m.Terminate = &value
	default:
		return fmt.Errorf("unknown IPC message type %q", typ)
	}
	return nil
}

func (m Message) Type() MessageType {
	typ, _, _ := m.messageTypePayload()
	return typ
}

func (f FramedMessage) MarshalJSON() ([]byte, error) {
	typ, payload, err := f.Message.messageTypePayload()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version int         `json:"version"`
		Type    MessageType `json:"type"`
		Payload any         `json:"payload"`
	}{Version: f.Version, Type: typ, Payload: payload})
}

func (f *FramedMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Version int             `json:"version"`
		Type    MessageType     `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var msg Message
	if err := msg.unmarshalVariant(raw.Type, raw.Payload); err != nil {
		return err
	}
	f.Version = raw.Version
	f.Message = msg
	return nil
}

func (r SpawnRequest) MarshalJSON() ([]byte, error) {
	type wireSpawnRequest struct {
		Command           []string               `json:"command"`
		CWD               string                 `json:"cwd"`
		Env               map[string]string      `json:"env"`
		PermissionProfile *rustPermissionProfile `json:"permission_profile"`
		WorkspaceRoots    []string               `json:"workspace_roots"`
		CodexHome         string                 `json:"codex_home"`
		RealCodexHome     string                 `json:"real_codex_home"`
		CapSIDs           []string               `json:"cap_sids"`
		TimeoutMS         *uint64                `json:"timeout_ms"`
		TTY               bool                   `json:"tty"`
		StdinOpen         bool                   `json:"stdin_open"`
		UsePrivateDesktop bool                   `json:"use_private_desktop"`
	}
	return json.Marshal(wireSpawnRequest{
		Command:           append([]string(nil), r.Command...),
		CWD:               r.CWD,
		Env:               cloneStringMap(r.Env),
		PermissionProfile: rustPermissionProfileFromSandbox(r.PermissionProfile),
		WorkspaceRoots:    append([]string(nil), r.WorkspaceRoots...),
		CodexHome:         r.CodexHome,
		RealCodexHome:     r.RealCodexHome,
		CapSIDs:           append([]string(nil), r.CapSIDs...),
		TimeoutMS:         r.TimeoutMS,
		TTY:               r.TTY,
		StdinOpen:         r.StdinOpen,
		UsePrivateDesktop: r.UsePrivateDesktop,
	})
}

func (r *SpawnRequest) UnmarshalJSON(data []byte) error {
	type wireSpawnRequest struct {
		Command           []string               `json:"command"`
		CWD               string                 `json:"cwd"`
		Env               map[string]string      `json:"env"`
		PermissionProfile *rustPermissionProfile `json:"permission_profile"`
		WorkspaceRoots    []string               `json:"workspace_roots"`
		CodexHome         string                 `json:"codex_home"`
		RealCodexHome     string                 `json:"real_codex_home"`
		CapSIDs           []string               `json:"cap_sids"`
		TimeoutMS         *uint64                `json:"timeout_ms"`
		TTY               bool                   `json:"tty"`
		StdinOpen         bool                   `json:"stdin_open"`
		UsePrivateDesktop bool                   `json:"use_private_desktop"`
	}
	var wire wireSpawnRequest
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.Command = append([]string(nil), wire.Command...)
	r.CWD = wire.CWD
	r.Env = cloneStringMap(wire.Env)
	r.PermissionProfile = sandboxPermissionProfileFromRust(wire.PermissionProfile)
	r.WorkspaceRoots = append([]string(nil), wire.WorkspaceRoots...)
	r.CodexHome = wire.CodexHome
	r.RealCodexHome = wire.RealCodexHome
	r.CapSIDs = append([]string(nil), wire.CapSIDs...)
	r.TimeoutMS = wire.TimeoutMS
	r.TTY = wire.TTY
	r.StdinOpen = wire.StdinOpen
	r.UsePrivateDesktop = wire.UsePrivateDesktop
	return nil
}

type rustPermissionProfile struct {
	Type       string                 `json:"type"`
	FileSystem *rustManagedFileSystem `json:"file_system,omitempty"`
	Network    string                 `json:"network,omitempty"`
}

type rustManagedFileSystem struct {
	Type             string        `json:"type"`
	Entries          []rustFSEntry `json:"entries,omitempty"`
	GlobScanMaxDepth *uint32       `json:"glob_scan_max_depth,omitempty"`
}

type rustFSEntry struct {
	Path   rustFSPath `json:"path"`
	Access string     `json:"access"`
}

type rustFSPath struct {
	Type    string         `json:"type"`
	Path    string         `json:"path,omitempty"`
	Pattern string         `json:"pattern,omitempty"`
	Value   *rustFSSpecial `json:"value,omitempty"`
}

type rustFSSpecial struct {
	Kind    string  `json:"kind"`
	Path    string  `json:"path,omitempty"`
	Subpath *string `json:"subpath,omitempty"`
}

func rustPermissionProfileFromSandbox(profile *coresandbox.PermissionProfile) *rustPermissionProfile {
	if profile == nil {
		return nil
	}
	if raw, err := coresandbox.RuntimePermissionProfileJSON(*profile); err == nil {
		var canonical rustPermissionProfile
		if json.Unmarshal([]byte(raw), &canonical) == nil && canonical.Type != "" {
			return &canonical
		}
	}
	if profile.Disabled {
		return &rustPermissionProfile{Type: "disabled"}
	}
	network := "restricted"
	if profile.AllowsNetwork() {
		network = "enabled"
	}
	policy := profile.SandboxPolicy
	if policy == nil {
		return &rustPermissionProfile{
			Type:       "managed",
			FileSystem: &rustManagedFileSystem{Type: "restricted", Entries: readOnlyFSEntries()},
			Network:    network,
		}
	}
	switch policy.Kind {
	case coresandbox.SandboxDangerFullAccess:
		return &rustPermissionProfile{Type: "disabled"}
	case "external-sandbox":
		return &rustPermissionProfile{Type: "external", Network: network}
	case coresandbox.SandboxWorkspaceWrite:
		return &rustPermissionProfile{
			Type:       "managed",
			FileSystem: &rustManagedFileSystem{Type: "restricted", Entries: workspaceWriteFSEntries(policy)},
			Network:    network,
		}
	default:
		return &rustPermissionProfile{
			Type:       "managed",
			FileSystem: &rustManagedFileSystem{Type: "restricted", Entries: readOnlyFSEntries()},
			Network:    network,
		}
	}
}

func sandboxPermissionProfileFromRust(profile *rustPermissionProfile) *coresandbox.PermissionProfile {
	if profile == nil {
		return nil
	}
	if raw, err := json.Marshal(profile); err == nil {
		if canonical, parseErr := coresandbox.ParseRuntimePermissionProfileJSON(string(raw)); parseErr == nil {
			return canonical
		}
	}
	networkEnabled := strings.EqualFold(profile.Network, "enabled")
	switch profile.Type {
	case "disabled":
		value := coresandbox.FullAccessPermissionProfile()
		return &value
	case "external":
		policy := coresandbox.NewExternalSandboxPolicy(coresandbox.NetworkRestricted)
		if networkEnabled {
			policy = coresandbox.NewExternalSandboxPolicy(coresandbox.NetworkEnabled)
		}
		return &coresandbox.PermissionProfile{SandboxPolicy: policy, NetworkEnabled: networkEnabled}
	case "managed":
		value := coresandbox.ReadOnlyPermissionProfile()
		value.NetworkEnabled = networkEnabled
		if profile.FileSystem != nil && strings.EqualFold(profile.FileSystem.Type, "unrestricted") {
			value.SandboxPolicy = coresandbox.NewDangerFullAccessPolicy()
			return &value
		}
		if profileHasProjectRootWrite(profile) {
			value = coresandbox.WorkspaceWritePermissionProfile()
			value.NetworkEnabled = networkEnabled
			for _, entry := range profile.FileSystem.Entries {
				if entry.Access == "write" && entry.Path.Type == "path" && entry.Path.Path != "" {
					value.SandboxPolicy.WritableRoots = append(value.SandboxPolicy.WritableRoots, entry.Path.Path)
				}
			}
		}
		return &value
	default:
		return nil
	}
}

func readOnlyFSEntries() []rustFSEntry {
	return []rustFSEntry{{
		Path:   specialFSPath("root", nil),
		Access: "read",
	}}
}

func workspaceWriteFSEntries(policy *coresandbox.SandboxPolicy) []rustFSEntry {
	entries := readOnlyFSEntries()
	entries = append(entries, rustFSEntry{Path: specialFSPath("project_roots", nil), Access: "write"})
	if policy == nil || !policy.ExcludeSlashTmp {
		entries = append(entries, rustFSEntry{Path: specialFSPath("slash_tmp", nil), Access: "write"})
	}
	if policy == nil || !policy.ExcludeTmpdirEnvVar {
		entries = append(entries, rustFSEntry{Path: specialFSPath("tmpdir", nil), Access: "write"})
	}
	if policy != nil {
		for _, root := range policy.WritableRoots {
			entries = append(entries, rustFSEntry{
				Path:   rustFSPath{Type: "path", Path: root},
				Access: "write",
			})
		}
	}
	for _, name := range []string{".git", ".agents", ".gcode"} {
		subpath := name
		entries = append(entries, rustFSEntry{Path: specialFSPath("project_roots", &subpath), Access: "read"})
	}
	return entries
}

func specialFSPath(kind string, subpath *string) rustFSPath {
	return rustFSPath{
		Type: "special",
		Value: &rustFSSpecial{
			Kind:    kind,
			Subpath: subpath,
		},
	}
}

func profileHasProjectRootWrite(profile *rustPermissionProfile) bool {
	if profile == nil || profile.FileSystem == nil {
		return false
	}
	for _, entry := range profile.FileSystem.Entries {
		if entry.Access != "write" || entry.Path.Type != "special" || entry.Path.Value == nil {
			continue
		}
		if entry.Path.Value.Kind == "project_roots" {
			return true
		}
	}
	return false
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
