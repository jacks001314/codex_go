package appserver

import (
	"strings"

	"codex_go/model"
)

// asyncHookContextInputItems drains finished async hook results for the thread
// and returns user-message items carrying their additional context, mirroring
// Rust drain_async_hook_results before the user prompt.
func (r *RuntimeRouter) asyncHookContextInputItems(threadID string) []any {
	if r == nil || !r.hookRunnerConfigured() {
		return nil
	}
	results := r.requireHookRunner().DrainAsyncResults(threadID)
	if len(results) == 0 {
		return nil
	}
	var items []any
	for _, result := range results {
		for _, entry := range result.Run.Entries {
			if entry.Kind == HookOutputContext && strings.TrimSpace(entry.Text) != "" {
				if item := model.UserMessageInputItem(entry.Text); item != nil {
					items = append(items, item)
				}
			}
		}
	}
	return items
}
