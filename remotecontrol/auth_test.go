package remotecontrol

import (
	"context"
	"errors"
	"testing"

	"codex_go/auth"
)

func TestUnauthorizedRecoveryModeStepAndUnavailableReason(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(chatGPTRecoverySnapshot("old-token", "refresh-token", "account-a")); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	recovery := NewUnauthorizedRecoveryForCodexHome(home, nil)
	if recovery.ModeName() != "managed" || recovery.StepName() != "reload" || !recovery.HasNext() || recovery.UnavailableReason() != "ready" {
		t.Fatalf("managed recovery mode=%s step=%s hasNext=%v reason=%s", recovery.ModeName(), recovery.StepName(), recovery.HasNext(), recovery.UnavailableReason())
	}

	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("external-token", "account-a", nil)); err != nil {
		t.Fatalf("save external auth: %v", err)
	}
	recovery = NewUnauthorizedRecoveryForCodexHome(home, nil)
	if recovery.ModeName() != "external" || recovery.StepName() != "external_refresh" || recovery.HasNext() || recovery.UnavailableReason() != "no_external_auth" {
		t.Fatalf("external recovery without callback mode=%s step=%s hasNext=%v reason=%s", recovery.ModeName(), recovery.StepName(), recovery.HasNext(), recovery.UnavailableReason())
	}
	recovery = NewUnauthorizedRecoveryForCodexHome(home, &UnauthorizedRecoveryOptions{
		ExternalRefresh: func(context.Context, *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
			return &RemoteControlConnectionAuth{AccountID: "account-a"}, true, nil
		},
	})
	if recovery.ModeName() != "external" || recovery.StepName() != "external_refresh" || !recovery.HasNext() || recovery.UnavailableReason() != "ready" {
		t.Fatalf("external recovery mode=%s step=%s hasNext=%v reason=%s", recovery.ModeName(), recovery.StepName(), recovery.HasNext(), recovery.UnavailableReason())
	}
}

func TestUnauthorizedRecoveryReloadMarksChangedAndAdvancesToRefresh(t *testing.T) {
	home := t.TempDir()
	store := auth.NewStore(home)
	if err := store.Save(chatGPTRecoverySnapshot("old-token", "refresh-token", "account-a")); err != nil {
		t.Fatalf("save old auth: %v", err)
	}
	recovery := NewUnauthorizedRecoveryForCodexHome(home, nil)
	if err := store.Save(chatGPTRecoverySnapshot("new-token", "refresh-token", "account-a")); err != nil {
		t.Fatalf("save new auth: %v", err)
	}

	result, err := recovery.Next(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if result.Auth() == nil || result.Auth().AccountID != "account-a" {
		t.Fatalf("result auth = %+v", result.Auth())
	}
	if changed := result.AuthStateChanged(); changed == nil || !*changed {
		t.Fatalf("auth state changed = %v, want true", changed)
	}
	if recovery.StepName() != "refresh_token" {
		t.Fatalf("step after reload = %s, want refresh_token", recovery.StepName())
	}
}

func TestUnauthorizedRecoveryReloadNoChangeReportsFalse(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(chatGPTRecoverySnapshot("old-token", "refresh-token", "account-a")); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	recovery := NewUnauthorizedRecoveryForCodexHome(home, nil)

	result, err := recovery.Next(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if changed := result.AuthStateChanged(); changed == nil || *changed {
		t.Fatalf("auth state changed = %v, want false", changed)
	}
}

func TestUnauthorizedRecoveryReloadRejectsAccountMismatch(t *testing.T) {
	home := t.TempDir()
	store := auth.NewStore(home)
	if err := store.Save(chatGPTRecoverySnapshot("old-token", "refresh-token", "account-a")); err != nil {
		t.Fatalf("save old auth: %v", err)
	}
	recovery := NewUnauthorizedRecoveryForCodexHome(home, nil)
	if err := store.Save(chatGPTRecoverySnapshot("new-token", "refresh-token", "account-b")); err != nil {
		t.Fatalf("save new auth: %v", err)
	}

	_, err := recovery.Next(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if err == nil || err.Error() != refreshTokenAccountMismatchMessage {
		t.Fatalf("Next() error = %v, want account mismatch", err)
	}
	if recovery.StepName() != "done" || recovery.HasNext() {
		t.Fatalf("step/hasNext after mismatch = %s/%v, want done/false", recovery.StepName(), recovery.HasNext())
	}
}

func TestUnauthorizedRecoveryExternalRefreshCompletes(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("external-token", "account-a", nil)); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	calls := 0
	recovery := NewUnauthorizedRecoveryForCodexHome(home, &UnauthorizedRecoveryOptions{
		ExternalRefresh: func(context.Context, *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
			calls++
			return &RemoteControlConnectionAuth{AccountID: "account-a"}, true, nil
		},
	})

	result, err := recovery.Next(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if calls != 1 || result.Auth() == nil || result.Auth().AccountID != "account-a" {
		t.Fatalf("calls/result = %d/%+v", calls, result.Auth())
	}
	if changed := result.AuthStateChanged(); changed == nil || !*changed {
		t.Fatalf("auth state changed = %v, want true", changed)
	}
	if recovery.StepName() != "done" || recovery.HasNext() || recovery.UnavailableReason() != "recovery_exhausted" {
		t.Fatalf("step/hasNext/reason = %s/%v/%s", recovery.StepName(), recovery.HasNext(), recovery.UnavailableReason())
	}
}

func TestUnauthorizedRecoveryExternalRefreshErrorKeepsStep(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("external-token", "account-a", nil)); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	wantErr := errors.New("refresh failed")
	recovery := NewUnauthorizedRecoveryForCodexHome(home, &UnauthorizedRecoveryOptions{
		ExternalRefresh: func(context.Context, *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
			return nil, false, wantErr
		},
	})

	_, err := recovery.Next(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Next() error = %v, want %v", err, wantErr)
	}
	if recovery.StepName() != "external_refresh" || !recovery.HasNext() {
		t.Fatalf("step/hasNext after error = %s/%v, want external_refresh/true", recovery.StepName(), recovery.HasNext())
	}
}

func TestUnauthorizedRecoveryControllerObservesStepMetadataAndReset(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(chatGPTRecoverySnapshot("old-token", "refresh-token", "account-a")); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	var events []RemoteControlAuthRecoveryEvent
	controller := NewUnauthorizedRecoveryControllerForCodexHome(home, &UnauthorizedRecoveryOptions{
		Observer: func(event RemoteControlAuthRecoveryEvent) {
			events = append(events, event)
		},
	})

	recovered, ok, err := controller.Recover(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if err != nil || !ok || recovered == nil {
		t.Fatalf("first Recover() recovered=%+v ok=%v err=%v", recovered, ok, err)
	}
	if len(events) != 1 || events[0].Mode != "managed" || events[0].Step != "reload" || events[0].AuthStateChanged == nil || *events[0].AuthStateChanged || events[0].Err != nil {
		t.Fatalf("first event = %+v", events)
	}
	if events[0].UnavailableReason != "ready" {
		t.Fatalf("first unavailable reason = %q, want ready", events[0].UnavailableReason)
	}

	controller.Reset()
	recovered, ok, err = controller.Recover(context.Background(), &RemoteControlConnectionAuth{AccountID: "account-a"})
	if err != nil || !ok || recovered == nil {
		t.Fatalf("recover after Reset() recovered=%+v ok=%v err=%v", recovered, ok, err)
	}
	if len(events) != 2 || events[1].Mode != "managed" || events[1].Step != "reload" {
		t.Fatalf("event after reset = %+v", events)
	}
}

func chatGPTRecoverySnapshot(accessToken string, refreshToken string, accountID string) auth.AuthDotJSON {
	return auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"account_id":    accountID,
		},
	}
}
