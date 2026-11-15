package appserver

import (
	"strings"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

const (
	contextWindowOpenTag  = "<context_window>"
	contextWindowCloseTag = "</context_window>"
	contextWindowKind     = "token_budget.context_window"
)

// contextWindowInputItemForTurn renders the context-window identity and its
// hints (Rust #61452). The default shape is a developer message wrapped in
// <context_window> markers; `context_window_as_tool_output` renders the same
// text as an `agent.thread_hint` function-call output instead.
func (r *RuntimeRouter) contextWindowInputItemForTurn(
	threadID string,
	cfg *config.Config,
	params *turn.TurnStartParams,
	info *model.ModelInfo,
	budget *config.TokenBudgetConfig,
) any {
	if r == nil || budget == nil || !budget.Enabled || info == nil {
		return nil
	}
	if info.ContextWindow <= 0 && info.MaxContextWindow <= 0 {
		return nil
	}
	agentName := "/root"
	if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false); err == nil && record != nil {
		if path := strings.TrimSpace(record.Metadata.AgentPath); path != "" {
			agentName = path
		}
	}
	ids := r.autoCompactWindowIDsForThread(threadID)
	description := contextWindowDescription(agentName, ids)
	hints := make([]string, 0, 1)
	if budget.UseHistoryNotesExtension {
		if hint, ok := r.historyNotesThreadHintText(cfg, params, threadID); ok {
			hints = append(hints, hint)
		}
	}
	if budget.ContextWindowAsToolOutput {
		text := description
		for _, hint := range hints {
			text += "\n" + hint
		}
		return map[string]any{
			"type":      "function_call_output",
			"name":      "thread_hint",
			"namespace": "agent",
			"output":    text,
		}
	}
	body := description
	if len(hints) > 0 {
		body += "\n" + strings.Join(hints, "\n")
	}
	fragment := contextfrag.NewSimpleFragmentWithKind(
		contextfrag.RoleDeveloper,
		contextWindowOpenTag,
		contextWindowCloseTag,
		"\n"+body+"\n",
		contextWindowKind,
	)
	return buildRenderedMessage(contextfrag.Render(fragment))
}

func contextWindowDescription(agentName string, ids autoCompactWindowIDs) string {
	lines := []string{
		"Agent name: " + agentName,
		"First context window id: " + ids.First,
		"Current context window id: " + ids.Current,
	}
	if ids.Previous != "" {
		lines = append(lines, "Previous context window id: "+ids.Previous)
	}
	return strings.Join(lines, "\n")
}
