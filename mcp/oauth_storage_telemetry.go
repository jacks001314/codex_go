package mcp

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"codex_go/keyring"
)

// OAuthStorageTelemetry mirrors Rust's bounded credential-storage observation
// (#47901/#49392). Tags contain only closed classifications; credential values,
// paths, server names, and raw errors are never included.
type OAuthStorageTelemetry struct {
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

type oauthStorageTelemetryObserverFunc func(OAuthStorageTelemetry)

var oauthStorageTelemetry struct {
	sync.Mutex
	observer oauthStorageTelemetryObserverFunc
}

// SetOAuthStorageTelemetryObserver installs the observer used by MCP OAuth
// credential stores. A nil observer disables recording.
func SetOAuthStorageTelemetryObserver(observer func(OAuthStorageTelemetry)) {
	oauthStorageTelemetry.Lock()
	defer oauthStorageTelemetry.Unlock()
	oauthStorageTelemetry.observer = observer
}

func recordOAuthStorageTelemetry(event OAuthStorageTelemetry) {
	oauthStorageTelemetry.Lock()
	observer := oauthStorageTelemetry.observer
	oauthStorageTelemetry.Unlock()
	if observer != nil {
		observer(event)
	}
}

type oauthStorageAttempt struct {
	started       time.Time
	storeMode     string
	selectedStore string
	operation     string
}

func newOAuthStorageAttempt(mode OAuthCredentialsStoreMode, operation string) oauthStorageAttempt {
	selected := "keyring"
	if mode == OAuthCredentialsStoreFile {
		selected = "file"
	}
	return oauthStorageAttempt{
		started:       time.Now(),
		storeMode:     string(mode),
		selectedStore: selected,
		operation:     operation,
	}
}

func (a oauthStorageAttempt) finish(actualStore string, outcome string, secureOutcome string, secureError string) {
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
	if a.storeMode == string(OAuthCredentialsStoreAuto) && actualStore == "file" {
		switch secureOutcome {
		case "error":
			fallback = "secure_error"
		case "not_found":
			fallback = "secure_entry_missing"
		}
	}
	recordOAuthStorageTelemetry(OAuthStorageTelemetry{
		CredentialKind: "mcp",
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
		Originator:     "mcp",
	})
}

func oauthStorageOutcome(tokens *OAuthTokenSet, err error) string {
	if err != nil {
		return "error"
	}
	if tokens == nil {
		return "not_found"
	}
	return "success"
}

func oauthStorageSaveOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func oauthStorageDeleteOutcome(removed bool, err error) string {
	if err != nil {
		return "error"
	}
	if !removed {
		return "not_found"
	}
	return "success"
}

func classifyOAuthStorageError(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, keyring.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, os.ErrPermission):
		return "access_denied"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "other"
	}
}
