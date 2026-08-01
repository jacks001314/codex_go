package remotecontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"codex_go/state"
)

const (
	RustStateDBFilename                  = "state_5.sqlite"
	remoteControlAppServerClientNameNone = ""
)

type EnrollmentRecord struct {
	WebSocketURL         string
	AccountID            string
	AppServerClientName  *string
	ServerID             string
	EnvironmentID        string
	ServerName           string
	RemoteControlEnabled *bool
}

type EnrollmentStore struct {
	DB  *sql.DB
	Now func() time.Time
}

func RemoteControlStateDBPath(codexHome string) string {
	return filepath.Join(state.ResolveSQLiteHome(codexHome), RustStateDBFilename)
}

func OpenEnrollmentStore(dbPath string) (*EnrollmentStore, error) {
	config, err := state.NewSqliteConfig(filepath.Dir(dbPath))
	if err != nil {
		return nil, err
	}
	db, err := config.OpenReadWrite(context.Background(), dbPath)
	if err != nil {
		return nil, err
	}
	return &EnrollmentStore{DB: db}, nil
}

func (s *EnrollmentStore) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *EnrollmentStore) EnsureSchema(ctx context.Context) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS remote_control_enrollments (
	websocket_url TEXT NOT NULL,
	account_id TEXT NOT NULL,
	app_server_client_name TEXT NOT NULL,
	server_id TEXT NOT NULL,
	environment_id TEXT NOT NULL,
	server_name TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	remote_control_enabled INTEGER,
	PRIMARY KEY (websocket_url, account_id, app_server_client_name)
)`)
	if err != nil {
		return err
	}
	hasEnabled, err := s.hasColumn(ctx, "remote_control_enrollments", "remote_control_enabled")
	if err != nil {
		return err
	}
	if !hasEnabled {
		_, err = s.DB.ExecContext(ctx, `ALTER TABLE remote_control_enrollments ADD COLUMN remote_control_enabled INTEGER`)
	}
	return err
}

func (s *EnrollmentStore) GetRemoteControlEnrollment(ctx context.Context, websocketURL string, accountID string, appServerClientName *string) (*EnrollmentRecord, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT websocket_url, account_id, app_server_client_name, server_id, environment_id, server_name, remote_control_enabled
FROM remote_control_enrollments
WHERE websocket_url = ? AND account_id = ? AND app_server_client_name = ?
`, websocketURL, accountID, remoteControlAppServerClientNameKey(appServerClientName))
	var record EnrollmentRecord
	var appServerClientNameKey string
	var enabled sql.NullBool
	err := row.Scan(
		&record.WebSocketURL,
		&record.AccountID,
		&appServerClientNameKey,
		&record.ServerID,
		&record.EnvironmentID,
		&record.ServerName,
		&enabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record.AppServerClientName = appServerClientNameFromKey(appServerClientNameKey)
	if enabled.Valid {
		value := enabled.Bool
		record.RemoteControlEnabled = &value
	}
	return &record, nil
}

func (s *EnrollmentStore) UpsertRemoteControlEnrollment(ctx context.Context, record *EnrollmentRecord) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("%w: enrollment record is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO remote_control_enrollments (
	websocket_url,
	account_id,
	app_server_client_name,
	server_id,
	environment_id,
	server_name,
	remote_control_enabled,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(websocket_url, account_id, app_server_client_name) DO UPDATE SET
	server_id = excluded.server_id,
	environment_id = excluded.environment_id,
	server_name = excluded.server_name,
	updated_at = excluded.updated_at
`,
		record.WebSocketURL,
		record.AccountID,
		remoteControlAppServerClientNameKey(record.AppServerClientName),
		record.ServerID,
		record.EnvironmentID,
		record.ServerName,
		nullableBool(record.RemoteControlEnabled),
		s.now().Unix(),
	)
	return err
}

func (s *EnrollmentStore) SetRemoteControlEnabled(ctx context.Context, websocketURL string, accountID string, appServerClientName *string, enabled bool) (int64, error) {
	if err := s.ensureReady(); err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := s.DB.ExecContext(ctx, `
UPDATE remote_control_enrollments
SET remote_control_enabled = ?, updated_at = ?
WHERE websocket_url = ? AND account_id = ? AND app_server_client_name = ?
`, enabled, s.now().Unix(), websocketURL, accountID, remoteControlAppServerClientNameKey(appServerClientName))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *EnrollmentStore) DeleteRemoteControlEnrollment(ctx context.Context, websocketURL string, accountID string, appServerClientName *string) (int64, error) {
	if err := s.ensureReady(); err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := s.DB.ExecContext(ctx, `
DELETE FROM remote_control_enrollments
WHERE websocket_url = ? AND account_id = ? AND app_server_client_name = ?
`, websocketURL, accountID, remoteControlAppServerClientNameKey(appServerClientName))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func LoadPersistedRemoteControlEnrollment(ctx context.Context, store *EnrollmentStore, target *RemoteControlTarget, accountID string, appServerClientName *string) (*Enrollment, error) {
	if target == nil {
		return nil, fmt.Errorf("%w: remote control target is nil", ErrInvalidRequest)
	}
	if store == nil {
		return nil, fmt.Errorf("remote control enrollment cache unavailable because sqlite state db is disabled: websocket_url=%s, account_id=%s, app_server_client_name=%s", target.WebSocketURL, accountID, formatOptionalStringDebug(appServerClientName))
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, accountID, appServerClientName)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	return &Enrollment{
		RemoteControlTarget: cloneRemoteControlTarget(target),
		AccountID:           record.AccountID,
		EnvironmentID:       record.EnvironmentID,
		ServerID:            record.ServerID,
		ServerName:          record.ServerName,
	}, nil
}

func UpdatePersistedRemoteControlEnrollment(ctx context.Context, store *EnrollmentStore, target *RemoteControlTarget, accountID string, appServerClientName *string, enrollment *Enrollment, remoteControlEnabled *bool) error {
	if target == nil {
		return fmt.Errorf("%w: remote control target is nil", ErrInvalidRequest)
	}
	if store == nil {
		return fmt.Errorf("remote control enrollment persistence unavailable because sqlite state db is disabled: websocket_url=%s, account_id=%s, app_server_client_name=%s, has_enrollment=%t", target.WebSocketURL, accountID, formatOptionalStringDebug(appServerClientName), enrollment != nil)
	}
	if enrollment != nil && enrollment.AccountID != accountID {
		return fmt.Errorf("enrollment account_id does not match expected account_id `%s`", accountID)
	}
	if enrollment == nil {
		_, err := store.DeleteRemoteControlEnrollment(ctx, target.WebSocketURL, accountID, appServerClientName)
		return err
	}
	return store.UpsertRemoteControlEnrollment(ctx, &EnrollmentRecord{
		WebSocketURL:         target.WebSocketURL,
		AccountID:            accountID,
		AppServerClientName:  cloneStringPtr(appServerClientName),
		ServerID:             enrollment.ServerID,
		EnvironmentID:        enrollment.EnvironmentID,
		ServerName:           enrollment.ServerName,
		RemoteControlEnabled: cloneBoolPtr(remoteControlEnabled),
	})
}

func (s *EnrollmentStore) ensureReady() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("%w: enrollment store is nil", ErrInvalidRequest)
	}
	return nil
}

func (s *EnrollmentStore) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *EnrollmentStore) hasColumn(ctx context.Context, table string, column string) (bool, error) {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func remoteControlAppServerClientNameKey(appServerClientName *string) string {
	if appServerClientName == nil {
		return remoteControlAppServerClientNameNone
	}
	return *appServerClientName
}

func appServerClientNameFromKey(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func formatOptionalStringDebug(value *string) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%q)", *value)
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
