package appserverdaemon

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreserveStderrLogKeepsBoundedTailLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.stderr.log")
	contents := bytes.Repeat([]byte("x"), maxPreviousStderrLogBytes+4096)
	copy(contents[len(contents)-4:], "tail")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preserveStderrLog(path); err != nil {
		t.Fatalf("preserveStderrLog: %v", err)
	}
	previous, err := os.ReadFile(path + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	if len(previous) != maxPreviousStderrLogBytes {
		t.Fatalf("previous bytes = %d, want %d", len(previous), maxPreviousStderrLogBytes)
	}
	if !bytes.HasSuffix(previous, []byte("tail")) {
		t.Fatalf("previous tail = %q", previous[len(previous)-8:])
	}
	original, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, contents) {
		t.Fatalf("preserve modified the current log")
	}
}

func TestPreserveStderrLogSkipsMissingEmptyAndNonregularLikeRust(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.stderr.log")
	if err := preserveStderrLog(missing); err != nil {
		t.Fatalf("missing: %v", err)
	}
	empty := filepath.Join(dir, "empty.stderr.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preserveStderrLog(empty); err != nil {
		t.Fatalf("empty: %v", err)
	}
	nonregular := filepath.Join(dir, "directory.stderr.log")
	if err := os.Mkdir(nonregular, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := preserveStderrLog(nonregular); err != nil {
		t.Fatalf("nonregular: %v", err)
	}
	for _, path := range []string{missing, empty, nonregular} {
		if _, err := os.Stat(path + ".previous"); !os.IsNotExist(err) {
			t.Fatalf("%s produced a previous snapshot", path)
		}
	}
}

// resetStderrPruning restores the package-wide pause flag a handoff test may
// change, so later tests prune normally.
func resetStderrPruning(t *testing.T) {
	t.Helper()
	previous := stderrPruningPaused.Load()
	stderrPruningPaused.Store(false)
	t.Cleanup(func() { stderrPruningPaused.Store(previous) })
}

// Rust #55856: oversized records are dropped before copying and a full queue
// never blocks the writer.
func TestManagedStderrLogDropsOversizedAndQueuedRecordsLikeRust(t *testing.T) {
	writer := &managedStderrLogWriter{path: filepath.Join(t.TempDir(), "daemon.stderr.log"), queue: make(chan []byte, 2)}

	oversized := bytes.Repeat([]byte("x"), maxStderrLogRecordBytes+1)
	if n, err := writer.Write(oversized); n != len(oversized) || err != nil {
		t.Fatalf("oversized Write = %d, %v; want the length and no error", n, err)
	}
	if got := len(writer.queue); got != 0 {
		t.Fatalf("oversized record queued %d entries, want 0", got)
	}

	for _, record := range []string{"first", "second", "dropped"} {
		if _, err := writer.Write([]byte(record)); err != nil {
			t.Fatalf("Write(%q): %v", record, err)
		}
	}
	if got := len(writer.queue); got != 2 {
		t.Fatalf("queue length = %d, want 2", got)
	}
	if first := string(<-writer.queue); first != "first" {
		t.Fatalf("first queued record = %q", first)
	}
	if second := string(<-writer.queue); second != "second" {
		t.Fatalf("second queued record = %q", second)
	}
}

// Rust #55856: a live log above 1 MiB keeps its latest 256 KiB in
// `.log.previous` before the current file is truncated in place.
func TestPruneCurrentStderrLogKeepsBoundedTailLikeRust(t *testing.T) {
	resetStderrPruning(t)
	path := filepath.Join(t.TempDir(), "daemon.stderr.log")
	contents := bytes.Repeat([]byte("x"), maxCurrentStderrLogBytes+4096)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	pruneCurrentStderrLog(path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("current log size = %d, want 0 after pruning", info.Size())
	}
	previous, err := os.ReadFile(path + ".previous")
	if err != nil {
		t.Fatalf("read previous log: %v", err)
	}
	if len(previous) != maxPreviousStderrLogBytes {
		t.Fatalf("previous log bytes = %d, want %d", len(previous), maxPreviousStderrLogBytes)
	}
	if !bytes.Equal(previous, contents[len(contents)-maxPreviousStderrLogBytes:]) {
		t.Fatalf("previous log does not hold the latest %d bytes", maxPreviousStderrLogBytes)
	}
	if _, err := os.Stat(path + ".previous.tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary previous log was left behind: %v", err)
	}
}

func TestPruneCurrentStderrLogSkipsSmallMissingAndNonregularLikeRust(t *testing.T) {
	resetStderrPruning(t)
	dir := t.TempDir()
	small := filepath.Join(dir, "small.log")
	if err := os.WriteFile(small, []byte("recent lines"), 0o600); err != nil {
		t.Fatal(err)
	}
	pruneCurrentStderrLog(small)
	if info, err := os.Stat(small); err != nil || info.Size() == 0 {
		t.Fatalf("small log changed: %v", err)
	}

	pruneCurrentStderrLog(filepath.Join(dir, "missing.log"))
	if _, err := os.Stat(filepath.Join(dir, "missing.log")); !os.IsNotExist(err) {
		t.Fatalf("pruning created a missing log: %v", err)
	}

	directory := filepath.Join(dir, "not-a-file")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	pruneCurrentStderrLog(directory)
}

// Rust #55856: pruning stays out of the truncated shared log for the whole
// updater handoff, and only a failed handoff that restored ownership re-enables
// it.
func TestStderrPruningPausesDuringUpdaterHandoffLikeRust(t *testing.T) {
	resetStderrPruning(t)
	path := filepath.Join(t.TempDir(), "daemon.stderr.log")
	contents := bytes.Repeat([]byte("x"), maxCurrentStderrLogBytes+4096)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	resume, err := pauseStderrPruningForHandoff()
	if err != nil {
		t.Fatalf("pauseStderrPruningForHandoff: %v", err)
	}
	pruneCurrentStderrLog(path)
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(contents)) {
		t.Fatalf("pruning ran while the handoff held the lock: %v", err)
	}
	resume(true)
	pruneCurrentStderrLog(path)
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("restored ownership did not re-enable pruning: %v", err)
	}

	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	resume, err = pauseStderrPruningForHandoff()
	if err != nil {
		t.Fatalf("second pauseStderrPruningForHandoff: %v", err)
	}
	// A successful handoff leaves retention to the successor.
	resume(false)
	pruneCurrentStderrLog(path)
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(contents)) {
		t.Fatalf("a successful handoff must leave predecessor pruning off: %v", err)
	}
}

// Rust #55856: the worker writes through the bounded queue and prunes the live
// file between records once it exceeds 1 MiB.
func TestManagedStderrLogPrunesLiveWritesLikeRust(t *testing.T) {
	resetStderrPruning(t)
	path := filepath.Join(t.TempDir(), "daemon.stderr.log")
	writer := newManagedStderrLogWriter(path)
	writer.enableAppend = func() error { return nil }
	go writer.run()
	t.Cleanup(writer.stop)
	record := append(bytes.Repeat([]byte("y"), maxStderrLogRecordBytes-1), '\n')
	// Pace the producer so the bounded queue keeps up; the queue is allowed to
	// drop records, so a tight loop could stay under the prune threshold.
	for written := 0; written <= 2*maxCurrentStderrLogBytes; written += len(record) {
		if _, err := writer.Write(record); err != nil {
			t.Fatalf("Write: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(writer.queue) > 0 {
			if time.Now().After(deadline) {
				t.Fatalf("bounded stderr queue never drained")
			}
			time.Sleep(time.Millisecond)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		previous, err := os.Stat(path + ".previous")
		if err == nil && previous.Size() <= maxPreviousStderrLogBytes {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live stderr log was not pruned: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat current log: %v", err)
	}
	if info.Size() > maxCurrentStderrLogBytes+int64(len(record)) {
		t.Fatalf("current log size = %d, want at most %d", info.Size(), maxCurrentStderrLogBytes+int64(len(record)))
	}
	previous, err := os.Stat(path + ".previous")
	if err != nil {
		t.Fatalf("previous log was not preserved: %v", err)
	}
	if previous.Size() > maxPreviousStderrLogBytes {
		t.Fatalf("previous log size = %d, want at most %d", previous.Size(), maxPreviousStderrLogBytes)
	}
}

// Rust #55856: the managed writer falls back to process stderr until the
// daemon child starts it, and an unmanaged process has no pid file to resolve.
func TestStderrWriterFallsBackToProcessStderrLikeRust(t *testing.T) {
	if managedStderrLog.Load() != nil {
		t.Skip("the managed stderr writer is already active in this process")
	}
	if StderrWriter() != io.Writer(os.Stderr) {
		t.Fatalf("StderrWriter() did not fall back to process stderr")
	}
	t.Setenv(UpdaterPIDFileEnv, "")
	if err := MaintainStderrLog(); err == nil {
		t.Fatalf("MaintainStderrLog() without a managed pid file must fail")
	}
}
