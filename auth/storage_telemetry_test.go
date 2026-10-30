package auth

import "testing"

func TestCLIAuthStorageTelemetryFileModeLikeRust(t *testing.T) {
	var events []StorageTelemetry
	SetStorageTelemetryObserver(func(event StorageTelemetry) { events = append(events, event) })
	defer SetStorageTelemetryObserver(nil)
	store := NewStoreWithOptions(t.TempDir(), &StoreOptions{Mode: AuthCredentialsStoreFile})
	if err := store.Save(FromAPIKey("sk-secret")); err != nil {
		t.Fatal(err)
	}
	if loaded, err := store.Load(); err != nil || loaded == nil {
		t.Fatalf("load = %#v, err=%v", loaded, err)
	}
	if removed, err := store.Delete(); err != nil || !removed {
		t.Fatalf("delete = %v, err=%v", removed, err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].CredentialKind != "codex" || events[0].Operation != "save" || events[0].ActualStore != "file" ||
		events[0].Outcome != "success" || events[0].SecureOutcome != "not_attempted" || events[0].FallbackReason != "none" {
		t.Fatalf("save telemetry = %#v", events[0])
	}
	if events[1].Operation != "load" || events[1].Outcome != "success" {
		t.Fatalf("load telemetry = %#v", events[1])
	}
	if events[2].Operation != "delete" || events[2].Outcome != "success" {
		t.Fatalf("delete telemetry = %#v", events[2])
	}
}

func TestCLIAuthStorageTelemetryKeyringUnavailableLikeRust(t *testing.T) {
	previous := OSKeyringAvailable
	OSKeyringAvailable = false
	defer func() { OSKeyringAvailable = previous }()
	var events []StorageTelemetry
	SetStorageTelemetryObserver(func(event StorageTelemetry) { events = append(events, event) })
	defer SetStorageTelemetryObserver(nil)
	store := NewStoreWithOptions(t.TempDir(), &StoreOptions{Mode: AuthCredentialsStoreKeyring})
	if err := store.Save(FromAPIKey("sk-secret")); err == nil {
		t.Fatal("keyring save should fail when the backend is unavailable")
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("keyring load should fail when the backend is unavailable")
	}
	if _, err := store.Delete(); err == nil {
		t.Fatal("keyring delete should fail when the backend is unavailable")
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		wantActual := "direct_keyring"
		wantSecureOutcome := "error"
		wantSecureError := "unavailable"
		if event.Operation == "delete" {
			// Rust records a multi-store delete without a secure attempt.
			wantActual = "multiple"
			wantSecureOutcome = "not_attempted"
			wantSecureError = "none"
		}
		if event.StoreMode != "keyring" || event.SelectedStore != "direct_keyring" || event.ActualStore != wantActual ||
			event.Outcome != "error" || event.SecureOutcome != wantSecureOutcome || event.SecureError != wantSecureError {
			t.Fatalf("keyring telemetry = %#v", event)
		}
	}
}
