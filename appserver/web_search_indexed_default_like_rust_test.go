package appserver

import (
	"net/http"
	"testing"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

// TestWebSearchModeForTurnAppliesPermissionProfileLikeRust mirrors Rust #62894
// resolve_web_search_mode_for_turn: the turn's permission profile, the managed
// requirements and the explicit setting decide whether an indexed default is
// promoted to live.
func TestWebSearchModeForTurnAppliesPermissionProfileLikeRust(t *testing.T) {
	cwd := t.TempDir()
	restricted := &config.Config{Values: map[string]any{}}
	fullAccess := &config.Config{Values: map[string]any{"sandbox_mode": "danger-full-access"}}
	managed := &config.Config{
		Values: map[string]any{"sandbox_mode": "danger-full-access"},
		Requirements: &config.ConfigRequirements{
			AllowedWebSearchModes: []config.WebSearchMode{config.WebSearchMode("cached"), config.WebSearchMode("indexed")},
		},
	}
	params := &turn.TurnStartParams{CWD: cwd}
	for _, test := range []struct {
		name       string
		cfg        *config.Config
		preferred  codexapi.WebSearchMode
		explicit   bool
		fullAccess bool
		want       codexapi.WebSearchMode
	}{
		{
			name:      "restricted turn keeps indexed default",
			cfg:       restricted,
			preferred: codexapi.WebSearchModeIndexed,
			want:      codexapi.WebSearchModeIndexed,
		},
		{
			name:       "full access promotes the implicit indexed default",
			cfg:        fullAccess,
			preferred:  codexapi.WebSearchModeIndexed,
			fullAccess: true,
			want:       codexapi.WebSearchModeLive,
		},
		{
			name:       "full access promotes cached",
			cfg:        fullAccess,
			preferred:  codexapi.WebSearchModeCached,
			fullAccess: true,
			want:       codexapi.WebSearchModeLive,
		},
		{
			name:       "explicit indexed keeps precedence",
			cfg:        fullAccess,
			preferred:  codexapi.WebSearchModeIndexed,
			explicit:   true,
			fullAccess: true,
			want:       codexapi.WebSearchModeIndexed,
		},
		{
			name:       "managed requirements keep indexed when live is disallowed",
			cfg:        managed,
			preferred:  codexapi.WebSearchModeIndexed,
			fullAccess: true,
			want:       codexapi.WebSearchModeIndexed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := webSearchModeForTurn(test.cfg, params, test.preferred, test.explicit, true)
			if got != test.want {
				t.Fatalf("webSearchModeForTurn() = %q, want %q", got, test.want)
			}
			// The resolved mode follows the sandbox profile only; the approval policy
			// is untouched (Rust matches PermissionProfile::Disabled).
			if gotFull := turnUsesFullAccessPermissionProfile(test.cfg, cwd, params); gotFull != test.fullAccess {
				t.Fatalf("turnUsesFullAccessPermissionProfile() = %v, want %v", gotFull, test.fullAccess)
			}
		})
	}
}

// TestStandaloneWebSearchAdvertisesIndexedDefaultLikeRust mirrors Rust #62894's
// app-server test: a restricted turn advertises indexed external web access,
// while a full-access turn advertises live. The permission change applies to the
// next turn because each turn resolves its own snapshot.
func TestStandaloneWebSearchAdvertisesIndexedDefaultLikeRust(t *testing.T) {
	router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{})
	})
	baseValues := func() map[string]any {
		return map[string]any{
			"features": map[string]any{
				"standalone_web_search":      true,
				"web_search_indexed_default": true,
			},
		}
	}
	cfg := &config.Config{Values: baseValues()}
	options, err := router.webSearchOptionsForTurn(cfg, &turn.TurnStartParams{ThreadID: "thread-1", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("restricted webSearchOptionsForTurn() error = %v", err)
	}
	if options == nil {
		t.Fatal("restricted turn did not register standalone web search")
	}
	access := options.Settings.ExternalWebAccess
	if access == nil || access.Mode == nil || *access.Mode != codexapi.ExternalWebIndexed {
		t.Fatalf("restricted external_web_access = %#v, want indexed", access)
	}

	fullAccessConfig := &config.Config{Values: baseValues()}
	fullAccessConfig.Values["sandbox_mode"] = "danger-full-access"
	options, err = router.webSearchOptionsForTurn(fullAccessConfig, &turn.TurnStartParams{
		ThreadID:      "thread-2",
		CWD:           t.TempDir(),
		SandboxPolicy: sandbox.NewDangerFullAccessPolicy(),
	})
	if err != nil {
		t.Fatalf("full-access webSearchOptionsForTurn() error = %v", err)
	}
	if options == nil {
		t.Fatal("full-access turn did not register standalone web search")
	}
	access = options.Settings.ExternalWebAccess
	if access == nil || access.Boolean == nil || !*access.Boolean {
		t.Fatalf("full-access external_web_access = %#v, want live", access)
	}

	explicit := &config.Config{Values: baseValues()}
	explicit.Values["web_search"] = "indexed"
	options, err = router.webSearchOptionsForTurn(explicit, &turn.TurnStartParams{
		ThreadID:      "thread-3",
		CWD:           t.TempDir(),
		SandboxPolicy: sandbox.NewDangerFullAccessPolicy(),
	})
	if err != nil {
		t.Fatalf("explicit indexed webSearchOptionsForTurn() error = %v", err)
	}
	if options == nil {
		t.Fatal("explicit indexed turn did not register standalone web search")
	}
	access = options.Settings.ExternalWebAccess
	if access == nil || access.Mode == nil || *access.Mode != codexapi.ExternalWebIndexed {
		t.Fatalf("explicit indexed external_web_access = %#v, want indexed", access)
	}
}
