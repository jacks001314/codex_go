package auth

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex_go/keyring"
)

type storageTelemetryCollector struct {
	mu     sync.Mutex
	events []StorageTelemetry
}

func (c *storageTelemetryCollector) record(event StorageTelemetry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *storageTelemetryCollector) snapshot() []StorageTelemetry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]StorageTelemetry(nil), c.events...)
}

func (c *storageTelemetryCollector) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = nil
}

func collectStorageTelemetry(t *testing.T) *storageTelemetryCollector {
	t.Helper()
	collector := &storageTelemetryCollector{}
	SetStorageTelemetryObserver(collector.record)
	t.Cleanup(func() { SetStorageTelemetryObserver(nil) })
	return collector
}

// failingKeyringStore is the deterministic secure-store failure seam; the
// wrapped error decides the classified secure_error tag.
type failingKeyringStore struct {
	err error
}

func (s failingKeyringStore) Load(string, string) (string, error) { return "", s.err }

func (s failingKeyringStore) Save(string, string, string) error { return s.err }

func (s failingKeyringStore) Delete(string, string) (bool, error) { return false, s.err }

func TestAutoStorageFallbackRecordsSecureFailureLikeRust(t *testing.T) {
	collector := collectStorageTelemetry(t)
	home := t.TempDir()
	store := NewStoreWithOptions(home, &StoreOptions{
		Mode:           AuthCredentialsStoreAuto,
		KeyringBackend: KeyringBackendSecrets,
		KeyringStore:   &KeyringStore{backend: KeyringBackendSecrets, osStore: failingKeyringStore{err: os.ErrPermission}},
	})
	if err := store.Save(FromAPIKey("sk-private-credential-value")); err != nil {
		t.Fatalf("auto save fallback failed: %v", err)
	}
	events := collector.snapshot()
	if len(events) != 1 {
		t.Fatalf("storage telemetry events = %#v", events)
	}
	event := events[0]
	if event.StoreMode != "auto" || event.SelectedStore != "secrets" || event.ActualStore != "file" ||
		event.Operation != "save" || event.Outcome != "success" || event.SecureOutcome != "error" ||
		event.SecureError != "access_denied" || event.FallbackReason != "secure_error" {
		t.Fatalf("auto save fallback event = %#v", event)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		t.Fatalf("auto save fallback did not write auth.json: %v", err)
	}
}

func TestAutoStorageLoadReportsMissingSecureEntryLikeRust(t *testing.T) {
	collector := collectStorageTelemetry(t)
	home := t.TempDir()
	if err := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile}).Save(FromAPIKey("sk-file-only")); err != nil {
		t.Fatalf("seed auth.json: %v", err)
	}
	collector.reset()
	store := NewStoreWithOptions(home, &StoreOptions{
		Mode:           AuthCredentialsStoreAuto,
		KeyringBackend: KeyringBackendSecrets,
		KeyringStore:   NewKeyringStore(KeyringBackendSecrets),
	})
	if _, err := store.Load(); err != nil {
		t.Fatalf("auto load from file failed: %v", err)
	}
	events := collector.snapshot()
	if len(events) != 1 {
		t.Fatalf("storage telemetry events = %#v", events)
	}
	event := events[0]
	if event.Operation != "load" || event.ActualStore != "file" || event.Outcome != "success" ||
		event.SecureOutcome != "not_found" || event.FallbackReason != "secure_entry_missing" || event.SecureError != "none" {
		t.Fatalf("auto load fallback event = %#v", event)
	}
}

func TestAutoStorageDeleteReportsMultipleStoreLikeRust(t *testing.T) {
	collector := collectStorageTelemetry(t)
	home := t.TempDir()
	keyringStore := NewKeyringStore(KeyringBackendDirect)
	store := NewStoreWithOptions(home, &StoreOptions{
		Mode:         AuthCredentialsStoreAuto,
		KeyringStore: keyringStore,
	})
	if err := store.Save(FromAPIKey("sk-auto-delete")); err != nil {
		t.Fatalf("auto save: %v", err)
	}
	collector.reset()
	if _, err := store.Delete(); err != nil {
		t.Fatalf("auto delete: %v", err)
	}
	events := collector.snapshot()
	if len(events) != 1 {
		t.Fatalf("delete telemetry events = %#v", events)
	}
	event := events[0]
	if event.Operation != "delete" || event.ActualStore != "multiple" || event.Outcome != "success" ||
		event.SecureOutcome != "not_attempted" || event.SecureError != "none" || event.FallbackReason != "none" {
		t.Fatalf("auto delete event = %#v", event)
	}
}

func TestKeyringSaveCleanupFailureKeepsSaveAndRecordsCleanupLikeRust(t *testing.T) {
	collector := collectStorageTelemetry(t)
	home := t.TempDir()
	authPath := filepath.Join(home, "auth.json")
	if err := os.MkdirAll(filepath.Join(authPath, "blocker"), 0o700); err != nil {
		t.Fatalf("seed blocking auth.json directory: %v", err)
	}
	store := NewStoreWithOptions(home, &StoreOptions{
		Mode:         AuthCredentialsStoreKeyring,
		KeyringStore: NewKeyringStore(KeyringBackendDirect),
	})
	if err := store.Save(FromAPIKey("sk-keyring-cleanup")); err != nil {
		t.Fatalf("keyring save must not fail on cleanup: %v", err)
	}
	events := collector.snapshot()
	if len(events) != 2 {
		t.Fatalf("storage telemetry events = %#v", events)
	}
	cleanup, save := events[0], events[1]
	if cleanup.Operation != "cleanup" || cleanup.ActualStore != "file" || cleanup.Outcome != "error" ||
		cleanup.SecureOutcome != "not_attempted" || cleanup.StoreMode != "keyring" {
		t.Fatalf("cleanup event = %#v", cleanup)
	}
	if save.Operation != "save" || save.ActualStore != "direct_keyring" || save.Outcome != "success" || save.SecureOutcome != "success" {
		t.Fatalf("save event = %#v", save)
	}
}

func TestCredentialStorageErrorsRedactPrivateValues(t *testing.T) {
	collectStorageTelemetry(t)
	t.Run("file", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"private-credential-value","auth_mode":`), 0o600); err != nil {
			t.Fatalf("seed malformed auth.json: %v", err)
		}
		_, err := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile}).Load()
		if err == nil {
			t.Fatal("malformed auth.json loaded without error")
		}
		message := err.Error()
		if !strings.Contains(message, "failed to load CLI auth from file") || !strings.Contains(message, "invalid credential JSON (Syntax at line 1 column ") {
			t.Fatalf("file storage error = %q", message)
		}
		if strings.Contains(message, "private-credential-value") {
			t.Fatalf("file storage error leaked credential data: %q", message)
		}
	})
	t.Run("keyring", func(t *testing.T) {
		home := t.TempDir()
		keyringStore := NewKeyringStore(KeyringBackendDirect)
		if err := keyringStore.Set(authKeyringService, authStoreKey(home), `{"OPENAI_API_KEY":"private-credential-value"`); err != nil {
			t.Fatalf("seed malformed keyring entry: %v", err)
		}
		_, err := NewStoreWithOptions(home, &StoreOptions{
			Mode:         AuthCredentialsStoreKeyring,
			KeyringStore: keyringStore,
		}).Load()
		if err == nil {
			t.Fatal("malformed keyring entry loaded without error")
		}
		message := err.Error()
		if !strings.Contains(message, "failed to deserialize CLI auth from keyring") || !strings.Contains(message, "invalid credential JSON (Syntax at line 1 column ") {
			t.Fatalf("keyring storage error = %q", message)
		}
		if strings.Contains(message, "private-credential-value") {
			t.Fatalf("keyring storage error leaked credential data: %q", message)
		}
	})
}

func TestStorageOriginatorIsBoundedAndPerStore(t *testing.T) {
	collector := collectStorageTelemetry(t)
	cases := []struct {
		originator string
		want       string
	}{
		{"", "none"},
		{"Codex Desktop", "codex_desktop"},
		{"codex_vscode", "codex_vscode"},
		{"private-client-name", "other"},
	}
	var wait sync.WaitGroup
	for _, testCase := range cases {
		wait.Add(1)
		go func(originator string) {
			defer wait.Done()
			store := NewStoreWithOptions(t.TempDir(), &StoreOptions{
				Mode:       AuthCredentialsStoreFile,
				Originator: originator,
			})
			if err := store.Save(FromAPIKey("sk-originator")); err != nil {
				t.Errorf("save with originator %q: %v", originator, err)
			}
		}(testCase.originator)
	}
	wait.Wait()
	events := collector.snapshot()
	if len(events) != len(cases) {
		t.Fatalf("storage telemetry events = %#v", events)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Originator]++
	}
	for _, testCase := range cases {
		if counts[testCase.want] != 1 {
			t.Fatalf("originator %q events = %#v (want one %q)", testCase.originator, counts, testCase.want)
		}
	}
	if _, ok := counts["private-client-name"]; ok {
		t.Fatalf("unbounded originator leaked into telemetry: %#v", counts)
	}
}

var _ keyring.Store = failingKeyringStore{}
