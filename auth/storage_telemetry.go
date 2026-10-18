package auth

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
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
	started       time.Time
	storeMode     string
	selectedStore string
	operation     string
}

func newStorageAttempt(mode AuthCredentialsStoreMode, operation string) storageAttempt {
	selected := "file"
	switch mode {
	case AuthCredentialsStoreKeyring, AuthCredentialsStoreAuto:
		selected = "direct_keyring"
	case AuthCredentialsStoreEphemeral:
		selected = "ephemeral"
	}
	return storageAttempt{started: time.Now(), storeMode: string(mode), selectedStore: selected, operation: operation}
}

func (a storageAttempt) finish(actualStore string, outcome string, secureOutcome string, secureError string) {
	if actualStore == "" {
		actualStore = a.selectedStore
	}
	if outcome == "" {
		outcome = "not_attempted"
	}
	if secureOutcome == "" {
		secureOutcome = "not_attempted"
	}
	if secureError == "" {
		secureError = "none"
	}
	fallback := "none"
	if a.storeMode == string(AuthCredentialsStoreAuto) && actualStore == "file" {
		switch secureOutcome {
		case "error":
			fallback = "secure_error"
		case "not_found":
			fallback = "secure_entry_missing"
		}
	}
	recordStorageTelemetry(StorageTelemetry{
		CredentialKind: "codex",
		StoreMode:      a.storeMode,
		SelectedStore:  a.selectedStore,
		ActualStore:    actualStore,
		Operation:      a.operation,
		SecureOutcome:  secureOutcome,
		Outcome:        outcome,
		DurationMS:     time.Since(a.started).Milliseconds(),
		StoragePhase:   "policy",
		SecureError:    secureError,
		FallbackReason: fallback,
		Originator:     "cli",
	})
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
	case errors.Is(err, ErrKeyringUnavailable):
		return "unavailable"
	case errors.Is(err, os.ErrPermission):
		return "access_denied"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "other"
	}
}
