package codemode

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// The UI plugin helper is a separate, narrowly scoped ABI (#57508). The child
// supplies correlation IDs, never authority or thread IDs.
const (
	uiPluginReady          = "codex-ui-plugin-process/3"
	uiPluginMaxFrameBytes  = 1024 * 1024
	uiPluginMaxInstances   = 32
	uiPluginMaxSourceBytes = 128 * 1024
	uiPluginMaxResultBytes = 64 * 1024
	uiPluginMaxMethodBytes = 1024
	uiPluginMaxPromises    = 1024
	uiPluginMaxInFlight    = 8
	uiPluginMaxScriptError = 16 * 1024
)

// Each instance reserves one queue slot per in-flight turn plus one for close;
// the output queue additionally reserves one slot for a rejected open.
const (
	uiPluginOutputSlotsPerInstance = uiPluginMaxInFlight + 1
	uiPluginCommandQueueCapacity   = uiPluginMaxInstances * (uiPluginMaxInFlight + 1)
	uiPluginOutputQueueCapacity    = uiPluginMaxInstances*(uiPluginOutputSlotsPerInstance+1) + 1
)

const (
	uiPluginReturnSync       = "sync"
	uiPluginReturnMaybeAsync = "maybeAsync"
)

const (
	uiPluginErrInvalidProgram   = "invalid_program"
	uiPluginErrInvalidResult    = "invalid_result"
	uiPluginErrExecutionFailed  = "execution_failed"
	uiPluginErrExecutionTimeout = "execution_timed_out"
	uiPluginErrQuotaExceeded    = "quota_exceeded"
	uiPluginErrInstanceClosed   = "instance_closed"
)

type uiPluginScriptError struct {
	Message   string `json:"message"`
	Line      *int   `json:"line"`
	Column    *int   `json:"column"`
	Truncated bool   `json:"truncated"`
}

type uiPluginRuntimeError struct {
	Code        string
	ScriptError *uiPluginScriptError
}

type uiPluginInboundPacket struct {
	Instance uint64          `json:"instance"`
	Message  json.RawMessage `json:"message"`
}

type uiPluginOutboundPacket struct {
	Instance uint64 `json:"instance"`
	Message  any    `json:"message"`
}

// uiPluginHostMessage is the decoded parent-to-child instruction.
type uiPluginHostMessage struct {
	Type       string
	Source     string
	ID         uint64
	Export     string
	ReturnMode string
}

func decodeUIPluginHostMessage(payload []byte) (uiPluginHostMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return uiPluginHostMessage{}, fmt.Errorf("failed to decode plugin message: %w", err)
	}
	var kind string
	if raw, ok := fields["type"]; !ok || json.Unmarshal(raw, &kind) != nil {
		return uiPluginHostMessage{}, fmt.Errorf("plugin message is missing a string type")
	}
	requireExactly := func(names ...string) error {
		if len(fields) != len(names) {
			return fmt.Errorf("plugin %s message has unexpected fields", kind)
		}
		for _, name := range names {
			if _, ok := fields[name]; !ok {
				return fmt.Errorf("plugin %s message is missing %s", kind, name)
			}
		}
		return nil
	}
	decode := func(name string, target any) error {
		raw := fields[name]
		if string(bytes.TrimSpace(raw)) == "null" {
			return fmt.Errorf("plugin %s message has invalid %s: null", kind, name)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return fmt.Errorf("plugin %s message has invalid %s: %w", kind, name, err)
		}
		return nil
	}
	message := uiPluginHostMessage{Type: kind}
	switch kind {
	case "initialize":
		if err := requireExactly("type", "source"); err != nil {
			return uiPluginHostMessage{}, err
		}
		if err := decode("source", &message.Source); err != nil {
			return uiPluginHostMessage{}, err
		}
	case "dispatch":
		if err := requireExactly("type", "id", "export", "returnMode"); err != nil {
			return uiPluginHostMessage{}, err
		}
		if err := decode("id", &message.ID); err != nil {
			return uiPluginHostMessage{}, err
		}
		if err := decode("export", &message.Export); err != nil {
			return uiPluginHostMessage{}, err
		}
		if err := decode("returnMode", &message.ReturnMode); err != nil {
			return uiPluginHostMessage{}, err
		}
		if message.ReturnMode != uiPluginReturnSync && message.ReturnMode != uiPluginReturnMaybeAsync {
			return uiPluginHostMessage{}, fmt.Errorf("plugin dispatch message has an invalid returnMode")
		}
	case "close":
		if err := requireExactly("type"); err != nil {
			return uiPluginHostMessage{}, err
		}
	default:
		return uiPluginHostMessage{}, fmt.Errorf("unknown plugin message type %q", kind)
	}
	return message, nil
}

// decodeUIPluginInboundPacket mirrors the Rust `Packet` envelope, which denies
// unknown fields and requires non-null `instance` and `message` members.
func decodeUIPluginInboundPacket(payload []byte) (uiPluginInboundPacket, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: %w", err)
	}
	if fields == nil {
		return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: expected an object")
	}
	for name := range fields {
		if name != "instance" && name != "message" {
			return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: unknown field %q", name)
		}
	}
	rawInstance, ok := fields["instance"]
	if !ok {
		return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: missing field \"instance\"")
	}
	rawMessage, ok := fields["message"]
	if !ok {
		return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: missing field \"message\"")
	}
	var packet uiPluginInboundPacket
	if err := decodeUIPluginEnvelopeValue(rawInstance, &packet.Instance); err != nil {
		return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: invalid \"instance\": %w", err)
	}
	if err := decodeUIPluginEnvelopeValue(rawMessage, &packet.Message); err != nil {
		return uiPluginInboundPacket{}, fmt.Errorf("failed to decode plugin packet: invalid \"message\": %w", err)
	}
	return packet, nil
}

func decodeUIPluginEnvelopeValue(raw json.RawMessage, target any) error {
	if string(bytes.TrimSpace(raw)) == "null" {
		return fmt.Errorf("must not be null")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func uiPluginInitializedMessage() any {
	return map[string]any{"type": "initialized"}
}

func uiPluginClosedMessage() any {
	return map[string]any{"type": "closed"}
}

func uiPluginPendingMessage(id uint64) any {
	return map[string]any{"type": "pending", "id": id}
}

func uiPluginReplyMessage(id uint64, value string) any {
	return map[string]any{"type": "reply", "id": id, "value": value}
}

func uiPluginErrorMessage(err uiPluginRuntimeError) any {
	return map[string]any{
		"type":        "error",
		"code":        err.Code,
		"scriptError": err.ScriptError,
	}
}

func readUIPluginFrame(reader io.Reader) ([]byte, bool, error) {
	var header [4]byte
	n, err := io.ReadFull(reader, header[:])
	if err == io.EOF && n == 0 {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	length := binary.LittleEndian.Uint32(header[:])
	if length > uiPluginMaxFrameBytes {
		return nil, false, fmt.Errorf("plugin IPC frame length %d exceeds %d bytes", length, uiPluginMaxFrameBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

func writeUIPluginFrame(writer io.Writer, payload []byte) error {
	if len(payload) > uiPluginMaxFrameBytes {
		return fmt.Errorf("plugin IPC frame length %d exceeds %d bytes", len(payload), uiPluginMaxFrameBytes)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	if _, err := buf.Write(payload); err != nil {
		return err
	}
	_, err := writer.Write(buf.Bytes())
	return err
}

func writeUIPluginJSON(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeUIPluginFrame(writer, payload)
}
