package app

// Rust parity: codex-rs/tui/src/task_mentions.rs (discovery half). Mirrors
// task_mentions::spawn_search's server work: a thread/search + thread/list merge
// filtered to same-host, non-ephemeral tasks, ordered so the current directory's
// tasks come first.

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"codex_go/appserver"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/turn"
)

// taskMentionSearchLimit mirrors Rust MAX_SEARCH_RESULTS.
const taskMentionSearchLimit = 50

// taskMentionRequestFunc is the typed app-server request Rust performs through
// AppServerRequestHandle::request_typed.
type taskMentionRequestFunc func(ctx context.Context, method appserver.Method, params any, target any) error

type taskMentionSearchResult struct {
	thread  appserver.Thread
	snippet string
}

// searchTaskMentions mirrors Rust task_mentions::spawn_search's discovery half.
// A nil result means no tasks (both requests failed, or nothing matched).
func searchTaskMentions(ctx context.Context, request taskMentionRequestFunc, query string, currentThreadID string, cwd string) []codextui.TaskMention {
	query = strings.TrimSpace(query)
	if request == nil || query == "" {
		return nil
	}
	limit := taskMentionSearchLimit
	archived := false
	var searchResponse appserver.ThreadSearchResponse
	searchErr := request(ctx, appserver.MethodThreadSearch, appserver.ThreadSearchParams{
		Limit:         &limit,
		SortKey:       appserver.SortRecencyAt,
		SortDirection: appserver.SortDesc,
		Archived:      &archived,
		SearchTerm:    query,
	}, &searchResponse)
	var listResponse appserver.ThreadListResponse
	listErr := request(ctx, appserver.MethodThreadList, appserver.ThreadListParams{
		Limit:          &limit,
		SortKey:        appserver.SortUpdatedAt,
		SortDirection:  appserver.SortDesc,
		Archived:       &archived,
		UseStateDBOnly: true,
	}, &listResponse)
	if searchErr != nil && listErr != nil {
		return nil
	}
	results := make([]taskMentionSearchResult, 0, len(searchResponse.Data)+len(listResponse.Data))
	seen := map[string]bool{}
	if searchErr == nil {
		for _, result := range searchResponse.Data {
			results = append(results, taskMentionSearchResult{thread: result.Thread, snippet: result.Snippet})
			seen[result.Thread.ID] = true
		}
	}
	if listErr == nil {
		// Titled threads not already returned by the search contribute an empty
		// snippet (Rust's second request is a title search).
		for _, thread := range listResponse.Data {
			if seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			results = append(results, taskMentionSearchResult{thread: thread})
		}
	}
	matches := make([]codextui.TaskMention, 0, len(results))
	for _, result := range results {
		thread := result.thread
		if thread.ID == currentThreadID || thread.Ephemeral {
			continue
		}
		if thread.ThreadSource != nil && string(*thread.ThreadSource) == "ambient_suggestions" {
			continue
		}
		title := taskMentionTitle(&thread)
		if title == "" {
			continue
		}
		matches = append(matches, codextui.TaskMention{
			ThreadID: thread.ID,
			Title:    title,
			CWD:      thread.CWD,
			Snippet:  result.snippet,
		})
		if len(matches) == taskMentionSearchLimit {
			break
		}
	}
	return orderTaskMentionsByCWD(matches, cwd)
}

// taskMentionTitle mirrors Rust's name -> preview -> id fallback.
func taskMentionTitle(thread *appserver.Thread) string {
	if thread == nil {
		return ""
	}
	candidates := make([]string, 0, 3)
	if thread.Name != nil {
		candidates = append(candidates, *thread.Name)
	}
	candidates = append(candidates, thread.Preview, thread.ID)
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// orderTaskMentionsByCWD mirrors Rust's stable sort_by_key: a task whose cwd is
// the current directory or a descendant sorts first; everything else keeps its
// relative order.
func orderTaskMentionsByCWD(matches []codextui.TaskMention, cwd string) []codextui.TaskMention {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || len(matches) < 2 {
		return matches
	}
	windowsCWD := len(cwd) > 1 && cwd[1] == ':' || strings.HasPrefix(cwd, `\\`)
	sort.SliceStable(matches, func(i, j int) bool {
		return taskMentionUnderCWD(matches[i].CWD, cwd, windowsCWD) && !taskMentionUnderCWD(matches[j].CWD, cwd, windowsCWD)
	})
	return matches
}

// taskMentionUnderCWD mirrors Rust's key closure, including the byte-boundary
// `get` and the Windows case-insensitive prefix rule.
func taskMentionUnderCWD(taskCWD string, cwd string, windowsCWD bool) bool {
	if !codextui.UTF8Boundary(taskCWD, len(cwd)) {
		return false
	}
	prefix := taskCWD[:len(cwd)]
	suffix := taskCWD[len(cwd):]
	if prefix != cwd && !(windowsCWD && strings.EqualFold(prefix, cwd)) {
		return false
	}
	return suffix == "" || strings.HasPrefix(suffix, "/") || strings.HasPrefix(suffix, `\`)
}

// applySubmitTaskReferences mirrors the Rust chatwidget submit path
// (`task_mentions::apply_task_references(&mut items, &mention_bindings,
// self.thread_id)`): the composer's encoded mention bindings add the bounded
// referenced-chat context and re-encode visible task mentions as guarded links.
func applySubmitTaskReferences(inputs []turn.TurnUserInput, encodedBindings []string, currentThreadID string) []turn.TurnUserInput {
	if len(inputs) == 0 || len(encodedBindings) == 0 {
		return inputs
	}
	bindings := make([]bottompane.MentionBinding, 0, len(encodedBindings))
	for _, encoded := range encodedBindings {
		if binding, ok := decodeTaskMentionBinding(encoded); ok {
			bindings = append(bindings, binding)
		}
	}
	if len(bindings) == 0 {
		return inputs
	}
	bottompane.ApplyTaskReferences(inputs, bindings, strings.TrimSpace(currentThreadID))
	return inputs
}

// decodeTaskMentionBinding decodes the composer's `<sigil><mention>|<path>`
// encoding (tui/tea addComposerMentionBinding) into a MentionBinding. Bindings
// whose path is not a thread link are still returned so the apply loop can
// consume them in order, matching Rust.
func decodeTaskMentionBinding(encoded string) (bottompane.MentionBinding, bool) {
	insert, path, ok := strings.Cut(strings.TrimSpace(encoded), "|")
	if !ok {
		return bottompane.MentionBinding{}, false
	}
	insert = strings.TrimSpace(insert)
	path = strings.TrimSpace(path)
	if insert == "" || path == "" {
		return bottompane.MentionBinding{}, false
	}
	sigil, size := utf8.DecodeRuneInString(insert)
	if size == 0 || size >= len(insert) {
		return bottompane.MentionBinding{}, false
	}
	return bottompane.MentionBinding{Sigil: sigil, Mention: insert[size:], Path: path}, true
}
