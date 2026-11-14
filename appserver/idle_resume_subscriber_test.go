package appserver

import (
	"codex_go/sandbox"
	"codex_go/session"
	"encoding/json"
	"testing"
)

func TestSubscribedIdleResumeKeepsExistingConfiguration(t *testing.T) {
	store := session.NewStore(t.TempDir())
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: extras, ThreadStatus: NewThreadStatusManager()})
	defer router.Close()
	cwd := t.TempDir()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd, Model: "original-model", Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	router.threads.Subscribe(id, "existing-observer")
	if !router.threadIsLoaded(id) {
		t.Fatal("expected loaded thread")
	}
	if router.activeRuntimeTurnSnapshot(id) != nil {
		t.Fatal("expected idle thread")
	}
	liveModel := "live-model"
	liveCWD := t.TempDir()
	liveApproval := "never"
	liveSandbox := "read-only"
	liveRaw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"}]},"network":"restricted"}`)
	if _, _, err := extras.UpdateSettingsWithCommit(&SettingsUpdateParams{ThreadID: id, Model: &liveModel, CWD: &liveCWD, ApprovalPolicy: &liveApproval, SandboxPolicy: &liveSandbox, normalizedPermissionProfile: liveRaw}); err != nil {
		t.Fatal(err)
	}
	model := "replacement-model"
	replacementCWD := t.TempDir()
	internal := requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{})
	internal.Params = nil
	internal.InternalParams = &ThreadResumeParams{ThreadID: id, Model: &model, CWD: &replacementCWD, ExcludeTurns: true}
	prepared, prepareErr := router.prepareSubscribedIdleResume(internal)
	if prepareErr != nil {
		t.Fatalf("internal prepare: %v", prepareErr)
	}
	var decoded ThreadResumeParams
	if err := prepared.DecodeParams(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ThreadID != id || !decoded.ExcludeTurns || decoded.Model != nil || decoded.CWD != nil {
		t.Fatalf("prepared internal params: %+v", decoded)
	}
	if internal.InternalParams.(*ThreadResumeParams).Model != &model {
		t.Fatal("mutated caller params")
	}
	result := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, Model: &model, CWD: &replacementCWD, ExcludeTurns: true}))
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	response := result.Result.(*ThreadResumeResponse)
	if response.Model != liveModel || response.CWD != liveCWD {
		t.Errorf("resume changed live configuration: model=%q cwd=%q", response.Model, response.CWD)
	}
	if response.ApprovalPolicy != liveApproval {
		t.Errorf("approval=%v want %s", response.ApprovalPolicy, liveApproval)
	}
	if got := resumeSandboxKind(t, response.Sandbox); got != sandbox.SandboxReadOnly {
		t.Errorf("sandbox=%s", got)
	}
	if settings := extras.Settings(id); settings != nil && (settings.Model == model || settings.CWD == replacementCWD) {
		t.Errorf("persisted replacement: %+v", settings)
	}
	if _, _, err := extras.UpdateSettingsWithCommit(&SettingsUpdateParams{ThreadID: id, SandboxPolicy: &liveSandbox, normalizedPermissionProfile: json.RawMessage("{")}); err != nil {
		t.Fatal(err)
	}
	extras.SetActiveDisabledPluginIDs(id, []string{"existing-plugin"})
	invalid := router.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{ThreadID: id, ExcludeTurns: true}))
	if invalid.Error == nil {
		t.Fatal("resume succeeded with an invalid live permission snapshot")
	}
	if ids, active := extras.ActiveDisabledPluginIDs(id); !active || len(ids) != 1 || ids[0] != "existing-plugin" {
		t.Fatalf("rejected resume changed active plugin state: ids=%v active=%v", ids, active)
	}

}

func TestSubscribedIdleResumeReportsLiveNamedPermissions(t *testing.T) {
	store := session.NewStore(t.TempDir())
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: extras, ThreadStatus: NewThreadStatusManager()})
	defer router.Close()
	original := ":danger-full-access"
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Permissions: &original, Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	router.threads.Subscribe(id, "existing-observer")
	live := ":read-only"
	if _, _, err := extras.UpdateSettingsWithCommit(&SettingsUpdateParams{ThreadID: id, Permissions: &live}); err != nil {
		t.Fatal(err)
	}
	result := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, Permissions: &original, ExcludeTurns: true}))
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	response := result.Result.(*ThreadResumeResponse)
	if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != live {
		t.Fatalf("active profile=%+v, want %s", response.ActivePermissionProfile, live)
	}
	if kind := resumeSandboxKind(t, response.Sandbox); kind != sandbox.SandboxReadOnly {
		t.Fatalf("live read-only profile returned sandbox %s", kind)
	}
	missing := "missing-live-profile"
	if _, _, err := extras.UpdateSettingsWithCommit(&SettingsUpdateParams{ThreadID: id, Permissions: &missing}); err != nil {
		t.Fatal(err)
	}
	extras.SetActiveDisabledPluginIDs(id, []string{"existing-plugin"})
	rejected := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: id, ExcludeTurns: true}))
	if rejected.Error == nil {
		t.Fatal("resume accepted an unresolved live profile")
	}
	if ids, active := extras.ActiveDisabledPluginIDs(id); !active || len(ids) != 1 || ids[0] != "existing-plugin" {
		t.Fatalf("unresolved profile changed active plugin state: ids=%v active=%v", ids, active)
	}
}
