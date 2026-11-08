//go:build windows

package conpty

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var peekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

// peekOutputPipeBytes reports how many bytes are buffered on the pipe, or the
// pipe error once the console has closed its output.
func peekOutputPipeBytes(handle windows.Handle) (uint32, error) {
	var available uint32
	result, _, callErr := peekNamedPipe.Call(
		uintptr(handle),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&available)),
		0,
	)
	if result == 0 {
		return 0, callErr
	}
	return available, nil
}

// Mirrors Rust #45504's post-spawn lifecycle: the creation handles only need to
// survive until a client has attached, and dropping them lets the console close
// its output once the last attached client exits.
func TestInstanceDropCreationHandlesClosesCreationPipes(t *testing.T) {
	instance, err := Create(defaultColumns, defaultRows)
	if err != nil {
		t.Skipf("this host cannot create a pseudoconsole: %v", err)
	}
	defer instance.Close()
	if instance.inputRead == 0 || instance.outputWrite == 0 {
		t.Fatalf("creation handles = %d/%d, want both set", instance.inputRead, instance.outputWrite)
	}
	inputWrite, outputRead := instance.inputWrite, instance.outputRead

	instance.dropCreationHandles()
	if instance.inputRead != 0 || instance.outputWrite != 0 {
		t.Fatalf("creation handles after the drop = %d/%d, want 0/0", instance.inputRead, instance.outputWrite)
	}
	// The client ends are untouched: callers still own them.
	if instance.inputWrite != inputWrite || instance.outputRead != outputRead {
		t.Fatalf("client handles changed: %d/%d, want %d/%d", instance.inputWrite, instance.outputRead, inputWrite, outputRead)
	}
	// Dropping twice is a no-op and Close still succeeds.
	instance.dropCreationHandles()
	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// ReleasePseudoConsole is the Windows 11 24H2+ API Rust calls after a successful
// spawn; when the host exposes it, releasing a real pseudoconsole must succeed.
func TestReleasePseudoConsoleOwnershipSucceedsWhenAvailable(t *testing.T) {
	if !ReleasePseudoConsoleAvailable() {
		t.Skip("this host does not export ReleasePseudoConsole")
	}
	instance, err := Create(defaultColumns, defaultRows)
	if err != nil {
		t.Skipf("this host cannot create a pseudoconsole: %v", err)
	}
	defer instance.Close()
	instance.dropCreationHandles()

	result, _, callErr := releasePseudoConsole.Call(instance.RawHandle())
	if callErr != nil && callErr != windows.ERROR_SUCCESS {
		t.Fatalf("ReleasePseudoConsole call error = %v", callErr)
	}
	if hresult := int32(uint32(result)); hresult != 0 {
		t.Fatalf("ReleasePseudoConsole HRESULT = %d, want S_OK", hresult)
	}
}

// A console EOF must come with console bytes behind it.
//
// This test replaced one documented as Rust #45504's "readers see EOF once the
// last client exits", which passed for the wrong reason: the
// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE attribute carried the HPCON's address
// instead of its value, so no client ever attached, ReleasePseudoConsole tore
// that never-attached console down during the spawn, and the pipe broke with
// zero bytes -- before the child had even exited. Once SetPseudoconsole sent
// the HPCON value (plan §17) the console really attaches and the old test
// started failing, which is the proof that its pass was an artifact.
//
// What distinguishes the two states on this host is the payload: an attached
// console emits its own initialisation/teardown sequences (ESC[?9001h, ESC[?25l,
// window title, ...) before EOF; a never-attached one emits nothing at all. So
// EOF alone is never evidence -- it has to carry console bytes.
func TestSpawnedConsoleOutputEOFCarriesConsoleBytes(t *testing.T) {
	instance, err := SpawnProcessAsUser([]string{"cmd", "/d", "/c", "exit 0"}, "")
	if err != nil {
		t.Skipf("this host cannot spawn a console process: %v", err)
	}
	defer instance.Close()
	// The creation handles are dropped after the spawn; only the client ends
	// remain for callers.
	if instance.inputRead != 0 || instance.outputWrite != 0 {
		t.Fatalf("creation handles after the spawn = %d/%d, want dropped", instance.inputRead, instance.outputWrite)
	}
	handle := windows.Handle(instance.TakeOutputRead())
	if handle == 0 {
		t.Fatal("spawn returned no output read handle")
	}
	output, readErr := readConsoleBytes(handle, 20*time.Second)
	if readErr != nil {
		t.Fatalf("reading console output: %v", readErr)
	}
	if len(output) == 0 {
		t.Fatal("the console output reached EOF with zero bytes: no client ever attached " +
			"(this is the signature of PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE carrying the HPCON address)")
	}
	t.Logf("console produced %d bytes before EOF: %q", len(output), output)
}
