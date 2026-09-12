package auth

import "testing"

func chatGPTChangeStateAuth(user string, workspace string, token string) *AuthDotJSON {
	tokens := map[string]any{"access_token": token}
	if user != "" {
		tokens["chatgpt_user_id"] = user
	}
	if workspace != "" {
		tokens["account_id"] = workspace
	}
	return &AuthDotJSON{AuthMode: "chatgpt", Tokens: tokens}
}

func TestAuthChangeTrackerDistinguishesRefreshesFromIdentityChanges(t *testing.T) {
	// Rust constructs this manager with no cached auth, so the first credential
	// set is generation 1 / owner generation 1.
	tracker := NewAuthChangeTracker(nil)

	cases := []struct {
		name            string
		auth            *AuthDotJSON
		generation      uint64
		ownerGeneration uint64
	}{
		{"first credential", chatGPTChangeStateAuth("user-a", "workspace-a", "token-1"), 1, 1},
		{"unchanged", chatGPTChangeStateAuth("user-a", "workspace-a", "token-1"), 1, 1},
		{"token refresh", chatGPTChangeStateAuth("user-a", "workspace-a", "token-2"), 2, 1},
		{"user change", chatGPTChangeStateAuth("user-b", "workspace-a", "token-3"), 3, 2},
		{"workspace change", chatGPTChangeStateAuth("user-b", "workspace-b", "token-4"), 4, 3},
		{"incomplete user", chatGPTChangeStateAuth("", "workspace-b", "token-5"), 5, 4},
		{"still incomplete", chatGPTChangeStateAuth("", "workspace-b", "token-6"), 6, 5},
		{"api key switch", &AuthDotJSON{OpenAIAPIKey: "key-1"}, 7, 6},
		{"api key change", &AuthDotJSON{OpenAIAPIKey: "key-2"}, 8, 7},
	}
	for _, tc := range cases {
		state := tracker.NoteAuth(tc.auth)
		if state.Generation != tc.generation || state.OwnerGeneration != tc.ownerGeneration {
			t.Fatalf("%s: state = %+v, want generation=%d owner=%d", tc.name, state, tc.generation, tc.ownerGeneration)
		}
	}
}

func TestAuthChangeTrackerPreservesLogoutAndSwitchRevisions(t *testing.T) {
	initial := chatGPTChangeStateAuth("user-a", "workspace-a", "token-1")
	tracker := NewAuthChangeTracker(initial)

	if state := tracker.NoteAuth(nil); state.Generation != 1 || state.OwnerGeneration != 1 {
		t.Fatalf("logout state = %+v, want {1 1}", state)
	}
	if state := tracker.NoteAuth(initial); state.Generation != 2 || state.OwnerGeneration != 2 {
		t.Fatalf("re-login state = %+v, want {2 2}", state)
	}
	tracker.NoteAuth(chatGPTChangeStateAuth("user-b", "workspace-a", "token-2"))
	if state := tracker.NoteAuth(initial); state.Generation != 4 || state.OwnerGeneration != 4 {
		t.Fatalf("switch-back state = %+v, want {4 4}", state)
	}
}

func TestSameAuthOwnerRequiresCompleteIdentity(t *testing.T) {
	base := chatGPTChangeStateAuth("user-a", "workspace-a", "token-1")
	if !SameAuthOwner(base, chatGPTChangeStateAuth("user-a", "workspace-a", "token-2")) {
		t.Fatal("same user/workspace should keep ownership across a token refresh")
	}
	if SameAuthOwner(base, chatGPTChangeStateAuth("user-b", "workspace-a", "token-1")) {
		t.Fatal("user change should change ownership")
	}
	if SameAuthOwner(base, chatGPTChangeStateAuth("user-a", "workspace-b", "token-1")) {
		t.Fatal("workspace change should change ownership")
	}
	if SameAuthOwner(base, chatGPTChangeStateAuth("", "workspace-a", "token-1")) {
		t.Fatal("incomplete identity should not keep ownership")
	}
	if SameAuthOwner(base, &AuthDotJSON{OpenAIAPIKey: "key-1"}) {
		t.Fatal("auth mode change should change ownership")
	}
	if SameAuthOwner(nil, base) || SameAuthOwner(base, nil) {
		t.Fatal("missing snapshots should not share an owner")
	}
}

func TestAuthChangeTrackerForceOwnerChangeAndSeed(t *testing.T) {
	tracker := NewAuthChangeTracker(nil)
	tracker.SeedLastAuth(chatGPTChangeStateAuth("user-a", "workspace-a", "token-1"))
	if state := tracker.NoteAuth(chatGPTChangeStateAuth("user-a", "workspace-a", "token-1")); state.Generation != 0 {
		t.Fatalf("seeded unchanged state = %+v, want no revision advance", state)
	}
	if state := tracker.ForceOwnerChange(); state.Generation != 1 || state.OwnerGeneration != 1 {
		t.Fatalf("force owner change = %+v, want {1 1}", state)
	}
}
