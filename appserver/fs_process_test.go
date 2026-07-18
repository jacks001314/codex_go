package appserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestProcessSpawnParamsValidateNormalizesTTYStreams(t *testing.T) {
	params := &ProcessSpawnParams{
		Command:       []string{"sh", "-lc", "echo ok"},
		ProcessHandle: "proc-1",
		CWD:           t.TempDir(),
		TTY:           true,
		Size:          &TerminalSize{Rows: 24, Cols: 80},
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !params.StreamStdin || !params.StreamStdoutStderr {
		t.Fatalf("TTY did not enable streams: %+v", params)
	}
}

func TestProcessSpawnParamsValidateRejectsBadShapes(t *testing.T) {
	cases := []ProcessSpawnParams{
		{ProcessHandle: "p", CWD: t.TempDir()},
		{Command: []string{"echo"}, CWD: t.TempDir()},
		{Command: []string{"echo"}, ProcessHandle: "p", CWD: "relative"},
		{Command: []string{"echo"}, ProcessHandle: "p", CWD: t.TempDir(), Size: &TerminalSize{Rows: 1, Cols: 1}},
		{Command: []string{"echo"}, ProcessHandle: "p", CWD: t.TempDir(), TTY: true, Size: &TerminalSize{Rows: 0, Cols: 1}},
	}
	for i := range cases {
		if err := cases[i].Validate(); !errors.Is(err, ErrInvalidFSRequest) {
			t.Fatalf("case %d Validate() error = %v, want ErrInvalidFSRequest", i, err)
		}
	}
}

func TestProcessSpawnParamsValidateMessagesMatchRust(t *testing.T) {
	cwd := t.TempDir()
	timeoutMS := int64(-1)
	cases := []struct {
		name   string
		params ProcessSpawnParams
		want   string
	}{
		{
			name:   "empty command",
			params: ProcessSpawnParams{ProcessHandle: "p", CWD: cwd},
			want:   "command must not be empty",
		},
		{
			name:   "empty handle",
			params: ProcessSpawnParams{Command: []string{"echo"}, CWD: cwd},
			want:   "processHandle must not be empty",
		},
		{
			name:   "size without tty",
			params: ProcessSpawnParams{Command: []string{"echo"}, ProcessHandle: "p", CWD: cwd, Size: &TerminalSize{Rows: 24, Cols: 80}},
			want:   "process/spawn size requires tty: true",
		},
		{
			name:   "negative timeout",
			params: ProcessSpawnParams{Command: []string{"echo"}, ProcessHandle: "p", CWD: cwd, TimeoutMS: &OptionalInt64{Set: true, Value: &timeoutMS}},
			want:   "process/spawn timeoutMs must be non-negative, got -1",
		},
		{
			name:   "zero rows",
			params: ProcessSpawnParams{Command: []string{"echo"}, ProcessHandle: "p", CWD: cwd, TTY: true, Size: &TerminalSize{Rows: 0, Cols: 80}},
			want:   "process size rows and cols must be greater than 0",
		},
	}
	for _, tc := range cases {
		if err := tc.params.Validate(); !errors.Is(err, ErrInvalidFSRequest) {
			t.Fatalf("%s Validate() error = %v, want ErrInvalidFSRequest", tc.name, err)
		} else if err.Error() != tc.want {
			t.Fatalf("%s Validate() error = %q, want %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestProcessWriteAndCommandWriteValidateBase64(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte("input"))
	if err := (&ProcessWriteStdinParams{ProcessHandle: "p", DeltaBase64: &valid}).Validate(); err != nil {
		t.Fatalf("ProcessWriteStdinParams.Validate() error = %v", err)
	}
	if err := (&CommandExecWriteParams{ProcessID: "p", DeltaBase64: &valid}).Validate(); err != nil {
		t.Fatalf("CommandExecWriteParams.Validate() error = %v", err)
	}
	invalid := "not base64"
	if err := (&ProcessWriteStdinParams{ProcessHandle: "p", DeltaBase64: &invalid}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("ProcessWriteStdinParams invalid error = %v, want ErrInvalidFSRequest", err)
	} else if !strings.HasPrefix(err.Error(), "invalid deltaBase64:") {
		t.Fatalf("ProcessWriteStdinParams invalid error = %v", err)
	}
	if err := (&CommandExecWriteParams{ProcessID: "p", DeltaBase64: &invalid}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("CommandExecWriteParams invalid error = %v, want ErrInvalidFSRequest", err)
	} else if !strings.HasPrefix(err.Error(), "invalid deltaBase64:") {
		t.Fatalf("CommandExecWriteParams invalid error = %v", err)
	}
	if err := (&ProcessWriteStdinParams{ProcessHandle: "p"}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("ProcessWriteStdinParams missing delta error = %v, want ErrInvalidFSRequest", err)
	} else if err.Error() != "process/writeStdin requires deltaBase64 or closeStdin" {
		t.Fatalf("ProcessWriteStdinParams missing delta error = %v", err)
	}
	if err := (&CommandExecWriteParams{ProcessID: "p"}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("CommandExecWriteParams missing delta error = %v, want ErrInvalidFSRequest", err)
	} else if err.Error() != "command/exec/write requires deltaBase64 or closeStdin" {
		t.Fatalf("CommandExecWriteParams missing delta error = %v", err)
	}
}

func TestProcessKillAndResizeValidate(t *testing.T) {
	if err := (&ProcessKillParams{ProcessHandle: "p"}).Validate(); err != nil {
		t.Fatalf("ProcessKillParams.Validate() error = %v", err)
	}
	if err := (&ProcessResizePtyParams{ProcessHandle: "p", Size: TerminalSize{Rows: 40, Cols: 100}}).Validate(); err != nil {
		t.Fatalf("ProcessResizePtyParams.Validate() error = %v", err)
	}
	if err := (&ProcessResizePtyParams{ProcessHandle: "p", Size: TerminalSize{Rows: 0, Cols: 100}}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("bad resize error = %v, want ErrInvalidFSRequest", err)
	}
	if err := (&CommandExecTerminateParams{ProcessID: "p"}).Validate(); err != nil {
		t.Fatalf("CommandExecTerminateParams.Validate() error = %v", err)
	}
	if err := (&CommandExecResizeParams{ProcessID: "p", Size: TerminalSize{Rows: 24, Cols: 80}}).Validate(); err != nil {
		t.Fatalf("CommandExecResizeParams.Validate() error = %v", err)
	}
	if err := (&CommandExecResizeParams{ProcessID: "", Size: TerminalSize{Rows: 24, Cols: 80}}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("bad command resize error = %v, want ErrInvalidFSRequest", err)
	}
}

func TestTerminalSizeRejectsWindowsConPTYOverflow(t *testing.T) {
	if err := (&ProcessResizePtyParams{ProcessHandle: "p", Size: TerminalSize{Rows: 32768, Cols: 100}}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("bad process resize error = %v, want ErrInvalidFSRequest", err)
	} else if err.Error() != "process size rows and cols must be less than or equal to 32767" {
		t.Fatalf("bad process resize error = %q", err.Error())
	}
	if err := (&CommandExecResizeParams{ProcessID: "p", Size: TerminalSize{Rows: 24, Cols: 32768}}).Validate(); !errors.Is(err, ErrInvalidFSRequest) {
		t.Fatalf("bad command resize error = %v, want ErrInvalidFSRequest", err)
	} else if err.Error() != "command/exec size rows and cols must be less than or equal to 32767" {
		t.Fatalf("bad command resize error = %q", err.Error())
	}
}

func TestCommandExecParamsValidateNormalizesAndRejectsConflicts(t *testing.T) {
	processID := "proc-1"
	cwd := t.TempDir()
	params := &CommandExecParams{
		Command:   []string{"go", "version"},
		ProcessID: &processID,
		TTY:       true,
		CWD:       &cwd,
		Size:      &TerminalSize{Rows: 24, Cols: 80},
	}
	if err := params.Validate(""); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !params.StreamStdin || !params.StreamStdoutStderr {
		t.Fatalf("TTY did not enable streams: %+v", params)
	}

	outputCap := 64
	timeout := int64(1000)
	permissionProfile := "default"
	conflictCases := []CommandExecParams{
		{},
		{Command: []string{"go"}, StreamStdin: true},
		{Command: []string{"go"}, ProcessID: &processID, OutputBytesCap: &outputCap, DisableOutputCap: true},
		{Command: []string{"go"}, ProcessID: &processID, TimeoutMS: &timeout, DisableTimeout: true},
		{Command: []string{"go"}, ProcessID: &processID, SandboxPolicy: sandbox.NewReadOnlyPolicy(), PermissionProfile: &permissionProfile},
		{Command: []string{"go"}, ProcessID: &processID, Size: &TerminalSize{Rows: 24, Cols: 80}},
		{Command: []string{"go"}, ProcessID: &processID, CWD: stringPtr("relative")},
	}
	for i := range conflictCases {
		if err := conflictCases[i].Validate(filepath.VolumeName(cwd) + "relative"); !errors.Is(err, ErrInvalidFSRequest) && !errors.Is(err, ErrJSONRPCInvalidRequest) {
			t.Fatalf("case %d Validate() error = %v, want invalid request", i, err)
		}
	}
}

func TestCommandExecParamsValidateMessagesMatchRust(t *testing.T) {
	processID := "proc-1"
	outputCap := 1
	timeoutMS := int64(-1)
	positiveTimeout := int64(1000)
	permissionProfile := "default"
	cases := []struct {
		name   string
		params CommandExecParams
		want   string
	}{
		{name: "empty command", params: CommandExecParams{}, want: "command must not be empty"},
		{name: "streaming missing process id", params: CommandExecParams{Command: []string{"go"}, StreamStdin: true}, want: "command/exec tty or streaming requires a client-supplied processId"},
		{name: "size without tty", params: CommandExecParams{Command: []string{"go"}, ProcessID: &processID, Size: &TerminalSize{Rows: 24, Cols: 80}}, want: "command/exec size requires tty: true"},
		{name: "output cap conflict", params: CommandExecParams{Command: []string{"go"}, ProcessID: &processID, OutputBytesCap: &outputCap, DisableOutputCap: true}, want: "command/exec cannot set both outputBytesCap and disableOutputCap"},
		{name: "timeout conflict", params: CommandExecParams{Command: []string{"go"}, ProcessID: &processID, TimeoutMS: &positiveTimeout, DisableTimeout: true}, want: "command/exec cannot set both timeoutMs and disableTimeout"},
		{name: "negative timeout", params: CommandExecParams{Command: []string{"go"}, ProcessID: &processID, TimeoutMS: &timeoutMS}, want: "command/exec timeoutMs must be non-negative, got -1"},
		{name: "sandbox permission conflict", params: CommandExecParams{Command: []string{"go"}, ProcessID: &processID, SandboxPolicy: sandbox.NewReadOnlyPolicy(), PermissionProfile: &permissionProfile}, want: "`permissionProfile` cannot be combined with `sandboxPolicy`"},
		{name: "zero rows", params: CommandExecParams{Command: []string{"go"}, ProcessID: &processID, TTY: true, Size: &TerminalSize{Rows: 0, Cols: 80}}, want: "command/exec size rows and cols must be greater than 0"},
	}
	for _, tc := range cases {
		if err := tc.params.Validate(t.TempDir()); err == nil {
			t.Fatalf("%s Validate() returned nil error", tc.name)
		} else if err.Error() != tc.want {
			t.Fatalf("%s Validate() error = %q, want %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestCommandExecParamsJSONMatchesRustShape(t *testing.T) {
	processID := "pty-1"
	outputCap := 128
	timeoutMS := int64(1000)
	cwd := t.TempDir()
	params := &CommandExecParams{
		Command:            []string{"top"},
		ProcessID:          &processID,
		TTY:                true,
		StreamStdoutStderr: true,
		OutputBytesCap:     &outputCap,
		TimeoutMS:          &timeoutMS,
		CWD:                &cwd,
		Env: map[string]*string{
			"FOO": stringPtr("bar"),
			"BAZ": nil,
		},
		Size: &TerminalSize{Rows: 40, Cols: 120},
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var shape map[string]any
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if shape["processId"] != "pty-1" || shape["tty"] != true || shape["streamStdoutStderr"] != true {
		t.Fatalf("unexpected flags: %s", data)
	}
	if shape["disableTimeout"] != nil || shape["disableOutputCap"] != nil {
		t.Fatalf("false flags should be omitted: %s", data)
	}
	var decoded CommandExecParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("round-trip unmarshal error = %v", err)
	}
	if decoded.ProcessID == nil || *decoded.ProcessID != processID || decoded.Size == nil || decoded.Size.Rows != 40 || decoded.Size.Cols != 120 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Env["FOO"] == nil || *decoded.Env["FOO"] != "bar" || decoded.Env["BAZ"] != nil {
		t.Fatalf("env round-trip = %#v", decoded.Env)
	}
}

func TestProcessSpawnParamsJSONDistinguishesOmittedNullAndValueLimits(t *testing.T) {
	cwd := t.TempDir()
	baseJSON := []byte(`{"command":["sleep","30"],"processHandle":"sleep-1","cwd":` + quoteJSON(cwd) + `}`)
	var omitted ProcessSpawnParams
	if err := json.Unmarshal(baseJSON, &omitted); err != nil {
		t.Fatalf("unmarshal omitted limits: %v", err)
	}
	if omitted.OutputBytesCap != nil || omitted.TimeoutMS != nil {
		t.Fatalf("omitted limits = %+v", omitted)
	}

	var disabled ProcessSpawnParams
	if err := json.Unmarshal([]byte(`{"command":["sleep","30"],"processHandle":"sleep-1","cwd":`+quoteJSON(cwd)+`,"outputBytesCap":null,"timeoutMs":null}`), &disabled); err != nil {
		t.Fatalf("unmarshal disabled limits: %v", err)
	}
	if disabled.OutputBytesCap == nil || !disabled.OutputBytesCap.Set || disabled.OutputBytesCap.Value != nil {
		t.Fatalf("disabled output cap = %+v", disabled.OutputBytesCap)
	}
	if disabled.TimeoutMS == nil || !disabled.TimeoutMS.Set || disabled.TimeoutMS.Value != nil {
		t.Fatalf("disabled timeout = %+v", disabled.TimeoutMS)
	}

	var explicit ProcessSpawnParams
	if err := json.Unmarshal([]byte(`{"command":["sleep","30"],"processHandle":"sleep-1","cwd":`+quoteJSON(cwd)+`,"outputBytesCap":123,"timeoutMs":456}`), &explicit); err != nil {
		t.Fatalf("unmarshal explicit limits: %v", err)
	}
	if explicit.OutputBytesCap == nil || explicit.OutputBytesCap.Value == nil || *explicit.OutputBytesCap.Value != 123 {
		t.Fatalf("explicit output cap = %+v", explicit.OutputBytesCap)
	}
	if explicit.TimeoutMS == nil || explicit.TimeoutMS.Value == nil || *explicit.TimeoutMS.Value != 456 {
		t.Fatalf("explicit timeout = %+v", explicit.TimeoutMS)
	}
	if !reflect.DeepEqual(explicit.Command, []string{"sleep", "30"}) || explicit.ProcessHandle != "sleep-1" || explicit.CWD != cwd {
		t.Fatalf("explicit params = %+v", explicit)
	}
}

func stringPtr(value string) *string {
	return &value
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
