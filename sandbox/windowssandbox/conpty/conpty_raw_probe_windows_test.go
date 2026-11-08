//go:build windows

package conpty

import (
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// This file is the raw-syscall control experiment for the ConPTY content
// question (update/plan_2026_11_08_win.md §12-§17). It deliberately does not
// use golang.org/x/sys/windows or this package's own helpers: every call goes
// through kernel32 with hand-written structs, so a difference between this
// probe and conpty.Create/SpawnProcessAsUserWithToken can only come from the
// binding or from the launch flags, never from a shared helper.
//
// The matrix covers the three STARTUPINFO states that matter:
//
//	no-triple      PSEUDOCONSOLE only, no STARTF_USESTDHANDLES
//	triple         PSEUDOCONSOLE + STARTF_USESTDHANDLES with INVALID_HANDLE_VALUE
//	               (this is what SpawnProcessAsUserWithToken sends today)
//	triple+console PSEUDOCONSOLE + STARTF_USESTDHANDLES with real CONIN$/CONOUT$
//	               handles inherited from the parent session
//
// It is opt-in (CODEX_CONPTY_CONTENT_PROBE=1) because it depends on the host's
// console stack, and it asserts *content*: peeked bytes, the conhost VT
// initialisation sequence (?9001h?1004h) or EOF alone never satisfy it.

const (
	rawProcThreadAttributePseudoConsole = 0x00020016
	rawExtendedStartupInfoPresent       = 0x00080000
	rawStartfUseStdHandles              = 0x00000100
	rawGenericRead                      = 0x80000000
	rawGenericWrite                     = 0x40000000
	rawFileShareRead                    = 0x00000001
	rawFileShareWrite                   = 0x00000002
	rawOpenExisting                     = 3
	rawHandleFlagInherit                = 0x00000001
	rawWaitTimeout                      = 0x00000102
	rawContentMarker                    = "CONPTY_CONTENT_OK"
	rawProbeCommand                     = "cmd.exe /d /c echo " + rawContentMarker
)

var (
	rawKernel32             = syscall.NewLazyDLL("kernel32.dll")
	rawCreatePipe           = rawKernel32.NewProc("CreatePipe")
	rawCreatePseudoConsole  = rawKernel32.NewProc("CreatePseudoConsole")
	rawClosePseudoConsole   = rawKernel32.NewProc("ClosePseudoConsole")
	rawInitializeAttrList   = rawKernel32.NewProc("InitializeProcThreadAttributeList")
	rawUpdateAttribute      = rawKernel32.NewProc("UpdateProcThreadAttribute")
	rawDeleteAttrList       = rawKernel32.NewProc("DeleteProcThreadAttributeList")
	rawCreateProcessW       = rawKernel32.NewProc("CreateProcessW")
	rawCreateFileW          = rawKernel32.NewProc("CreateFileW")
	rawReadFile             = rawKernel32.NewProc("ReadFile")
	rawWaitForSingleObject  = rawKernel32.NewProc("WaitForSingleObject")
	rawGetExitCodeProcess   = rawKernel32.NewProc("GetExitCodeProcess")
	rawSetHandleInformation = rawKernel32.NewProc("SetHandleInformation")
	rawPeekNamedPipe        = rawKernel32.NewProc("PeekNamedPipe")
	rawCloseHandle          = rawKernel32.NewProc("CloseHandle")
)

type rawSecurityAttributes struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      int32
}

// rawStartupInfoEx mirrors STARTUPINFOEXW: STARTUPINFOW followed by the
// attribute-list pointer.
type rawStartupInfoEx struct {
	StartupInfo   syscall.StartupInfo
	AttributeList uintptr
}

type rawProbeState struct {
	name          string
	useStdHandles bool
	// validConsole selects inherited CONIN$/CONOUT$ handles instead of
	// INVALID_HANDLE_VALUE for the standard-handle triple.
	validConsole bool
	// inheritHandles mirrors bInheritHandles for CreateProcessW.
	inheritHandles bool
	// attrByValue passes the HPCON value itself as
	// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE's lpValue, the way the Microsoft
	// ConPTY sample does, instead of a pointer to it (what
	// windowssandbox.ProcThreadAttributeList.SetPseudoconsole sends).
	attrByValue bool
}

func rawProbeStates() []rawProbeState {
	return []rawProbeState{
		{name: "no-triple"},
		{name: "triple", useStdHandles: true},
		{name: "triple+console", useStdHandles: true, validConsole: true, inheritHandles: true},
		// Same launch flags as "triple" above, but the attribute carries the
		// HPCON value rather than its address.
		{name: "triple+attrvalue", useStdHandles: true, attrByValue: true},
	}
}

type rawProbeResult struct {
	State            string
	CreateErr        string
	PseudoConsoleErr string
	WaitMillis       int64
	WaitTimedOut     bool
	ExitCode         uint32
	ExitCodeKnown    bool
	BytesBeforeClose int
	BytesAfterClose  int
	PeekBeforeClose  string
	DrainEnd         string
	ContentMatched   bool
	Head             string
	Skipped          string
	Note             string
}

func (r rawProbeResult) String() string {
	if r.Skipped != "" {
		return fmt.Sprintf("%-15s skipped: %s", r.State, r.Skipped)
	}
	parts := []string{fmt.Sprintf("%-15s", r.State)}
	parts = append(parts, fmt.Sprintf("createErr=%q", r.CreateErr))
	parts = append(parts, fmt.Sprintf("pseudoErr=%q", r.PseudoConsoleErr))
	if r.WaitTimedOut {
		parts = append(parts, "wait=timeout")
	} else {
		parts = append(parts, fmt.Sprintf("wait=%dms", r.WaitMillis))
	}
	if r.ExitCodeKnown {
		parts = append(parts, fmt.Sprintf("exit=%d", r.ExitCode))
	}
	parts = append(parts, fmt.Sprintf("bytes(beforeClose)=%d", r.BytesBeforeClose))
	parts = append(parts, "peek="+r.PeekBeforeClose)
	parts = append(parts, fmt.Sprintf("bytes(afterClose)=%d", r.BytesAfterClose))
	parts = append(parts, fmt.Sprintf("drain=%s", r.DrainEnd))
	parts = append(parts, fmt.Sprintf("content=%v", r.ContentMatched))
	parts = append(parts, fmt.Sprintf("head=%s", r.Head))
	if r.Note != "" {
		parts = append(parts, "note="+r.Note)
	}
	return strings.Join(parts, " ")
}

// TestRawConPTYContentMatrix runs the three launch states with raw kernel32
// calls and records what reaches the pseudoconsole output pipe. Every cell is
// logged; the assertion is on content, so a silent "it spawned" never counts.
func TestRawConPTYContentMatrix(t *testing.T) {
	if os.Getenv("CODEX_CONPTY_CONTENT_PROBE") != "1" {
		t.Skip("set CODEX_CONPTY_CONTENT_PROBE=1 to run the raw ConPTY content matrix")
	}
	t.Logf("host: %s %s, pid %d, startupInfoEx size %d", runtime.GOOS, runtime.Version(), os.Getpid(), unsafe.Sizeof(rawStartupInfoEx{}))
	if unsafe.Sizeof(rawStartupInfoEx{}) != 112 {
		t.Fatalf("STARTUPINFOEXW size = %d on %s, want 112 (the probe struct must match the ABI)", unsafe.Sizeof(rawStartupInfoEx{}), runtime.GOARCH)
	}
	results := make([]rawProbeResult, 0, len(rawProbeStates()))
	for _, state := range rawProbeStates() {
		results = append(results, runRawProbeState(t, state))
	}
	var report []string
	for _, result := range results {
		report = append(report, result.String())
	}
	t.Logf("raw ConPTY content matrix:\n  %s", strings.Join(report, "\n  "))

	var matched []string
	for _, result := range results {
		if result.ContentMatched {
			matched = append(matched, result.State)
		}
	}
	if len(matched) == 0 {
		t.Fatalf("no launch state delivered %q to the pseudoconsole output pipe:\n  %s",
			rawContentMarker, strings.Join(report, "\n  "))
	}
	t.Logf("states that delivered content: %s", strings.Join(matched, ", "))
}

// rawTrace writes the probe's progress to stderr when CODEX_CONPTY_PROBE_TRACE=1.
// A hard crash inside a Win32 call would otherwise leave no breadcrumb.
func rawTrace(format string, args ...any) {
	if os.Getenv("CODEX_CONPTY_PROBE_TRACE") != "1" {
		return
	}
	fmt.Fprintf(os.Stderr, "[rawprobe] "+format+"\n", args...)
}

func runRawProbeState(t *testing.T, state rawProbeState) rawProbeResult {
	t.Helper()
	result := rawProbeResult{State: state.name}

	// Both pipe pairs are created inheritable and the client ends are then made
	// private, which is exactly what the Microsoft ConPTY sample does: the
	// console host side inherits the console ends, the caller keeps inWrite and
	// outRead to itself.
	sa := &rawSecurityAttributes{
		Length:        uint32(unsafe.Sizeof(rawSecurityAttributes{})),
		InheritHandle: 1,
	}
	var inputRead, inputWrite, outputRead, outputWrite syscall.Handle
	if err := rawCall("CreatePipe(input)", rawCreatePipe,
		uintptr(unsafe.Pointer(&inputRead)), uintptr(unsafe.Pointer(&inputWrite)),
		uintptr(unsafe.Pointer(sa)), 0); err != nil {
		result.Skipped = "CreatePipe(input): " + err.Error()
		return result
	}
	defer rawClose(inputWrite)
	defer rawClose(inputRead)
	if err := rawCall("CreatePipe(output)", rawCreatePipe,
		uintptr(unsafe.Pointer(&outputRead)), uintptr(unsafe.Pointer(&outputWrite)),
		uintptr(unsafe.Pointer(sa)), 0); err != nil {
		result.Skipped = "CreatePipe(output): " + err.Error()
		return result
	}
	if err := rawCall("SetHandleInformation(inWrite)", rawSetHandleInformation,
		uintptr(inputWrite), rawHandleFlagInherit, 0); err != nil {
		result.Skipped = "SetHandleInformation(inWrite): " + err.Error()
		rawClose(outputWrite)
		return result
	}
	if err := rawCall("SetHandleInformation(outRead)", rawSetHandleInformation,
		uintptr(outputRead), rawHandleFlagInherit, 0); err != nil {
		result.Skipped = "SetHandleInformation(outRead): " + err.Error()
		rawClose(outputWrite)
		return result
	}
	defer rawClose(outputRead)

	var pseudo syscall.Handle
	// COORD is two SHORTs packed into one 32-bit argument, the way
	// x/sys/windows passes it.
	coord := uintptr(uint32(uint16(defaultColumns)) | uint32(uint16(defaultRows))<<16)
	// CreatePseudoConsole is HRESULT-based: S_OK (0) is success and a failure
	// returns a non-zero HRESULT without touching GetLastError, so it cannot go
	// through rawCall's BOOL semantics.
	if err := rawCallHRESULT("CreatePseudoConsole", rawCreatePseudoConsole,
		coord, uintptr(inputRead), uintptr(outputWrite), 0,
		uintptr(unsafe.Pointer(&pseudo))); err != nil {
		result.PseudoConsoleErr = err.Error()
		rawClose(outputWrite)
		return result
	}
	// Exactly one ClosePseudoConsole per handle: a second close on the same
	// handle value can free an unrelated object once Windows reuses the number.
	closePseudoConsole := func() {
		if pseudo != 0 {
			rawClosePseudoConsole.Call(uintptr(pseudo))
			pseudo = 0
		}
	}
	defer closePseudoConsole()

	// Attribute list with exactly one entry: the pseudoconsole.
	var listSize uintptr
	rawInitializeAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&listSize)))
	if listSize == 0 {
		result.Skipped = "InitializeProcThreadAttributeList returned size 0"
		rawClose(outputWrite)
		return result
	}
	list := make([]byte, listSize)
	if err := rawCall("InitializeProcThreadAttributeList", rawInitializeAttrList,
		uintptr(unsafe.Pointer(&list[0])), 1, 0, uintptr(unsafe.Pointer(&listSize))); err != nil {
		result.Skipped = "InitializeProcThreadAttributeList: " + err.Error()
		rawClose(outputWrite)
		return result
	}
	defer rawDeleteAttrList.Call(uintptr(unsafe.Pointer(&list[0])))
	attrValue := uintptr(unsafe.Pointer(&pseudo))
	if state.attrByValue {
		attrValue = uintptr(pseudo)
	}
	if err := rawCall("UpdateProcThreadAttribute(PSEUDOCONSOLE)", rawUpdateAttribute,
		uintptr(unsafe.Pointer(&list[0])), 0, rawProcThreadAttributePseudoConsole,
		attrValue, unsafe.Sizeof(pseudo), 0, 0); err != nil {
		result.Skipped = "UpdateProcThreadAttribute: " + err.Error()
		rawClose(outputWrite)
		return result
	}

	startup := rawStartupInfoEx{}
	startup.StartupInfo.Cb = uint32(unsafe.Sizeof(rawStartupInfoEx{}))
	startup.AttributeList = uintptr(unsafe.Pointer(&list[0]))

	var consoleIn, consoleOut syscall.Handle
	if state.useStdHandles {
		startup.StartupInfo.Flags |= rawStartfUseStdHandles
		startup.StartupInfo.StdInput = syscall.InvalidHandle
		startup.StartupInfo.StdOutput = syscall.InvalidHandle
		startup.StartupInfo.StdErr = syscall.InvalidHandle
		if state.validConsole {
			inherit := &rawSecurityAttributes{
				Length:        uint32(unsafe.Sizeof(rawSecurityAttributes{})),
				InheritHandle: 1,
			}
			var err error
			consoleIn, err = rawOpenConsole("CONIN$", inherit)
			if err != nil {
				result.Skipped = "CreateFile(CONIN$): " + err.Error()
				rawClose(outputWrite)
				return result
			}
			defer rawClose(consoleIn)
			consoleOut, err = rawOpenConsole("CONOUT$", inherit)
			if err != nil {
				result.Skipped = "CreateFile(CONOUT$): " + err.Error()
				rawClose(outputWrite)
				return result
			}
			defer rawClose(consoleOut)
			startup.StartupInfo.StdInput = consoleIn
			startup.StartupInfo.StdOutput = consoleOut
			startup.StartupInfo.StdErr = consoleOut
		}
	}

	rawTrace("%s: pipes+pseudoconsole+attrs ready (pseudo=%#x, listSize=%d, useStdHandles=%v, validConsole=%v)",
		state.name, uintptr(pseudo), listSize, state.useStdHandles, state.validConsole)
	commandLine, err := syscall.UTF16FromString(rawProbeCommand)
	if err != nil {
		result.Skipped = "UTF16FromString: " + err.Error()
		rawClose(outputWrite)
		return result
	}
	var processInfo syscall.ProcessInformation
	var inheritHandle uintptr
	if state.inheritHandles {
		inheritHandle = 1
	}
	createErr := rawCall("CreateProcessW", rawCreateProcessW,
		0,
		uintptr(unsafe.Pointer(&commandLine[0])),
		0,
		0,
		inheritHandle,
		rawExtendedStartupInfoPresent,
		0, // lpEnvironment: inherit the parent's block
		0, // lpCurrentDirectory: inherit
		uintptr(unsafe.Pointer(&startup)),
		uintptr(unsafe.Pointer(&processInfo)),
	)
	rawTrace("%s: CreateProcessW -> err=%v pid=%d", state.name, createErr, processInfo.ProcessId)
	// The console holds its own reference to the write end once a client is
	// attached, so the parent's copy goes away here (Rust #45504).
	rawClose(outputWrite)
	if createErr != nil {
		result.CreateErr = createErr.Error()
		return result
	}
	defer rawClose(processInfo.Process)
	defer rawClose(processInfo.Thread)

	waitStart := time.Now()
	waitResult, _, _ := rawWaitForSingleObject.Call(uintptr(processInfo.Process), 5000)
	result.WaitMillis = time.Since(waitStart).Milliseconds()
	if uint32(waitResult) == rawWaitTimeout {
		result.WaitTimedOut = true
	}
	var exitCode uint32
	if r, _, _ := rawGetExitCodeProcess.Call(uintptr(processInfo.Process), uintptr(unsafe.Pointer(&exitCode))); r != 0 {
		result.ExitCode = exitCode
		result.ExitCodeKnown = true
	}
	// Give the console a moment to flush whatever it is going to flush before
	// we tear it down, so "content only appears after ClosePseudoConsole" is
	// distinguishable from "content arrives while the child runs".
	time.Sleep(300 * time.Millisecond)
	result.BytesBeforeClose, result.PeekBeforeClose = rawPeekAvailable(outputRead)
	rawTrace("%s: after wait, bytesBeforeClose=%d peekBeforeClose=%s",
		state.name, result.BytesBeforeClose, result.PeekBeforeClose)

	closePseudoConsole()
	rawTrace("%s: ClosePseudoConsole done, draining", state.name)
	data, end := rawDrainAvailable(outputRead)
	result.DrainEnd = end
	result.BytesAfterClose = len(data)
	rawTrace("%s: drain finished end=%s bytes=%d", state.name, result.DrainEnd, result.BytesAfterClose)
	if len(data) > 32 {
		result.Head = hex.EncodeToString(data[:32])
	} else if len(data) > 0 {
		result.Head = hex.EncodeToString(data)
	}
	result.ContentMatched = strings.Contains(string(data), rawContentMarker)
	return result
}

// rawCallHRESULT covers the COM-style Win32 entry points that report success as
// S_OK instead of a non-zero BOOL (CreatePseudoConsole, ReleasePseudoConsole).
func rawCallHRESULT(name string, proc *syscall.LazyProc, args ...uintptr) error {
	result, _, _ := proc.Call(args...)
	if hresult := int32(uint32(result)); hresult != 0 {
		return fmt.Errorf("%s failed with HRESULT 0x%08x", name, uint32(hresult))
	}
	return nil
}

func rawCall(name string, proc *syscall.LazyProc, args ...uintptr) error {
	result, _, callErr := proc.Call(args...)
	if result != 0 {
		return nil
	}
	if callErr == nil || callErr == syscall.Errno(0) {
		return fmt.Errorf("%s failed with a zero result and no error", name)
	}
	return callErr
}

func rawOpenConsole(name string, sa *rawSecurityAttributes) (syscall.Handle, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	handle, _, callErr := rawCreateFileW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		rawGenericRead|rawGenericWrite,
		rawFileShareRead|rawFileShareWrite,
		uintptr(unsafe.Pointer(sa)),
		rawOpenExisting,
		0,
		0,
	)
	if syscall.Handle(handle) == syscall.InvalidHandle || syscall.Handle(handle) == 0 {
		if callErr == nil {
			callErr = syscall.EINVAL
		}
		return 0, callErr
	}
	return syscall.Handle(handle), nil
}

func rawClose(handle syscall.Handle) {
	if handle == 0 || handle == syscall.InvalidHandle {
		return
	}
	rawCloseHandle.Call(uintptr(handle))
}

// rawPeekAvailable reports how many bytes are buffered on the pseudoconsole
// output pipe without blocking, plus a short description of the peek result.
// A blocking ReadFile on this pipe is not equivalent: it fails with
// ERROR_BROKEN_PIPE as soon as the console starts tearing down, which loses
// whatever the console flushed during that teardown.
func rawPeekAvailable(handle syscall.Handle) (int, string) {
	var available uint32
	ok, _, callErr := rawPeekNamedPipe.Call(
		uintptr(handle),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&available)),
		0,
	)
	if ok == 0 {
		return 0, "peek:" + rawDrainEnd(callErr)
	}
	return int(available), fmt.Sprintf("available=%d", available)
}

// rawDrainAvailable reads everything the console buffered, stopping when the
// pipe reports no more data or closes. It never blocks, so it observes the
// flush the console performs while it shuts down.
func rawDrainAvailable(handle syscall.Handle) ([]byte, string) {
	var out []byte
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		available, peek := rawPeekAvailable(handle)
		if available == 0 {
			if strings.HasPrefix(peek, "peek:") {
				return out, strings.TrimPrefix(peek, "peek:")
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		want := available
		if want > len(buffer) {
			want = len(buffer)
		}
		var read uint32
		ok, _, callErr := rawReadFile.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(want),
			uintptr(unsafe.Pointer(&read)),
			0,
		)
		if ok == 0 {
			return out, rawDrainEnd(callErr)
		}
		if read == 0 {
			return out, "no_data"
		}
		out = append(out, buffer[:read]...)
	}
	return out, "timeout"
}

// rawDrainEnd renders the terminating condition of a pipe read.
func rawDrainEnd(err error) string {
	if err == nil || err == syscall.Errno(0) {
		return "closed"
	}
	errno, ok := err.(syscall.Errno)
	if !ok {
		return err.Error()
	}
	switch errno {
	case 109: // ERROR_BROKEN_PIPE
		return "broken_pipe"
	case 232: // ERROR_NO_DATA
		return "no_data"
	case 997: // ERROR_IO_PENDING
		return "io_pending"
	default:
		return fmt.Sprintf("errno_%d", uint32(errno))
	}
}
