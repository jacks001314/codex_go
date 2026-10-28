package codemode

import "testing"

func TestCellStoreCompleteDoesNotOverwriteTerminationLikeRust(t *testing.T) {
	store := NewCellStore()
	if _, err := store.Start("cell", "text(1)"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := store.Terminate("cell"); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	cell, completed, err := store.CompleteIfRunning("cell", "late output", nil)
	if err != nil {
		t.Fatalf("CompleteIfRunning() error = %v", err)
	}
	if completed {
		t.Fatal("terminated cell was completed")
	}
	if cell.Status != CellTerminated {
		t.Fatalf("status = %v, want terminated", cell.Status)
	}
	if cell.Output != "late output" {
		t.Fatalf("output = %q, want the late output on the terminated cell", cell.Output)
	}
	if _, ok := cell.ResultBufferedFor(store.Now()); ok {
		t.Fatal("terminated cell reported a buffered result")
	}
	if _, err := store.Complete("cell", "", nil); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	got, ok := store.Get("cell")
	if !ok || got.Status != CellTerminated {
		t.Fatalf("Complete() overwrote the termination: %#v", got)
	}
}

func TestCellStoreCompleteIfRunningCompletesRunningCellLikeRust(t *testing.T) {
	store := NewCellStore()
	if _, err := store.Start("cell", "text(1)"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	cell, completed, err := store.CompleteIfRunning("cell", "done", nil)
	if err != nil {
		t.Fatalf("CompleteIfRunning() error = %v", err)
	}
	if !completed {
		t.Fatal("running cell was not completed")
	}
	if cell.Status != CellCompleted || cell.Output != "done" {
		t.Fatalf("completed cell = %#v", cell)
	}
	if _, ok := cell.ResultBufferedFor(store.Now()); !ok {
		t.Fatal("completed cell did not report a result")
	}
}
