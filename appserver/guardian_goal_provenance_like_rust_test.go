package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Mirrors Rust #57430: a goal set after a worker has already produced its first
// Guardian review reaches that worker's later review root authorization only
// with user provenance, and never appears in the earlier review.
func TestGuardianRootAuthorizationIncludesLaterUserGoalLikeRust(t *testing.T) {
	user := ThreadGoalMutationOriginUser
	automatic := ThreadGoalMutationOriginAutomatic
	paused := GoalPaused
	const workerID = "goal-tool-worker"
	for _, test := range []struct {
		name   string
		origin *ThreadGoalMutationOrigin
		want   bool
	}{
		{name: "user goal", origin: &user, want: true},
		{name: "automatic goal is not authorization", origin: &automatic, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, _, rootID := newGoalToolTestRouter(t)
			objective := "Publish the approved report using the configured MCP tool."
			now := time.Date(2026, 7, 31, 8, 0, 1, 0, time.UTC)
			if err := router.services.ThreadRouter.store.Create(&session.Record{
				ID:             session.ThreadID(workerID),
				SessionID:      rootID,
				ParentThreadID: session.ThreadID(rootID),
				CreatedAt:      now,
				UpdatedAt:      now,
				RecencyAt:      now,
				Metadata: session.Metadata{
					CWD:               t.TempDir(),
					ModelProvider:     "openai",
					AgentPath:         "/root/worker",
					AgentDepth:        1,
					MultiAgentVersion: "v2",
				},
			}); err != nil {
				t.Fatalf("create worker: %v", err)
			}
			rootAuthorization := func() string {
				var builder strings.Builder
				for _, message := range router.guardianRootConversationWithHandoff(workerID, true, "/root/worker", "collaboration") {
					builder.WriteString(message.Render())
				}
				return builder.String()
			}
			if first := rootAuthorization(); strings.Contains(first, objective) {
				t.Fatalf("first review already carried the later goal: %s", first)
			}

			if _, _, _, err := router.setStateThreadGoal(&GoalSetParams{
				ThreadID: rootID, Objective: &objective, Status: &paused, Origin: test.origin,
			}); err != nil {
				t.Fatalf("set goal: %v", err)
			}

			second := rootAuthorization()
			if strings.Contains(second, objective) != test.want {
				t.Fatalf("later review goal presence = %t, want %t: %s", strings.Contains(second, objective), test.want, second)
			}
			if test.want && !strings.Contains(second, "User set the goal:") {
				t.Fatalf("root authorization did not mark the goal as user-set: %s", second)
			}
		})
	}
}
