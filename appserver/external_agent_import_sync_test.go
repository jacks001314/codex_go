package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
)

type externalAgentImportSyncFixture struct {
	t          *testing.T
	home       string
	repo       string
	store      *session.Store
	router     *RuntimeRouter
	sourcePath string
}

func newExternalAgentImportSyncFixture(t *testing.T) *externalAgentImportSyncFixture {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := &externalAgentImportSyncFixture{
		t:          t,
		home:       home,
		repo:       repo,
		store:      session.NewStore(filepath.Join(home, "sessions-store")),
		sourcePath: filepath.Join(home, "source.jsonl"),
	}
	fixture.router = NewDefaultRuntimeRouter(fixture.store, home)
	fixture.writeSource(fixture.sourcePath, [][2]string{{"user", "original external message"}})
	return fixture
}

func (f *externalAgentImportSyncFixture) writeSource(path string, messages [][2]string) {
	f.t.Helper()
	var raw []byte
	for index, message := range messages {
		row, err := json.Marshal(map[string]any{
			"type":      message[0],
			"cwd":       f.repo,
			"timestamp": time.Date(2026, 8, 1, 1, index, 0, 0, time.UTC).Format(time.RFC3339),
			"message":   map[string]any{"content": message[1]},
		})
		if err != nil {
			f.t.Fatal(err)
		}
		raw = append(raw, row...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *externalAgentImportSyncFixture) appendSource(path string, role string, text string) {
	f.t.Helper()
	row, err := json.Marshal(map[string]any{
		"type": role, "cwd": f.repo, "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"content": text},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := file.Write(append(row, '\n')); err != nil {
		_ = file.Close()
		f.t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *externalAgentImportSyncFixture) importPaths(paths ...string) config.ExternalAgentConfigImportTypeResult {
	f.t.Helper()
	details := config.NewMigrationDetails()
	for _, path := range paths {
		details.Sessions = append(details.Sessions, config.SessionMigration{Path: path, CWD: f.repo})
	}
	results := f.router.importExternalAgentSessions(&config.ExternalAgentConfigImportParams{
		MigrationItems: []config.ExternalAgentConfigMigrationItem{{ItemType: config.MigrationSessions, Details: details}},
	})
	if len(results) != 1 {
		f.t.Fatalf("session import results = %#v", results)
	}
	return results[0]
}

func (f *externalAgentImportSyncFixture) importOne(path string) string {
	f.t.Helper()
	result := f.importPaths(path)
	if len(result.Failures) != 0 || len(result.Successes) != 1 || result.Successes[0].Target == nil {
		f.t.Fatalf("session import result = %#v", result)
	}
	return *result.Successes[0].Target
}

func (f *externalAgentImportSyncFixture) assertDeferred(path string, ledgerBefore []byte) {
	f.t.Helper()
	result := f.importPaths(path)
	if len(result.Successes) != 0 || len(result.Failures) != 0 {
		f.t.Fatalf("deferred import result = %#v", result)
	}
	ledgerAfter, err := os.ReadFile(filepath.Join(f.home, externalSessionImportLedgerFileForTest))
	if err != nil {
		f.t.Fatal(err)
	}
	if string(ledgerAfter) != string(ledgerBefore) {
		f.t.Fatalf("deferred import changed ledger\nbefore=%s\nafter=%s", ledgerBefore, ledgerAfter)
	}
}

func (f *externalAgentImportSyncFixture) record(threadID string) *session.Record {
	f.t.Helper()
	record, err := f.store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		f.t.Fatal(err)
	}
	return record
}

func assertExternalTranscript(t *testing.T, record *session.Record, expected [][2]string) {
	t.Helper()
	var actual [][2]string
	markers := 0
	for _, item := range record.Items {
		if item.Type == externalSessionImportedMarkerType && item.Text == externalSessionImportedMarker {
			markers++
			continue
		}
		actual = append(actual, [2]string{item.Role, item.Text})
	}
	if len(actual) != len(expected) {
		t.Fatalf("transcript = %#v, want %#v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("transcript = %#v, want %#v", actual, expected)
		}
	}
	if markers != 1 {
		t.Fatalf("import marker count = %d, want 1", markers)
	}
}

const externalSessionImportLedgerFileForTest = "external_agent_session_imports.json"

func TestExternalAgentImportSyncColdExactPrefixAppendsSuffixToSameThreadAndCheckpoints(t *testing.T) {
	fixture := newExternalAgentImportSyncFixture(t)
	original := fixture.importOne(fixture.sourcePath)
	fixture.appendSource(fixture.sourcePath, "assistant", "late external assistant reply")

	if appended := fixture.importOne(fixture.sourcePath); appended != original {
		t.Fatalf("appended target = %q, want %q", appended, original)
	}
	page, err := fixture.store.List(session.ListOptions{PageSize: 10, IncludeHistory: true})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("threads = %+v err=%v", page, err)
	}
	assertExternalTranscript(t, fixture.record(original), [][2]string{
		{"user", "original external message"},
		{"assistant", "late external assistant reply"},
	})
	_, currentHash, err := config.ExternalSessionContentSHA256(fixture.sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := config.FindExternalSessionImport(fixture.home, fixture.sourcePath)
	if err != nil || !mapping.Found || mapping.Ambiguous || mapping.ImportedThreadID != original || mapping.SourceContentSHA256 != currentHash {
		t.Fatalf("checkpoint mapping = %#v err=%v", mapping, err)
	}
}

func TestExternalAgentImportSyncConcurrentChangedSessionsCheckpointWithoutLostUpdate(t *testing.T) {
	fixture := newExternalAgentImportSyncFixture(t)
	secondPath := filepath.Join(fixture.home, "second.jsonl")
	fixture.writeSource(secondPath, [][2]string{{"user", "second original external message"}})
	initial := fixture.importPaths(fixture.sourcePath, secondPath)
	if len(initial.Failures) != 0 || len(initial.Successes) != 2 {
		t.Fatalf("initial imports = %#v", initial)
	}
	wantTargets := []string{*initial.Successes[0].Target, *initial.Successes[1].Target}
	sort.Strings(wantTargets)
	fixture.appendSource(fixture.sourcePath, "assistant", "late external assistant reply")
	fixture.appendSource(secondPath, "assistant", "second late external assistant reply")

	var wg sync.WaitGroup
	var mu sync.Mutex
	var targets []string
	for _, path := range []string{fixture.sourcePath, secondPath} {
		path := path
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := fixture.importPaths(path)
			if len(result.Failures) != 0 || len(result.Successes) != 1 || result.Successes[0].Target == nil {
				fixture.t.Errorf("changed import %s = %#v", path, result)
				return
			}
			mu.Lock()
			targets = append(targets, *result.Successes[0].Target)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Strings(targets)
	if len(targets) != 2 || targets[0] != wantTargets[0] || targets[1] != wantTargets[1] {
		t.Fatalf("appended targets = %#v, want %#v", targets, wantTargets)
	}
	for _, path := range []string{fixture.sourcePath, secondPath} {
		_, currentHash, err := config.ExternalSessionContentSHA256(path)
		if err != nil {
			t.Fatal(err)
		}
		mapping, err := config.FindExternalSessionImport(fixture.home, path)
		if err != nil || mapping.SourceContentSHA256 != currentHash {
			t.Fatalf("mapping for %s = %#v err=%v", path, mapping, err)
		}
	}
}

func TestExternalAgentImportSyncActiveTargetIsDeferredWithoutCheckpoint(t *testing.T) {
	fixture := newExternalAgentImportSyncFixture(t)
	original := fixture.importOne(fixture.sourcePath)
	ledgerBefore, err := os.ReadFile(filepath.Join(fixture.home, externalSessionImportLedgerFileForTest))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.router.services.ThreadRouter.retainLiveThread(fixture.record(original)); err != nil {
		t.Fatal(err)
	}
	defer fixture.router.services.ThreadRouter.releaseLiveThreads([]session.ThreadID{session.ThreadID(original)})
	fixture.router.requireThreadStatus().UpsertThread(original, false)
	fixture.appendSource(fixture.sourcePath, "user", "later external user message")

	fixture.assertDeferred(fixture.sourcePath, ledgerBefore)
	assertExternalTranscript(t, fixture.record(original), [][2]string{{"user", "original external message"}})
}

func TestExternalAgentImportSyncColdDivergedTargetIsDeferredWithoutCheckpoint(t *testing.T) {
	fixture := newExternalAgentImportSyncFixture(t)
	original := fixture.importOne(fixture.sourcePath)
	native := []session.Item{
		{ID: "native-user", Type: "user_message", Role: "user", Text: "native Codex message", CreatedAt: time.Now().UTC()},
		{ID: "native-assistant", Type: "assistant_message", Role: "assistant", Text: "native Codex answer", CreatedAt: time.Now().UTC()},
	}
	if _, err := fixture.store.AppendItems(session.ThreadID(original), native); err != nil {
		t.Fatal(err)
	}
	ledgerBefore, err := os.ReadFile(filepath.Join(fixture.home, externalSessionImportLedgerFileForTest))
	if err != nil {
		t.Fatal(err)
	}
	fixture.appendSource(fixture.sourcePath, "user", "later external user message")

	fixture.assertDeferred(fixture.sourcePath, ledgerBefore)
	assertExternalTranscript(t, fixture.record(original), [][2]string{
		{"user", "original external message"},
		{"user", "native Codex message"},
		{"assistant", "native Codex answer"},
	})
}

func TestExternalAgentImportSyncChangedHashWithEqualTranscriptIsDeferredWithoutCheckpoint(t *testing.T) {
	fixture := newExternalAgentImportSyncFixture(t)
	original := fixture.importOne(fixture.sourcePath)
	ledgerBefore, err := os.ReadFile(filepath.Join(fixture.home, externalSessionImportLedgerFileForTest))
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(fixture.sourcePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	fixture.assertDeferred(fixture.sourcePath, ledgerBefore)
	assertExternalTranscript(t, fixture.record(original), [][2]string{{"user", "original external message"}})
}

func TestExternalAgentImportSyncAmbiguousLegacyTargetsAreDeferredWithoutCheckpoint(t *testing.T) {
	fixture := newExternalAgentImportSyncFixture(t)
	original := fixture.importOne(fixture.sourcePath)
	ledgerPath := filepath.Join(fixture.home, externalSessionImportLedgerFileForTest)
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	duplicate := make(map[string]any, len(ledger.Records[0]))
	for key, value := range ledger.Records[0] {
		duplicate[key] = value
	}
	duplicate["imported_thread_id"] = "01800000-0001-7000-8000-000000000001"
	ledger.Records = append(ledger.Records, duplicate)
	ambiguous, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	ambiguous = append(ambiguous, '\n')
	if err := os.WriteFile(ledgerPath, ambiguous, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.appendSource(fixture.sourcePath, "user", "later external user message")
	ledgerBefore, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	fixture.assertDeferred(fixture.sourcePath, ledgerBefore)
	assertExternalTranscript(t, fixture.record(original), [][2]string{{"user", "original external message"}})
}
