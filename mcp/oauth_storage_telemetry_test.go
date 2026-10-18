package mcp

import (
	"errors"
	"testing"
)

type failingOAuthKeyring struct{}

func (failingOAuthKeyring) Load(string, string) (string, error) {
	return "", errors.New("keyring load failed")
}
func (failingOAuthKeyring) Save(string, string, string) error {
	return errors.New("keyring save failed")
}
func (failingOAuthKeyring) Delete(string, string) (bool, error) {
	return false, errors.New("keyring delete failed")
}

func TestMCPOAuthStorageTelemetryRecordsFallbackLikeRust(t *testing.T) {
	previousAvailable := MCPOAuthKeyringAvailable
	MCPOAuthKeyringAvailable = true
	defer func() { MCPOAuthKeyringAvailable = previousAvailable }()

	var events []OAuthStorageTelemetry
	SetOAuthStorageTelemetryObserver(func(event OAuthStorageTelemetry) {
		events = append(events, event)
	})
	defer SetOAuthStorageTelemetryObserver(nil)

	home := t.TempDir()
	store := &OAuthStore{CodexHome: home, Mode: OAuthCredentialsStoreAuto, Keyring: failingOAuthKeyring{}}
	tokens := &OAuthTokenSet{ServerName: "docs", ServerURL: "https://example.test/mcp", ClientID: "client", AccessToken: "secret"}
	if err := store.Save(tokens); err != nil {
		t.Fatal(err)
	}
	if loaded, err := store.Load(tokens.ServerName, tokens.ServerURL); err != nil || loaded == nil {
		t.Fatalf("load = %#v, err=%v", loaded, err)
	}
	if _, err := store.Delete(tokens.ServerName, tokens.ServerURL); err == nil {
		t.Fatal("auto-mode delete with a failing keyring should propagate the secure error")
	}

	if len(events) != 3 {
		t.Fatalf("telemetry events = %#v", events)
	}
	save := events[0]
	if save.CredentialKind != "mcp" || save.StoreMode != "auto" || save.SelectedStore != "keyring" ||
		save.ActualStore != "file" || save.Operation != "save" || save.SecureOutcome != "error" ||
		save.Outcome != "success" || save.FallbackReason != "secure_error" || save.SecureError != "other" {
		t.Fatalf("save telemetry = %#v", save)
	}
	load := events[1]
	if load.Operation != "load" || load.ActualStore != "file" || load.SecureOutcome != "error" ||
		load.Outcome != "success" || load.FallbackReason != "secure_error" {
		t.Fatalf("load telemetry = %#v", load)
	}
	if events[2].Operation != "delete" || events[2].ActualStore != "multiple" || events[2].Outcome != "error" || events[2].SecureOutcome != "error" {
		t.Fatalf("delete telemetry = %#v", events[2])
	}
}

func TestMCPOAuthStorageTelemetryFileModeOutcomesLikeRust(t *testing.T) {
	var events []OAuthStorageTelemetry
	SetOAuthStorageTelemetryObserver(func(event OAuthStorageTelemetry) { events = append(events, event) })
	defer SetOAuthStorageTelemetryObserver(nil)
	store := NewOAuthStoreWithMode(t.TempDir(), OAuthCredentialsStoreFile)
	if _, err := store.Load("docs", "https://example.test/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&OAuthTokenSet{ServerName: "docs", ServerURL: "https://example.test/mcp", ClientID: "client", AccessToken: "secret"}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Operation != "load" || events[0].Outcome != "not_found" || events[0].ActualStore != "file" || events[0].SecureOutcome != "not_attempted" {
		t.Fatalf("file load telemetry = %#v", events[0])
	}
	if events[1].Operation != "save" || events[1].Outcome != "success" || events[1].ActualStore != "file" {
		t.Fatalf("file save telemetry = %#v", events[1])
	}
}
