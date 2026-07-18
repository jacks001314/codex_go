package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
)

const defaultRemoteCompactModel = "gpt-5.4-mini"

type agentCompactRunner struct {
	agent      model.AgentRunner
	model      string
	providerID string
}

func (r *agentCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	if r == nil || r.agent == nil {
		return nil, nil
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	history := compactItemsForRemoteRequest(request)
	response, err := r.agent.Run(ctx, &model.AgentRequest{
		Prompt:       strings.TrimSpace(request.Prompt),
		Instructions: remoteCompactInstructions(),
		InputItems:   inputItemsFromCompactItems(history),
		Model:        firstNonEmpty(r.model, defaultRemoteCompactModel),
		ProviderID:   strings.TrimSpace(r.providerID),
		TaskKind:     model.AgentTaskRegular,
		ThreadID:     request.ThreadID,
		TurnID:       request.TurnID,
		Store:        false,
		ClientMetadata: map[string]string{
			"request_kind": "compact",
			"thread_id":    request.ThreadID,
			"turn_id":      request.TurnID,
		},
	})
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(response.Message)
	if summary == "" {
		for i := range response.Items {
			if text := strings.TrimSpace(response.Items[i].Text); text != "" {
				summary = text
				break
			}
		}
	}
	if summary == "" {
		return nil, nil
	}
	return &compact.Result{
		Status:      compact.StatusCompleted,
		Request:     *request,
		Summary:     summary,
		NewHistory:  compact.BuildCompactedHistory(nil, lastUserCompactItems(history, 1), summary),
		CompletedAt: time.Now().UTC(),
		Source:      compact.SourceRemote,
		ResponseID:  response.ResponseID,
		Model:       response.Model,
		ProviderID:  response.ProviderID,
		Usage:       compactUsageFromAgentUsage(&response.Usage),
	}, nil
}

func compactUsageFromAgentUsage(usage *model.AgentUsage) *compact.Usage {
	if usage == nil {
		return nil
	}
	return &compact.Usage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
	}
}

func remoteCompactInstructions() string {
	return strings.TrimSpace(`You are compacting a Codex conversation for future continuation.
Write a concise but high-fidelity summary that preserves:
- the user's objective and constraints
- important decisions and current plan
- files changed or intended changes
- commands run and notable outputs
- unresolved work and risks

Return only the summary.`)
}

func compactItemsForRemoteRequest(request *compact.Request) []compact.Item {
	if request == nil {
		return nil
	}
	history := append([]compact.Item(nil), request.History...)
	if request.MaxHistoryTokens > 0 {
		history = compact.TrimHistoryToTokenBudget(history, request.MaxHistoryTokens)
	}
	return history
}

func inputItemsFromCompactItems(items []compact.Item) []any {
	sessionItems := sessionItemsFromCompactItems(items, time.Now().UTC())
	return session.InputItemsFromItems(sessionItems, &session.HistoryBuildOptions{IncludeToolOutputs: true})
}

func lastUserCompactItems(items []compact.Item, count int) []compact.Item {
	if count <= 0 {
		return nil
	}
	out := make([]compact.Item, 0, count)
	for i := len(items) - 1; i >= 0 && len(out) < count; i-- {
		item := items[i]
		if item.Type == "message" && item.Role == "user" && item.Kind != "compaction_summary" {
			out = append(out, item)
		}
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}
