//go:build windows

package conpty

import (
	"errors"
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

// Mirrors Rust #45504's user-visible effect: a successful spawn releases the
// pseudoconsole, so output readers see EOF once the last attached client exits
// instead of blocking while the session stays alive.
func TestSpawnedConsoleOutputReachesEOFAfterLastClientExits(t *testing.T) {
	if !ReleasePseudoConsoleAvailable() {
		t.Skip("this host does not export ReleasePseudoConsole")
	}
	instance, err := SpawnProcessAsUser([]string{"cmd", "/c", "exit 0"}, "")
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
	defer windows.CloseHandle(handle)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, peekErr := peekOutputPipeBytes(handle)
		if peekErr == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if errors.Is(peekErr, windows.ERROR_BROKEN_PIPE) {
			return
		}
		t.Fatalf("unexpected console output error: %v", peekErr)
	}
	t.Fatal("the console output pipe stayed open after the last client exited")
}
