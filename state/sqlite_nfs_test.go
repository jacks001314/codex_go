package state

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// nfsProbeStub is a filesystem probe whose answer and failure mode can be
// changed between calls, so the shared-probe contract can be driven on every
// platform (a real NFS mount only exists on Linux).
type nfsProbeStub struct {
	mu    sync.Mutex
	calls int
	paths []string
	value bool
	err   error
}

func (s *nfsProbeStub) probe(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.paths = append(s.paths, path)
	if s.err != nil {
		return false, s.err
	}
	return s.value, nil
}

func (s *nfsProbeStub) set(value bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
	s.err = err
}

func (s *nfsProbeStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Mirrors Rust sqlite_home_filesystem_probe_is_shared_by_clones_until_config_
// replacement (codex-rs/state/src/sqlite_tests.rs, #54417 `9c84e89ce0`): a copy
// of a config reuses the probe the original already ran, while a freshly
// constructed config probes the home again.
func TestSqliteConfigFilesystemProbeIsSharedByCopiesLikeRust(t *testing.T) {
	home := t.TempDir()
	stub := &nfsProbeStub{value: false}
	config, err := sqliteConfigWithFilesystemProbe(home, stub.probe)
	if err != nil {
		t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
	}
	clone := config
	isNFS, err := config.IsNFS()
	if err != nil {
		t.Fatalf("IsNFS: %v", err)
	}
	if isNFS {
		t.Fatal("the stubbed home must start off NFS")
	}
	// The clone must reuse the original probe: a fresh lookup would now report a
	// different answer.
	stub.set(true, nil)
	if isNFS, err = clone.IsNFS(); err != nil {
		t.Fatalf("clone IsNFS: %v", err)
	}
	if isNFS {
		t.Fatal("the clone re-probed the home instead of reusing the shared probe")
	}
	if calls := stub.callCount(); calls != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 for the shared probe", calls)
	}
	// A new config for the same home probes independently.
	fresh, err := sqliteConfigWithFilesystemProbe(home, stub.probe)
	if err != nil {
		t.Fatalf("fresh sqliteConfigWithFilesystemProbe: %v", err)
	}
	if isNFS, err = fresh.IsNFS(); err != nil {
		t.Fatalf("fresh IsNFS: %v", err)
	}
	if !isNFS {
		t.Fatal("a new config must probe the home again and observe the new answer")
	}
	if calls := stub.callCount(); calls != 2 {
		t.Fatalf("probe calls = %d, want 2 after a replacement config", calls)
	}
}

// Mirrors Rust failed_sqlite_home_filesystem_probe_recovers_after_home_creation
// (#54417 `9c84e89ce0`): a failed probe is not cached, so the same config
// succeeds once the home exists. The Linux statfs probe reports the missing
// directory itself; here the probe's error stands in for it.
func TestFailedSqliteConfigFilesystemProbeRetriesLikeRust(t *testing.T) {
	home := t.TempDir()
	stub := &nfsProbeStub{err: errors.New("statfs: no such file or directory")}
	config, err := sqliteConfigWithFilesystemProbe(home, stub.probe)
	if err != nil {
		t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
	}
	if _, err := config.IsNFS(); err == nil {
		t.Fatal("the first probe must surface its error")
	}
	stub.set(false, nil)
	isNFS, err := config.IsNFS()
	if err != nil {
		t.Fatalf("IsNFS after the home appeared: %v", err)
	}
	if isNFS {
		t.Fatal("the retried probe must report the stubbed answer")
	}
	if calls := stub.callCount(); calls != 2 {
		t.Fatalf("probe calls = %d, want 2 because a failed probe is retried", calls)
	}
}

// Mirrors the NFS branch of Rust interrupted_reclamation_releases_writer_and_
// resumes_without_data_loss (#54417 `9c84e89ce0`): on an NFS home a pass
// reports no reclaimed pages and leaves the file and the freelist untouched, so
// no maintenance connection is opened. The control run with the same fixture and
// a non-NFS probe still reclaims, which is what makes the skip observable.
func TestReclaimRuntimeDBSkipsNFSHomeLikeRust(t *testing.T) {
	ctx := context.Background()
	config, db := reclamationLogsDB(t, 4096)
	path := config.LogsDBPath()
	options := reclamationOptions{
		budget:     reclamationBudget{deadline: time.Now().Add(30 * time.Second), pages: reclamationBatchPages},
		batchPages: reclamationBatchPages,
	}

	stub := &nfsProbeStub{value: true}
	nfsConfig, err := sqliteConfigWithFilesystemProbe(config.Home(), stub.probe)
	if err != nil {
		t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
	}
	bytesBefore := int64(len(mustReadFile(t, path)))
	freeBefore := reclamationQueryInt(t, db, `PRAGMA freelist_count`)
	skipped := reclaimRuntimeDB(nfsConfig, ctx, reclamationTarget{label: "log DB", path: path}, options, nil)
	if skipped.err != nil {
		t.Fatalf("reclaim on an NFS home: %v", skipped.err)
	}
	if skipped.pass.pages != 0 || skipped.pass.outcome != reclamationOutcomeIdle {
		t.Fatalf("NFS pass = (%d pages, %v), want (0 pages, idle)", skipped.pass.pages, skipped.pass.outcome)
	}
	if calls := stub.callCount(); calls != 1 {
		t.Fatalf("filesystem probe calls = %d, want 1", calls)
	}
	if freeAfter := reclamationQueryInt(t, db, `PRAGMA freelist_count`); freeAfter != freeBefore {
		t.Fatalf("NFS pass changed the freelist: %d -> %d", freeBefore, freeAfter)
	}
	if bytesAfter := int64(len(mustReadFile(t, path))); bytesAfter != bytesBefore {
		t.Fatalf("NFS pass changed the database file: %d -> %d bytes", bytesBefore, bytesAfter)
	}

	// Control: the same fixture with a non-NFS probe reclaims pages.
	control, err := sqliteConfigWithFilesystemProbe(config.Home(), (&nfsProbeStub{}).probe)
	if err != nil {
		t.Fatalf("control sqliteConfigWithFilesystemProbe: %v", err)
	}
	reclaimed := reclaimRuntimeDB(control, ctx, reclamationTarget{label: "log DB", path: path}, options, nil)
	if reclaimed.err != nil {
		t.Fatalf("control reclaim: %v", reclaimed.err)
	}
	if reclaimed.pass.pages == 0 {
		t.Fatal("the control pass on a local home must reclaim pages")
	}
}
