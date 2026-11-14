package appserver

import (
	"bytes"
	"os"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

func TestSubscribedIdleResumeKeepsAdmittedNamedProfileSnapshot(t *testing.T) {
	home := t.TempDir()
	configPath := config.ConfigPath(home)
	body := "[permissions.audit.filesystem]\n\":minimal\" = \"read\"\n[permissions.audit.network]\nenabled = false\n"
	if err := os.WriteFile(configPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService(), ThreadStatus: NewThreadStatusManager(), Config: config.NewConfigService(home)})
	defer closeNotificationTestRouter(t, router)
	cwd := t.TempDir()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd, Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	router.threads.Subscribe(id, "observer")
	name := "audit"
	baseMode := "danger-full-access"
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: id, SandboxPolicy: &baseMode}, true); err != nil {
		t.Fatal(err)
	}
	originalRecord, err := store.Read(session.ThreadID(id), true, true)
	if err != nil {
		t.Fatal(err)
	}
	baseProfile := recordPermissionProfileJSON(originalRecord)
	if len(baseProfile) == 0 {
		t.Fatal("expected persisted unnamed base permissions")
	}
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: id, Permissions: &name, CWD: &cwd}, true); err != nil {
		t.Fatal(err)
	}
	before := router.threadSettingsForTurn(id)
	if len(before.normalizedPermissionProfile) == 0 {
		t.Fatal("named admission did not capture a permission snapshot")
	}
	record, err := store.Read(session.ThreadID(id), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseProfile, recordPermissionProfileJSON(record)) {
		t.Fatal("persisting a named selection overwrote the unnamed base profile")
	}
	if record.Metadata.ActivePermissionProfile != name {
		t.Fatalf("persisted named identity = %q, want %q", record.Metadata.ActivePermissionProfile, name)
	}
	// Removing the selected definition must not invalidate an already admitted runtime.
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.5\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: id, Permissions: &name}, false); err == nil {
		t.Fatal("accepted a new update for a removed named profile")
	}
	if !bytes.Equal(before.normalizedPermissionProfile, router.threadSettingsForTurn(id).normalizedPermissionProfile) {
		t.Fatal("failed admission changed the accepted permission snapshot")
	}
	resumed := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, ExcludeTurns: true}))
	if resumed.Error != nil {
		t.Fatal(resumed.Error)
	}
	response := resumed.Result.(*ThreadResumeResponse)
	if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != name {
		t.Fatalf("profile = %+v", response.ActivePermissionProfile)
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(router.threadSettingsForTurn(id).normalizedPermissionProfile))
	if err != nil {
		t.Fatal(err)
	}
	if profile.AllowsNetwork() {
		t.Fatal("resumed snapshot unexpectedly allows network")
	}
	next := &turn.TurnStartParams{ThreadID: id, CWD: before.CWD}
	if err := router.prepareTurnStartParams(next); err != nil {
		t.Fatal(err)
	}
	resolved, err := threadDefaultSandboxPermissionProfile(&config.Config{Values: map[string]any{}}, next.CWD, next)
	if err != nil {
		t.Fatalf("next turn lost accepted profile: %v", err)
	}
	if resolved.ID != name || resolved.Profile.AllowsNetwork() {
		t.Fatalf("next turn profile = %+v", resolved)
	}
	explicit := ":danger-full-access"
	override := &turn.TurnStartParams{ThreadID: id, Permissions: &explicit}
	applyThreadSettingsEnvironmentConfigToTurnStartParams(override, before)
	resolved, err = threadDefaultSandboxPermissionProfile(&config.Config{Values: map[string]any{}}, next.CWD, override)
	if err != nil || !resolved.Profile.AllowsNetwork() {
		t.Fatalf("explicit override lost: profile=%+v err=%v", resolved, err)
	}
}
