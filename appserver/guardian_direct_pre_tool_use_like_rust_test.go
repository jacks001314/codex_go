package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
)

// Rust #58357/#58368: direct Guardian review applies the shared PreToolUse
// hook handling before it evaluates the approval requirement. A rewrite changes
// the policy-checked and reviewed command, and a hook denial rejects a call the
// policy would otherwise allow, without asking the reviewer.
func TestThreadGuardianReviewAppliesPreToolUseHookLikeRust(t *testing.T) {
	newRouter := func(t *testing.T, arguments string, hookOutput string, reviewer GuardianReviewer) (*RuntimeRouter, *string) {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		hooksDir := filepath.Join(cwd, ".gcode")
		if err := os.MkdirAll(hooksDir, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		hooksJSON, err := json.Marshal(map[string]any{
			"hooks": map[string]any{
				"PreToolUse": []any{map[string]any{
					"matcher": "^Bash$",
					"hooks":   []any{map[string]any{"type": "command", "command": hookRunnerOutputCommand(hookOutput, "")}},
				}},
			},
		})
		if err != nil {
			t.Fatalf("marshal hooks: %v", err)
		}
		if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
			t.Fatalf("write hooks: %v", err)
		}
		now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
		payload, err := json.Marshal(map[string]any{
			"type":      "function_call",
			"name":      "exec_command",
			"call_id":   "hook-review",
			"arguments": arguments,
		})
		if err != nil {
			t.Fatalf("marshal recorded call: %v", err)
		}
		item, err := sessionItemFromRaw(json.RawMessage(payload), now, 0)
		if err != nil {
			t.Fatalf("sessionItemFromRaw() error = %v", err)
		}
		store := session.NewStore(t.TempDir())
		if err := store.Create(&session.Record{
			ID:        session.ThreadID("guardian-hook-thread"),
			SessionID: "guardian-hook-thread",
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  session.Metadata{CWD: cwd, Source: "cli"},
			Items:     []session.Item{item},
		}); err != nil {
			t.Fatalf("create thread: %v", err)
		}
		services := RuntimeServices{
			ThreadRouter:     NewRouter(store),
			DefaultCWD:       cwd,
			Config:           config.NewConfigService(home),
			HooksDiscovery:   NewHookDiscoveryService(home),
			HookRunner:       NewHookRunner(),
			GuardianReviewer: reviewer,
		}
		router := NewRuntimeRouter(services)
		router.rememberConnectionClientInfo(defaultRequestConnectionID, ClientInfo{Name: "test", Version: "1"})
		return router, &cwd
	}

	t.Run("rewrite reaches the reviewer", func(t *testing.T) {
		reviewer := &assessmentGuardianReviewer{
			decision:   state.DecisionApproved,
			assessment: &state.Assessment{Outcome: state.OutcomeAllow, RiskLevel: state.RiskLow, UserAuthorization: state.AuthorizationHigh, Rationale: "rewritten"},
		}
		router, cwd := newRouter(t,
			`{"cmd":"git init guardian-original","sandbox_permissions":"require_escalated","justification":"Create the requested repository."}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"git init guardian-rewritten"}}}`,
			reviewer,
		)
		response := router.Handle(guardianReviewRequest(t, "guardian-hook-thread"))
		if response.Error != nil {
			t.Fatalf("thread/guardianReview error = %+v", response.Error)
		}
		called, action, _, _ := reviewer.snapshot()
		if !called {
			t.Fatal("the rewritten command requires approval but the reviewer was not called")
		}
		if !strings.Contains(action.Command, "git init guardian-rewritten") || strings.Contains(action.Command, "guardian-original") {
			t.Fatalf("reviewed command = %q, want the hook rewrite", action.Command)
		}
		for _, name := range []string{"guardian-original", "guardian-rewritten"} {
			if _, err := os.Stat(filepath.Join(*cwd, name)); !os.IsNotExist(err) {
				t.Fatalf("direct review executed %q", name)
			}
		}
	})

	t.Run("denial beats a policy skip", func(t *testing.T) {
		reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
		router, _ := newRouter(t,
			`{"cmd":"git init guardian-original"}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"blocked by direct-review hook"}}`,
			reviewer,
		)
		response := router.Handle(guardianReviewRequest(t, "guardian-hook-thread"))
		if response.Error == nil || !strings.Contains(response.Error.Message, "blocked by direct-review hook") {
			t.Fatalf("response = %+v, want the hook denial", response)
		}
		if called, _, _, _ := reviewer.snapshot(); called {
			t.Fatal("a denied call reached the reviewer")
		}
	})
}
