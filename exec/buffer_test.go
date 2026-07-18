package exec

import (
	"errors"
	"reflect"
	"testing"
)

func TestHeadTailBufferKeepsHeadAndTail(t *testing.T) {
	buffer := NewHeadTailBuffer(10)
	buffer.PushChunk([]byte("hello"))
	buffer.PushChunk([]byte(" middle "))
	buffer.PushChunk([]byte("world"))
	if got := string(buffer.Bytes()); got != "helloorld" {
		t.Fatalf("Bytes() = %q, want helloorld", got)
	}
	if buffer.RetainedBytes() != 9 {
		t.Fatalf("RetainedBytes() = %d, want 9", buffer.RetainedBytes())
	}
	if buffer.OmittedBytes() == 0 {
		t.Fatalf("OmittedBytes() = 0, want > 0")
	}
}

func TestHeadTailBufferZeroBudgetOmitsAll(t *testing.T) {
	buffer := NewHeadTailBuffer(0)
	buffer.PushChunk([]byte("hello"))
	if len(buffer.Bytes()) != 0 || buffer.OmittedBytes() != 5 {
		t.Fatalf("zero buffer bytes=%q omitted=%d", buffer.Bytes(), buffer.OmittedBytes())
	}
}

func TestHeadTailBufferDrainResets(t *testing.T) {
	buffer := NewHeadTailBuffer(10)
	buffer.PushChunk([]byte("hello"))
	chunks := buffer.DrainChunks()
	if len(chunks) != 1 || string(chunks[0]) != "hello" {
		t.Fatalf("DrainChunks() = %q", chunks)
	}
	if buffer.RetainedBytes() != 0 || buffer.OmittedBytes() != 0 {
		t.Fatalf("buffer not reset")
	}
}

func TestProcessStateTransitions(t *testing.T) {
	state := ProcessState{SandboxDenied: true}
	code := 2
	exited := state.Exited(&code)
	if !exited.HasExited || exited.ExitCode == nil || *exited.ExitCode != 2 || !exited.SandboxDenied {
		t.Fatalf("Exited() = %#v", exited)
	}
	failed := state.Failed("boom")
	if !failed.HasExited || failed.FailureMessage != "boom" || !failed.SandboxDenied {
		t.Fatalf("Failed() = %#v", failed)
	}
}

func TestStoreCreateLimitAndList(t *testing.T) {
	store := NewStore(1)
	first, err := store.Create([]string{"echo", "hi"}, "/repo", false)
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	if _, err := store.Create([]string{"echo", "hi"}, "/repo", false); !errors.Is(err, ErrProcessLimit) {
		t.Fatalf("Create(over limit) error = %v, want ErrProcessLimit", err)
	}
	if err := store.MarkExited(first.ID, 0); err != nil {
		t.Fatalf("MarkExited() error = %v", err)
	}
	second, err := store.Create([]string{"echo", "bye"}, "/repo", false)
	if err != nil {
		t.Fatalf("Create(after exit) error = %v", err)
	}
	if got := processIDs(store.List()); !reflect.DeepEqual(got, []int{first.ID, second.ID}) {
		t.Fatalf("List() = %v", got)
	}
}

func TestStoreAppendOutputAndClone(t *testing.T) {
	store := NewStore(2)
	entry, err := store.Create([]string{"echo"}, "/repo", false)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.AppendOutput(entry.ID, []byte("hello")); err != nil {
		t.Fatalf("AppendOutput() error = %v", err)
	}
	got, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got.Output.Bytes()) != "hello" {
		t.Fatalf("output = %q, want hello", got.Output.Bytes())
	}
	got.Output.PushChunk([]byte("mutated"))
	again, _ := store.Get(entry.ID)
	if string(again.Output.Bytes()) != "hello" {
		t.Fatalf("Get() did not clone output")
	}
}

func TestStoreWriteStdinRequiresTTY(t *testing.T) {
	store := NewStore(2)
	plain, err := store.Create([]string{"cat"}, "/repo", false)
	if err != nil {
		t.Fatalf("Create(plain) error = %v", err)
	}
	if err := store.WriteStdin(plain.ID, "hi"); !errors.Is(err, ErrStdinClosed) {
		t.Fatalf("WriteStdin(no tty) error = %v, want ErrStdinClosed", err)
	}
	tty, err := store.Create([]string{"cat"}, "/repo", true)
	if err != nil {
		t.Fatalf("Create(tty) error = %v", err)
	}
	if err := store.WriteStdin(tty.ID, "hi"); err != nil {
		t.Fatalf("WriteStdin(tty) error = %v", err)
	}
	got, _ := store.Get(tty.ID)
	if !reflect.DeepEqual(got.Stdin, []string{"hi"}) {
		t.Fatalf("stdin = %v, want [hi]", got.Stdin)
	}
}

func TestStoreErrors(t *testing.T) {
	store := NewStore(1)
	if _, err := store.Create(nil, "/repo", false); !errors.Is(err, ErrMissingCommand) {
		t.Fatalf("Create(empty) error = %v, want ErrMissingCommand", err)
	}
	if _, err := store.Get(42); !errors.Is(err, ErrUnknownProcessID) {
		t.Fatalf("Get(missing) error = %v, want ErrUnknownProcessID", err)
	}
}

func processIDs(entries []ProcessEntry) []int {
	out := make([]int, len(entries))
	for i := range entries {
		out[i] = entries[i].ID
	}
	return out
}
