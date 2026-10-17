package appserver

import (
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
)

func guardianNotificationTestRouter(t *testing.T, strict bool) (*RuntimeRouter, session.ThreadID, session.ThreadID) {
	t.Helper()
	home := t.TempDir()
	body := "model = \"gpt-5\"\napprovals_reviewer = \"auto_review\"\n[features]\nmulti_agent_v2 = true\n"
	if strict {
		body += "[auto_review]\ncircuit_break_action = \"strict\"\n"
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parentID := session.ThreadID("thread-parent")
	childID := session.ThreadID("thread-child")
	for _, record := range []*session.Record{
		{
			ID: parentID, SessionID: string(parentID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: t.TempDir(), AgentPath: "/root"},
		},
		{
			ID: childID, SessionID: string(childID), ParentThreadID: parentID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: t.TempDir(), AgentPath: "/root/worker", AgentDepth: 1},
		},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	t.Cleanup(func() { _ = router.Close() })
	return router, parentID, childID
}

func guardianNotificationTexts(items []any) []string {
	var texts []string
	for _, item := range items {
		message, _ := item.(map[string]any)
		content, _ := message["content"].([]any)
		for _, part := range content {
			block, _ := part.(map[string]any)
			if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

// TestGuardianSubagentInterruptionNotifiesParentOnlyInStrictModeLikeRust pins
// Rust #49312: a strict circuit-break interruption reaches the parent with
// Guardian guidance, while the default mode stays silent.
func TestGuardianSubagentInterruptionNotifiesParentOnlyInStrictModeLikeRust(t *testing.T) {
	router, parentID, childID := guardianNotificationTestRouter(t, true)
	router.interruptTurnForGuardianCircuitBreaker(string(childID), "turn-child", "The action was denied three times.")
	texts := guardianNotificationTexts(router.drainRuntimeAgentMessages(string(parentID)))
	if len(texts) != 1 {
		t.Fatalf("parent notifications = %#v, want one", texts)
	}
	for _, want := range []string{
		"Agent interrupted by Guardian: The action was denied three times.",
		agent.GuardianNextAction,
	} {
		if !strings.Contains(texts[0], want) {
			t.Fatalf("notification missing %q: %s", want, texts[0])
		}
	}

	defaultRouter, defaultParent, defaultChild := guardianNotificationTestRouter(t, false)
	defaultRouter.interruptTurnForGuardianCircuitBreaker(string(defaultChild), "turn-child", "denied")
	if texts := guardianNotificationTexts(defaultRouter.drainRuntimeAgentMessages(string(defaultParent))); len(texts) != 0 {
		t.Fatalf("default mode notified the parent: %#v", texts)
	}
}
