package auth

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"codex_go/keyring"
)

// StorageTelemetry mirrors Rust's bounded credential-storage observation for
// CLI auth (#47901). It never carries credentials, paths, or raw error text.
type StorageTelemetry struct {
	CredentialKind string
	StoreMode      string
	SelectedStore  string
	ActualStore    string
	Operation      string
	SecureOutcome  string
	Outcome        string
	DurationMS     int64
	StoragePhase   string
	SecureError    string
	FallbackReason string
	Originator     string
}

type storageTelemetryObserverFunc func(StorageTelemetry)

var storageTelemetry struct {
	sync.Mutex
	observer storageTelemetryObserverFunc
}

// SetStorageTelemetryObserver installs the observer used by CLI auth stores.
func SetStorageTelemetryObserver(observer func(StorageTelemetry)) {
	storageTelemetry.Lock()
	defer storageTelemetry.Unlock()
	storageTelemetry.observer = observer
}

func recordStorageTelemetry(event StorageTelemetry) {
	storageTelemetry.Lock()
	observer := storageTelemetry.observer
	storageTelemetry.Unlock()
	if observer != nil {
		observer(event)
	}
}

type storageAttempt struct {
	started        time.Time
	storeMode      AuthCredentialsStoreMode
	keyringBackend KeyringBackendKind
	selectedStore  string
	actualStore    string
	operation      string
	outcome        string
	secureOutcome  string
	secureError    string
	originator     string
}

func newStorageAttempt(mode AuthCredentialsStoreMode, backend KeyringBackendKind, operation string, originator string) *storageAttempt {
	selected := "file"
	switch mode {
	case AuthCredentialsStoreKeyring, AuthCredentialsStoreAuto:
		selected = keyringStoreName(backend)
	case AuthCredentialsStoreEphemeral:
		selected = "ephemeral"
	}
	return &storageAttempt{
		started:        time.Now(),
		storeMode:      mode,
		keyringBackend: backend,
		selectedStore:  selected,
		actualStore:    selected,
		operation:      operation,
		outcome:        "not_attempted",
		secureOutcome:  "not_attempted",
		secureError:    "none",
		originator:     normalizeStorageOriginator(originator),
	}
}

func (a *storageAttempt) recordLoadAttempt(store string, auth *AuthDotJSON, err error) {
	a.recordAttempt(store, authStorageLoadOutcome(auth, err))
}

func (a *storageAttempt) recordSaveAttempt(store string, err error) {
	a.recordAttempt(store, authStorageSaveOutcome(err))
}

func (a *storageAttempt) recordDeleteAttempt(store string, removed bool, err error) {
	a.recordAttempt(store, authStorageDeleteOutcome(removed, err))
}

func (a *storageAttempt) recordAttempt(store string, outcome string) {
	a.actualStore = store
	a.outcome = outcome
	if isSecureStorageStore(store) {
		a.secureOutcome = outcome
		if outcome == "error" {
			if a.secureError == "none" {
				a.secureError = "other"
			}
		} else {
			a.secureError = "none"
		}
	}
}

func (a *storageAttempt) recordSecureError(err error) {
	a.secureError = classifyAuthStorageError(err)
	if a.secureOutcome == "not_attempted" && err != nil {
		a.secureOutcome = "error"
	}
}

func (a *storageAttempt) finish() {
	if a == nil || a.outcome == "not_attempted" {
		return
	}
	if a.actualStore == "" {
		a.actualStore = a.selectedStore
	}
	secureError := a.secureError
	if secureError == "" {
		secureError = "none"
	}
	fallback := "none"
	if a.storeMode == AuthCredentialsStoreAuto && a.actualStore == "file" {
		switch a.secureOutcome {
		case "error":
			fallback = "secure_error"
		case "not_found":
			fallback = "secure_entry_missing"
		}
	}
	recordStorageTelemetry(StorageTelemetry{
		CredentialKind: "codex",
		StoreMode:      string(a.storeMode),
		SelectedStore:  a.selectedStore,
		ActualStore:    a.actualStore,
		Operation:      a.operation,
		SecureOutcome:  a.secureOutcome,
		Outcome:        a.outcome,
		DurationMS:     time.Since(a.started).Milliseconds(),
		StoragePhase:   "policy",
		SecureError:    secureError,
		FallbackReason: fallback,
		Originator:     a.originator,
	})
	if fallback != "none" {
		slog.Warn("credential storage file fallback completed",
			"credential_kind", "codex",
			"store_mode", a.storeMode,
			"selected_store", a.selectedStore,
			"actual_store", a.actualStore,
			"operation", a.operation,
			"secure_outcome", a.secureOutcome,
			"outcome", a.outcome,
			"fallback_reason", fallback,
			"secure_error", secureError,
			"storage_phase", "policy",
			"originator", a.originator,
		)
	}
}

func keyringStoreName(backend KeyringBackendKind) string {
	if backend == KeyringBackendSecrets {
		return "secrets"
	}
	return "direct_keyring"
}

func isSecureStorageStore(store string) bool {
	return store == "direct_keyring" || store == "secrets"
}

// normalizeStorageOriginator mirrors Rust's bounded auth-storage attribution.
// It accepts only the known low-cardinality client names.
func normalizeStorageOriginator(originator string) string {
	originator = strings.TrimSpace(originator)
	if originator == "" {
		return "none"
	}
	if originator == "Codex Desktop" {
		originator = "codex_desktop"
	}
	switch originator {
	case "codex_desktop", "codex-app-server", "codex_mcp_server", "codex_cli_rs", "codex-tui",
		"codex_vscode", "none", "codex_exec", "codex-cli", "codex_sdk_ts", "codex-app-server-sdk":
		return originator
	default:
		return "other"
	}
}

func authStorageBackendName(source string) string {
	switch source {
	case "auth.json":
		return "file"
	case "keyring":
		return "direct_keyring"
	case "ephemeral":
		return "ephemeral"
	default:
		return "file"
	}
}

func authStorageLoadOutcome(auth *AuthDotJSON, err error) string {
	if err != nil {
		return "error"
	}
	if auth == nil {
		return "not_found"
	}
	return "success"
}

func authStorageSaveOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func authStorageDeleteOutcome(removed bool, err error) string {
	if err != nil {
		return "error"
	}
	if !removed {
		return "not_found"
	}
	return "success"
}

func classifyAuthStorageError(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrKeyringUnavailable), errors.Is(err, keyring.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, os.ErrPermission):
		return "access_denied"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "other"
	}
}
