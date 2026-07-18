package appserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"codex_go/sandbox"
)

type TerminalSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

const maxPTYTerminalDimension = 32767

func (s *TerminalSize) Validate() error {
	return s.validateForMethod("process")
}

func (s *TerminalSize) validateForMethod(method string) error {
	if s == nil {
		return nil
	}
	if s.Rows == 0 || s.Cols == 0 {
		if method == "command/exec" {
			return invalidFSRequest("command/exec size rows and cols must be greater than 0")
		}
		return invalidFSRequest("process size rows and cols must be greater than 0")
	}
	if s.Rows > maxPTYTerminalDimension || s.Cols > maxPTYTerminalDimension {
		if method == "command/exec" {
			return invalidFSRequest("command/exec size rows and cols must be less than or equal to 32767")
		}
		return invalidFSRequest("process size rows and cols must be less than or equal to 32767")
	}
	return nil
}

type ProcessSpawnParams struct {
	Command            []string           `json:"command"`
	ProcessHandle      string             `json:"processHandle"`
	CWD                string             `json:"cwd"`
	TTY                bool               `json:"tty,omitempty"`
	StreamStdin        bool               `json:"streamStdin,omitempty"`
	StreamStdoutStderr bool               `json:"streamStdoutStderr,omitempty"`
	OutputBytesCap     *OptionalInt       `json:"outputBytesCap,omitempty"`
	TimeoutMS          *OptionalInt64     `json:"timeoutMs,omitempty"`
	Env                map[string]*string `json:"env,omitempty"`
	Size               *TerminalSize      `json:"size,omitempty"`
}

func (p *ProcessSpawnParams) UnmarshalJSON(data []byte) error {
	type processSpawnParamsAlias ProcessSpawnParams
	var decoded processSpawnParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = ProcessSpawnParams(decoded)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["outputBytesCap"]; ok {
		value := &OptionalInt{}
		if err := value.UnmarshalJSON(raw); err != nil {
			return err
		}
		p.OutputBytesCap = value
	}
	if raw, ok := fields["timeoutMs"]; ok {
		value := &OptionalInt64{}
		if err := value.UnmarshalJSON(raw); err != nil {
			return err
		}
		p.TimeoutMS = value
	}
	return nil
}

func (p *ProcessSpawnParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	if len(p.Command) == 0 {
		return invalidFSRequest("command must not be empty")
	}
	if strings.TrimSpace(p.ProcessHandle) == "" {
		return invalidFSRequest("processHandle must not be empty")
	}
	if _, err := validateAbsolute(p.CWD); err != nil {
		return err
	}
	if p.TTY {
		p.StreamStdin = true
		p.StreamStdoutStderr = true
	}
	if p.Size != nil && !p.TTY {
		return invalidFSRequest("process/spawn size requires tty: true")
	}
	if p.OutputBytesCap != nil && p.OutputBytesCap.Value != nil && *p.OutputBytesCap.Value < 0 {
		return fmt.Errorf("%w: outputBytesCap must be non-negative", ErrInvalidFSRequest)
	}
	if p.TimeoutMS != nil && p.TimeoutMS.Value != nil && *p.TimeoutMS.Value < 0 {
		return invalidFSRequest(fmt.Sprintf("process/spawn timeoutMs must be non-negative, got %d", *p.TimeoutMS.Value))
	}
	return p.Size.Validate()
}

type ProcessSpawnResponse struct{}

type ProcessWriteStdinParams struct {
	ProcessHandle string  `json:"processHandle"`
	DeltaBase64   *string `json:"deltaBase64,omitempty"`
	CloseStdin    bool    `json:"closeStdin,omitempty"`
}

func (p *ProcessWriteStdinParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ProcessHandle) == "" {
		return fmt.Errorf("%w: processHandle is required", ErrInvalidFSRequest)
	}
	if p.DeltaBase64 == nil && !p.CloseStdin {
		return invalidFSRequest("process/writeStdin requires deltaBase64 or closeStdin")
	}
	if p.DeltaBase64 != nil {
		if _, err := base64.StdEncoding.DecodeString(*p.DeltaBase64); err != nil {
			return invalidFSRequest(fmt.Sprintf("invalid deltaBase64: %v", err))
		}
	}
	return nil
}

type ProcessWriteStdinResponse struct{}

type ProcessKillParams struct {
	ProcessHandle string `json:"processHandle"`
}

func (p *ProcessKillParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ProcessHandle) == "" {
		return fmt.Errorf("%w: processHandle is required", ErrInvalidFSRequest)
	}
	return nil
}

type ProcessKillResponse struct{}

type ProcessResizePtyParams struct {
	ProcessHandle string       `json:"processHandle"`
	Size          TerminalSize `json:"size"`
}

func (p *ProcessResizePtyParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ProcessHandle) == "" {
		return fmt.Errorf("%w: processHandle is required", ErrInvalidFSRequest)
	}
	return p.Size.Validate()
}

type ProcessResizePtyResponse struct{}

type OutputStream string

const (
	StreamStdout OutputStream = "stdout"
	StreamStderr OutputStream = "stderr"
)

type ProcessOutputDeltaNotification struct {
	ProcessHandle string       `json:"processHandle"`
	Stream        OutputStream `json:"stream"`
	DeltaBase64   string       `json:"deltaBase64"`
	CapReached    bool         `json:"capReached"`
}

type ProcessExitedNotification struct {
	ProcessHandle    string `json:"processHandle"`
	ExitCode         int32  `json:"exitCode"`
	Stdout           string `json:"stdout"`
	StdoutCapReached bool   `json:"stdoutCapReached"`
	Stderr           string `json:"stderr"`
	StderrCapReached bool   `json:"stderrCapReached"`
}

type CommandExecParams struct {
	Command            []string               `json:"command"`
	ProcessID          *string                `json:"processId,omitempty"`
	ThreadID           *string                `json:"threadId,omitempty"`
	TurnID             *string                `json:"turnId,omitempty"`
	ItemID             *string                `json:"itemId,omitempty"`
	TTY                bool                   `json:"tty,omitempty"`
	StreamStdin        bool                   `json:"streamStdin,omitempty"`
	StreamStdoutStderr bool                   `json:"streamStdoutStderr,omitempty"`
	OutputBytesCap     *int                   `json:"outputBytesCap,omitempty"`
	DisableOutputCap   bool                   `json:"disableOutputCap,omitempty"`
	DisableTimeout     bool                   `json:"disableTimeout,omitempty"`
	TimeoutMS          *int64                 `json:"timeoutMs,omitempty"`
	CWD                *string                `json:"cwd,omitempty"`
	Env                map[string]*string     `json:"env,omitempty"`
	Size               *TerminalSize          `json:"size,omitempty"`
	SandboxPolicy      *sandbox.SandboxPolicy `json:"sandboxPolicy,omitempty"`
	PermissionProfile  *string                `json:"permissionProfile,omitempty"`
}

func (p *CommandExecParams) Validate(defaultCWD string) error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidFSRequest)
	}
	if len(p.Command) == 0 {
		return jsonRPCInvalidRequest("command must not be empty")
	}
	if p.TTY {
		p.StreamStdin = true
		p.StreamStdoutStderr = true
	}
	if (p.TTY || p.StreamStdin || p.StreamStdoutStderr) && (p.ProcessID == nil || *p.ProcessID == "") {
		return jsonRPCInvalidRequest("command/exec tty or streaming requires a client-supplied processId")
	}
	if p.hasTerminalInteractionContext() && p.terminalInteractionContext() == nil {
		return invalidFSRequest("command/exec terminal interaction context requires threadId, turnId, and itemId")
	}
	if p.OutputBytesCap != nil && p.DisableOutputCap {
		return invalidFSRequest("command/exec cannot set both outputBytesCap and disableOutputCap")
	}
	if p.OutputBytesCap != nil && *p.OutputBytesCap < 0 {
		return fmt.Errorf("%w: outputBytesCap must be non-negative", ErrInvalidFSRequest)
	}
	if p.TimeoutMS != nil && p.DisableTimeout {
		return invalidFSRequest("command/exec cannot set both timeoutMs and disableTimeout")
	}
	if p.TimeoutMS != nil && *p.TimeoutMS < 0 {
		return invalidFSRequest(fmt.Sprintf("command/exec timeoutMs must be non-negative, got %d", *p.TimeoutMS))
	}
	if p.SandboxPolicy != nil && p.PermissionProfile != nil {
		return jsonRPCInvalidRequest("`permissionProfile` cannot be combined with `sandboxPolicy`")
	}
	if p.Size != nil && !p.TTY {
		return invalidFSRequest("command/exec size requires tty: true")
	}
	if err := p.Size.validateForMethod("command/exec"); err != nil {
		return err
	}
	cwd := commandExecCWD(p, defaultCWD)
	if cwd != "" && !filepath.IsAbs(cwd) {
		return fmt.Errorf("%w: cwd must be absolute", ErrInvalidFSRequest)
	}
	return nil
}

func (p *CommandExecParams) hasTerminalInteractionContext() bool {
	if p == nil {
		return false
	}
	return p.ThreadID != nil || p.TurnID != nil || p.ItemID != nil
}

func (p *CommandExecParams) terminalInteractionContext() *CommandExecTerminalInteractionContext {
	if p == nil {
		return nil
	}
	threadID := stringPtrValue(p.ThreadID)
	turnID := stringPtrValue(p.TurnID)
	itemID := stringPtrValue(p.ItemID)
	if threadID == "" || turnID == "" || itemID == "" {
		return nil
	}
	return &CommandExecTerminalInteractionContext{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
	}
}

type CommandExecResponse struct {
	ExitCode int32  `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type CommandExecOutputDeltaNotification struct {
	ProcessID   string       `json:"processId"`
	Stream      OutputStream `json:"stream"`
	DeltaBase64 string       `json:"deltaBase64"`
	CapReached  bool         `json:"capReached"`
}

type CommandExecWriteParams struct {
	ProcessID   string  `json:"processId"`
	DeltaBase64 *string `json:"deltaBase64,omitempty"`
	CloseStdin  bool    `json:"closeStdin,omitempty"`
}

func (p *CommandExecWriteParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ProcessID) == "" {
		return fmt.Errorf("%w: processId is required", ErrInvalidFSRequest)
	}
	if p.DeltaBase64 == nil && !p.CloseStdin {
		return invalidFSRequest("command/exec/write requires deltaBase64 or closeStdin")
	}
	if p.DeltaBase64 != nil {
		if _, err := base64.StdEncoding.DecodeString(*p.DeltaBase64); err != nil {
			return invalidFSRequest(fmt.Sprintf("invalid deltaBase64: %v", err))
		}
	}
	return nil
}

type CommandExecWriteResponse struct{}

type CommandExecTerminateParams struct {
	ProcessID string `json:"processId"`
}

func (p *CommandExecTerminateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ProcessID) == "" {
		return fmt.Errorf("%w: processId is required", ErrInvalidFSRequest)
	}
	return nil
}

type CommandExecTerminateResponse struct{}

type CommandExecResizeParams struct {
	ProcessID string       `json:"processId"`
	Size      TerminalSize `json:"size"`
}

func (p *CommandExecResizeParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ProcessID) == "" {
		return fmt.Errorf("%w: processId is required", ErrInvalidFSRequest)
	}
	return p.Size.validateForMethod("command/exec")
}

type CommandExecResizeResponse struct{}

type OptionalInt struct {
	Set   bool
	Value *int
}

func (o *OptionalInt) UnmarshalJSON(data []byte) error {
	o.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		o.Value = nil
		return nil
	}
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (o *OptionalInt) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.Value)
}

type OptionalInt64 struct {
	Set   bool
	Value *int64
}

func (o *OptionalInt64) UnmarshalJSON(data []byte) error {
	o.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		o.Value = nil
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (o *OptionalInt64) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.Value)
}
