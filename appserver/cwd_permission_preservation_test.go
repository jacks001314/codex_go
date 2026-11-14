package appserver

import (
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #61195: moving a session retargets runtime roots without changing its
// selected permission profile or dropping independently configured roots.
func TestCWDUpdateRetargetsLivePermissionWorkspaceRootsLikeRust(t *testing.T) {
	oldCWD, newCWD, docs := t.TempDir(), t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name     string
		explicit []string
		want     []string
	}{
		{"cwd-only", nil, []string{newCWD, docs}},
		{"explicit-roots", []string{docs}, []string{docs}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := NewThreadExtraService()
			profile := "audit"
			if _, err := service.UpdateSettings(&SettingsUpdateParams{
				ThreadID: "thread", CWD: &oldCWD, Permissions: &profile,
				RuntimeWorkspaceRoots: []string{oldCWD, docs},
			}); err != nil {
				t.Fatal(err)
			}
			_, committed, err := service.UpdateSettingsWithCommit(&SettingsUpdateParams{
				ThreadID: "thread", CWD: &newCWD, RuntimeWorkspaceRoots: tc.explicit,
			})
			if err != nil {
				t.Fatal(err)
			}
			if committed.ActivePermissionProfile == nil || *committed.ActivePermissionProfile != profile {
				t.Fatalf("profile changed: %#v", committed.ActivePermissionProfile)
			}
			if !reflect.DeepEqual(committed.RuntimeWorkspaceRoots, tc.want) {
				t.Fatalf("roots = %v, want %v", committed.RuntimeWorkspaceRoots, tc.want)
			}
			params := &turn.TurnStartParams{ThreadID: "thread", CWD: newCWD}
			applyThreadSettingsEnvironmentConfigToTurnStartParams(params, service.Settings("thread"))
			if !reflect.DeepEqual(params.RuntimeWorkspaceRoots, tc.want) {
				t.Fatalf("next turn roots = %v, want %v", params.RuntimeWorkspaceRoots, tc.want)
			}
			for _, root := range params.RuntimeWorkspaceRoots {
				if filepath.Clean(root) == filepath.Clean(oldCWD) {
					t.Fatal("next turn retained old workspace")
				}
			}
		})
	}
}

// Exercise settings admission, persistence, next-turn preparation and the
// filesystem enforcement projection, without making a model request.
func TestCWDSettingsUpdatePreservesCustomPermissionsForNextTurnLikeRust(t *testing.T) {
	home, oldCWD, newCWD := t.TempDir(), t.TempDir(), t.TempDir()
	docs := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService(),
		Config: config.NewConfigService(home), Turns: turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(), DefaultCWD: oldCWD, Agent: newRecordingRuntimeAgent("done"),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)
	profile := "audit"
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: oldCWD, Permissions: &profile, RuntimeWorkspaceRoots: []string{oldCWD},
		Config: map[string]any{"permissions.audit": map[string]any{
			"filesystem": map[string]any{":minimal": "read", ":workspace_roots": "write", docs: "read"},
		}},
	}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	initial := router.Handle(requestWithParams(t, IntID(10), MethodTurnStart, turn.TurnStartParams{ThreadID: id, Prompt: "materialize session"}))
	if initial.Error != nil {
		t.Fatal(initial.Error)
	}
	waitForTurnCompletedStatus(t, sink, initial.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	update := router.Handle(requestWithParams(t, IntID(2), MethodThreadSettingsUpdate, SettingsUpdateParams{ThreadID: id, CWD: &newCWD}))
	if update.Error != nil {
		t.Fatal(update.Error)
	}
	params := &turn.TurnStartParams{ThreadID: id}
	if err := router.prepareTurnStartParams(params); err != nil {
		t.Fatal(err)
	}
	if !sameAppPath(params.CWD, newCWD) {
		t.Fatalf("next cwd = %q", params.CWD)
	}
	cfg, err := router.effectiveConfigForTurn(params)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := turnSandboxPermissionProfile(cfg, params.CWD, params)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.ID != profile {
		t.Fatalf("profile = %#v", resolved)
	}
	enforced, err := sandbox.ResolveFileSystemPermissionsForEnforcement(resolved.Profile, newCWD, sandboxpath.ProjectionNative)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path  string
		write bool
	}{
		{filepath.Join(newCWD, "ordinary.txt"), true},
		{filepath.Join(newCWD, ".git", "config"), false},
		{filepath.Join(docs, "guide.txt"), false},
		{filepath.Join(oldCWD, "ordinary.txt"), false},
	} {
		writable := enforced.HasFullDiskWriteAccess
		for _, root := range enforced.WritableRoots {
			writable = writable || root.IsPathWritable(tc.path)
		}
		if writable != tc.write {
			t.Errorf("write(%q) = %v, want %v; roots=%+v", tc.path, writable, tc.write, enforced.WritableRoots)
		}
	}
	record, err := store.Read(session.ThreadID(id), true, true)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := router.services.ThreadRouter.latestPersistedResumeSettings(record)
	if !ok || persisted.ActivePermissionProfile == nil || persisted.ActivePermissionProfile.ID != profile {
		t.Fatalf("persisted profile = %+v (found=%v)", persisted.ActivePermissionProfile, ok)
	}
	readable := enforced.HasFullDiskReadAccess
	for _, root := range enforced.ReadableRoots {
		if sameAppPath(root, docs) {
			readable = true
		}
	}
	if !readable {
		t.Fatalf("independent read grant lost: %v", enforced.ReadableRoots)
	}
}
