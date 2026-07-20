package app

import "testing"

func TestParentOwnedThreadSessionIsReadOnly(t *testing.T) {
	state := ThreadSessionState{ThreadID: "child", ParentOwned: true}
	if !state.IsReadOnly() || state.MutationError() != "This sub-agent thread is owned by its parent and is read-only." {
		t.Fatalf("state = %#v", state)
	}
}

func TestSyncActiveThreadServiceTierToCachedSessionMatchRust(t *testing.T) {
	fast := "flex"
	primary := ThreadSessionState{ThreadID: "thread-1", ServiceTier: &fast}
	storeMain := primary.Clone()
	sideTier := "priority"
	storeSide := ThreadSessionState{ThreadID: "thread-2", ServiceTier: &sideTier}
	cache := &ThreadSessionCache{
		PrimaryThreadID: "thread-1",
		ActiveThreadID:  "thread-1",
		PrimarySession:  &primary,
		StoreSessions: map[string]*ThreadSessionState{
			"thread-1": &storeMain,
			"thread-2": &storeSide,
		},
	}

	SyncActiveThreadServiceTierToCachedSession(cache, nil)

	if cache.PrimarySession.ServiceTier != nil || cache.StoreSessions["thread-1"].ServiceTier != nil {
		t.Fatalf("active service tier not cleared: primary=%#v store=%#v", cache.PrimarySession.ServiceTier, cache.StoreSessions["thread-1"].ServiceTier)
	}
	if cache.StoreSessions["thread-2"].ServiceTier == nil || *cache.StoreSessions["thread-2"].ServiceTier != "priority" {
		t.Fatalf("side service tier mutated: %#v", cache.StoreSessions["thread-2"].ServiceTier)
	}
}

func TestSyncActiveThreadPermissionSettingsToCachedSessionMatchRust(t *testing.T) {
	primary := ThreadSessionState{ThreadID: "thread-1", ApprovalPolicy: "never", PermissionProfile: "read-only"}
	storeMain := primary.Clone()
	storeSide := ThreadSessionState{ThreadID: "thread-2", ApprovalPolicy: "never", PermissionProfile: "side-profile"}
	cache := &ThreadSessionCache{
		PrimaryThreadID: "thread-1",
		ActiveThreadID:  "thread-1",
		PrimarySession:  &primary,
		StoreSessions: map[string]*ThreadSessionState{
			"thread-1": &storeMain,
			"thread-2": &storeSide,
		},
	}

	SyncActiveThreadPermissionSettingsToCachedSession(cache, ThreadPermissionSettings{
		ApprovalPolicy:          "on-request",
		ApprovalsReviewer:       "auto_review",
		PermissionProfile:       "workspace",
		ActivePermissionProfile: "builtin-workspace",
	})

	if cache.PrimarySession.ApprovalPolicy != "on-request" || cache.PrimarySession.ApprovalsReviewer != "auto_review" || cache.PrimarySession.PermissionProfile != "workspace" {
		t.Fatalf("primary session not updated: %#v", cache.PrimarySession)
	}
	if cache.StoreSessions["thread-1"].ActivePermissionProfile != "builtin-workspace" {
		t.Fatalf("store active profile = %#v", cache.StoreSessions["thread-1"].ActivePermissionProfile)
	}
	if cache.StoreSessions["thread-2"].PermissionProfile != "side-profile" || cache.StoreSessions["thread-2"].ApprovalPolicy != "never" {
		t.Fatalf("side session mutated: %#v", cache.StoreSessions["thread-2"])
	}
}

func TestSessionStateForThreadReadClearsThreadScopedStateMatchRust(t *testing.T) {
	collab := "plan"
	personality := "warm"
	threadName := "Read Thread"
	readModel := "gpt-read"
	rollout := "rollout.jsonl"
	primary := &ThreadSessionState{
		ThreadID:               "thread-primary",
		Model:                  "gpt-primary",
		ModelProviderID:        "provider-primary",
		CWD:                    "/repo/primary",
		RuntimeWorkspaceRoots:  []string{"/repo/primary"},
		CollaborationMode:      &collab,
		Personality:            &personality,
		MessageHistory:         []string{"old"},
		InstructionSourcePaths: []string{"AGENTS.md"},
	}

	session := SessionStateForThreadRead(primary, ThreadSessionDefaults{
		PermissionProfile:       "active-profile",
		ActivePermissionProfile: "active",
	}, ThreadReadSnapshot{
		ThreadID:      "thread-read",
		ThreadName:    &threadName,
		ModelProvider: "provider-read",
		CWD:           "/repo/read",
		Path:          &rollout,
		ReadModel:     &readModel,
	})

	if session.ThreadID != "thread-read" || session.ThreadName == nil || *session.ThreadName != "Read Thread" {
		t.Fatalf("thread identity = %#v", session)
	}
	if session.CollaborationMode != nil || session.Personality != nil {
		t.Fatalf("thread-scoped state not cleared: collab=%#v personality=%#v", session.CollaborationMode, session.Personality)
	}
	if session.Model != "gpt-read" || session.ModelProviderID != "provider-read" {
		t.Fatalf("model fields = %#v", session)
	}
	if session.CWD != "/repo/read" || len(session.RuntimeWorkspaceRoots) != 1 || session.RuntimeWorkspaceRoots[0] != "/repo/read" {
		t.Fatalf("cwd/root retarget = cwd %q roots %#v", session.CWD, session.RuntimeWorkspaceRoots)
	}
	if session.PermissionProfile != "active-profile" || session.ActivePermissionProfile != "active" {
		t.Fatalf("permission profile = %#v/%#v", session.PermissionProfile, session.ActivePermissionProfile)
	}
	if session.MessageHistory != nil || len(session.InstructionSourcePaths) != 0 {
		t.Fatalf("history/instructions = %#v/%#v", session.MessageHistory, session.InstructionSourcePaths)
	}
}

func TestSessionStateForThreadReadFallbackAndPathModelClearingMatchRust(t *testing.T) {
	rollout := "rollout.jsonl"
	serviceTier := "priority"
	effort := "high"
	session := SessionStateForThreadRead(nil, ThreadSessionDefaults{
		Model:                   "gpt-default",
		ModelProviderID:         "provider-default",
		ServiceTier:             &serviceTier,
		ApprovalPolicy:          "never",
		ApprovalsReviewer:       "user",
		PermissionProfile:       "profile",
		ActivePermissionProfile: "active",
		CWD:                     "/repo/default",
		RuntimeWorkspaceRoots:   []string{"/repo/default"},
		ReasoningEffort:         &effort,
	}, ThreadReadSnapshot{
		ThreadID:      "thread-read",
		ModelProvider: "provider-read",
		CWD:           "/repo/read",
		Path:          &rollout,
	})

	if session.Model != "" {
		t.Fatalf("model = %q, want cleared when rollout path exists and no read model", session.Model)
	}
	if session.ServiceTier == nil || *session.ServiceTier != "priority" || session.ReasoningEffort == nil || *session.ReasoningEffort != "high" {
		t.Fatalf("defaults not cloned: %#v", session)
	}
	if session.RolloutPath == nil || *session.RolloutPath != "rollout.jsonl" {
		t.Fatalf("rollout path = %#v", session.RolloutPath)
	}
}
