package remotecontrol

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistedRemoteControlEnrollmentRoundTripsByTargetAndAccount(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	firstTarget := mustTarget(t, "https://chatgpt.com/remote/control")
	secondTarget := mustTarget(t, "https://api.chatgpt-staging.com/other/control")
	clientName := "desktop-client"
	enabled := false
	firstEnrollment := &Enrollment{
		RemoteControlTarget: firstTarget,
		AccountID:           "account-a",
		EnvironmentID:       "env_first",
		ServerID:            "srv_e_first",
		ServerName:          "first-server",
	}
	secondEnrollment := &Enrollment{
		RemoteControlTarget: secondTarget,
		AccountID:           "account-a",
		EnvironmentID:       "env_second",
		ServerID:            "srv_e_second",
		ServerName:          "second-server",
	}

	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, firstTarget, "account-a", &clientName, firstEnrollment, &enabled); err != nil {
		t.Fatalf("persist first: %v", err)
	}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, secondTarget, "account-a", &clientName, secondEnrollment, nil); err != nil {
		t.Fatalf("persist second: %v", err)
	}

	loaded, err := LoadPersistedRemoteControlEnrollment(ctx, store, firstTarget, "account-a", &clientName)
	if err != nil {
		t.Fatalf("load first: %v", err)
	}
	if loaded == nil || loaded.ServerID != "srv_e_first" || loaded.EnvironmentID != "env_first" || loaded.RemoteControlToken != nil || loaded.ExpiresAt != nil {
		t.Fatalf("loaded first = %+v", loaded)
	}
	missing, err := LoadPersistedRemoteControlEnrollment(ctx, store, firstTarget, "account-b", &clientName)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing = %+v, want nil", missing)
	}
	loaded, err = LoadPersistedRemoteControlEnrollment(ctx, store, secondTarget, "account-a", &clientName)
	if err != nil {
		t.Fatalf("load second: %v", err)
	}
	if loaded == nil || loaded.ServerID != "srv_e_second" || loaded.EnvironmentID != "env_second" {
		t.Fatalf("loaded second = %+v", loaded)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, firstTarget.WebSocketURL, "account-a", &clientName)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record.RemoteControlEnabled == nil || *record.RemoteControlEnabled != false || record.AppServerClientName == nil || *record.AppServerClientName != clientName {
		t.Fatalf("record = %+v", record)
	}
}

func TestPersistedRemoteControlEnrollmentDeleteRemovesOnlyMatchingEntry(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	firstTarget := mustTarget(t, "https://chatgpt.com/remote/control")
	secondTarget := mustTarget(t, "https://api.chatgpt-staging.com/other/control")
	firstEnrollment := &Enrollment{RemoteControlTarget: firstTarget, AccountID: "account-a", EnvironmentID: "env_first", ServerID: "srv_e_first", ServerName: "first-server"}
	secondEnrollment := &Enrollment{RemoteControlTarget: secondTarget, AccountID: "account-a", EnvironmentID: "env_second", ServerID: "srv_e_second", ServerName: "second-server"}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, firstTarget, "account-a", nil, firstEnrollment, nil); err != nil {
		t.Fatalf("persist first: %v", err)
	}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, secondTarget, "account-a", nil, secondEnrollment, nil); err != nil {
		t.Fatalf("persist second: %v", err)
	}

	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, firstTarget, "account-a", nil, nil, nil); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	loaded, err := LoadPersistedRemoteControlEnrollment(ctx, store, firstTarget, "account-a", nil)
	if err != nil {
		t.Fatalf("load deleted: %v", err)
	}
	if loaded != nil {
		t.Fatalf("deleted first = %+v, want nil", loaded)
	}
	loaded, err = LoadPersistedRemoteControlEnrollment(ctx, store, secondTarget, "account-a", nil)
	if err != nil {
		t.Fatalf("load second: %v", err)
	}
	if loaded == nil || loaded.ServerID != "srv_e_second" {
		t.Fatalf("second after delete = %+v", loaded)
	}
}

func TestUpsertRemoteControlEnrollmentPreservesEnabledOnConflict(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	enabled := true
	first := &Enrollment{RemoteControlTarget: target, AccountID: "account-a", EnvironmentID: "env_first", ServerID: "srv_e_first", ServerName: "first-server"}
	second := &Enrollment{RemoteControlTarget: target, AccountID: "account-a", EnvironmentID: "env_second", ServerID: "srv_e_second", ServerName: "second-server"}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, first, &enabled); err != nil {
		t.Fatalf("persist first: %v", err)
	}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, second, nil); err != nil {
		t.Fatalf("persist second: %v", err)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record.ServerID != "srv_e_second" || record.RemoteControlEnabled == nil || *record.RemoteControlEnabled != true {
		t.Fatalf("record = %+v, want second server and enabled preserved", record)
	}
}

func TestSetRemoteControlEnabledUpdatesExistingEnrollment(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	enrollment := &Enrollment{RemoteControlTarget: target, AccountID: "account-a", EnvironmentID: "env_first", ServerID: "srv_e_first", ServerName: "first-server"}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, enrollment, nil); err != nil {
		t.Fatalf("persist: %v", err)
	}
	rows, err := store.SetRemoteControlEnabled(ctx, target.WebSocketURL, "account-a", nil, true)
	if err != nil {
		t.Fatalf("SetRemoteControlEnabled() error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("record = %+v, want enabled", record)
	}
}

func TestPersistedRemoteControlEnrollmentErrorsMatchRustContext(t *testing.T) {
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	_, err := LoadPersistedRemoteControlEnrollment(context.Background(), nil, target, "account-a", nil)
	if err == nil || !strings.Contains(err.Error(), "remote control enrollment cache unavailable because sqlite state db is disabled") {
		t.Fatalf("load nil store error = %v", err)
	}
	err = UpdatePersistedRemoteControlEnrollment(context.Background(), nil, target, "account-a", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "remote control enrollment persistence unavailable because sqlite state db is disabled") {
		t.Fatalf("update nil store error = %v", err)
	}
	err = UpdatePersistedRemoteControlEnrollment(context.Background(), newTestEnrollmentStore(t), target, "account-a", nil, &Enrollment{AccountID: "account-b"}, nil)
	if err == nil || !strings.Contains(err.Error(), "enrollment account_id does not match expected account_id `account-a`") {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestEnrollmentStoreEnsureSchemaAddsEnabledColumnToOldTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state_5.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE remote_control_enrollments (
	websocket_url TEXT NOT NULL,
	account_id TEXT NOT NULL,
	app_server_client_name TEXT NOT NULL,
	server_id TEXT NOT NULL,
	environment_id TEXT NOT NULL,
	server_name TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (websocket_url, account_id, app_server_client_name)
)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	_ = db.Close()
	store, err := OpenEnrollmentStore(dbPath)
	if err != nil {
		t.Fatalf("OpenEnrollmentStore() error = %v", err)
	}
	defer store.Close()
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	enabled := true
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	err = UpdatePersistedRemoteControlEnrollment(context.Background(), store, target, "account-a", nil, &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env",
		ServerID:            "server",
		ServerName:          "name",
	}, &enabled)
	if err != nil {
		t.Fatalf("persist after migration: %v", err)
	}
}

func newTestEnrollmentStore(t *testing.T) *EnrollmentStore {
	t.Helper()
	store, err := OpenEnrollmentStore(filepath.Join(t.TempDir(), "state_5.sqlite"))
	if err != nil {
		t.Fatalf("OpenEnrollmentStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Fatalf("Close enrollment store: %v", err)
		}
	})
	store.Now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	return store
}

func mustTarget(t *testing.T, raw string) *RemoteControlTarget {
	t.Helper()
	target, err := NormalizeRemoteControlURL(raw)
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL(%q) error = %v", raw, err)
	}
	return target
}
