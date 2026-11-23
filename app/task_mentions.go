package app

// Rust parity: codex-rs/tui/src/task_mentions.rs (discovery half). Mirrors
// task_mentions::spawn_search's server work: two state-DB thread/list requests
// (a title filter plus the recent list) merged by thread id, filtered to
// same-host, non-ephemeral tasks, and ordered so the current directory's tasks
// come first (Rust #62691).

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codex_go/appserver"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/turn"
)

// taskMentionSearchLimit mirrors Rust MAX_SEARCH_RESULTS.
const taskMentionSearchLimit = 50

// taskMentionSearchTimeout mirrors Rust task_mentions::SEARCH_TIMEOUT: a task
// search that outlives the query that triggered it must not hold the caller (or
// its connection) open (Rust #62132).
const taskMentionSearchTimeout = 10 * time.Second

// errTaskMentionCloudTitleFilterUnsupported marks the title filter the hosted
// Cloud backend does not implement. Rust #62691 keeps the recent list for
// AppServerRequestHandle::Cloud and skips the filtered request.
var errTaskMentionCloudTitleFilterUnsupported = errors.New("cloud app server does not implement thread/list title filters")

// taskMentionRequestFunc is the typed app-server request Rust performs through
// AppServerRequestHandle::request_typed.
type taskMentionRequestFunc func(ctx context.Context, method appserver.Method, params any, target any) error

// taskMentionSearchRunner issues the title-filtered page and the recent page
// Rust joins in task_mentions::spawn_search, each under taskMentionSearchTimeout.
// The remote TUI supplies a runner that multiplexes both replies on one
// connection; taskMentionRequestRunner backs test doubles and in-process
// handles.
type taskMentionSearchRunner func(
	ctx context.Context,
	recent appserver.ThreadListParams,
	filtered appserver.ThreadListParams,
) (
	recentResponse appserver.ThreadListResponse,
	recentErr error,
	filteredResponse appserver.ThreadListResponse,
	filteredErr error,
)

// searchTaskMentions mirrors Rust task_mentions::spawn_search's discovery half.
// A nil result means no tasks (both requests failed, or nothing matched).
func searchTaskMentions(ctx context.Context, request taskMentionRequestFunc, query string, currentThreadID string, cwd string) []codextui.TaskMention {
	if request == nil {
		return nil
	}
	return searchTaskMentionsWithRunner(ctx, taskMentionRequestRunner(request), query, currentThreadID, cwd)
}

// taskMentionRequestRunner joins the two thread/list requests through request.
// The callback must tolerate concurrent use: Rust issues both typed requests at
// once through one app-server request handle.
func taskMentionRequestRunner(request taskMentionRequestFunc) taskMentionSearchRunner {
	return func(ctx context.Context, recentParams appserver.ThreadListParams, filteredParams appserver.ThreadListParams) (
		appserver.ThreadListResponse, error, appserver.ThreadListResponse, error,
	) {
		var recentResponse appserver.ThreadListResponse
		var filteredResponse appserver.ThreadListResponse
		var recentErr error
		var filteredErr error
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			recentCtx, cancel := context.WithTimeout(ctx, taskMentionSearchTimeout)
			defer cancel()
			recentErr = request(recentCtx, appserver.MethodThreadList, recentParams, &recentResponse)
		}()
		go func() {
			defer wait.Done()
			filteredCtx, cancel := context.WithTimeout(ctx, taskMentionSearchTimeout)
			defer cancel()
			filteredErr = request(filteredCtx, appserver.MethodThreadList, filteredParams, &filteredResponse)
		}()
		wait.Wait()
		return recentResponse, recentErr, filteredResponse, filteredErr
	}
}

// searchTaskMentionsWithRunner builds Rust #62691's title-filtered and recent
// thread/list pair, merges them by thread id with the filtered page first, and
// keeps the recent list when the title filter is unavailable or times out.
func searchTaskMentionsWithRunner(ctx context.Context, run taskMentionSearchRunner, query string, currentThreadID string, cwd string) []codextui.TaskMention {
	query = strings.TrimSpace(query)
	if run == nil || query == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limit := taskMentionSearchLimit
	archived := false
	recentParams := appserver.ThreadListParams{
		CurrentAccountOnly: true,
		Limit:              &limit,
		SortKey:            appserver.SortUpdatedAt,
		SortDirection:      appserver.SortDesc,
		Archived:           &archived,
		UseStateDBOnly:     true,
	}
	filteredParams := recentParams
	filteredParams.SearchTerm = &query
	recentResponse, recentErr, filteredResponse, filteredErr := run(ctx, recentParams, filteredParams)
	if recentErr != nil && filteredErr != nil {
		return nil
	}
	// Rust #62691 starts from the filtered page and appends recent threads that
	// it did not already contain, so a full filtered page cannot crowd out the
	// recent candidates.
	threads := make([]appserver.Thread, 0, len(filteredResponse.Data)+len(recentResponse.Data))
	seen := map[string]bool{}
	if filteredErr == nil {
		for _, thread := range filteredResponse.Data {
			if seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			threads = append(threads, thread)
		}
	}
	if recentErr == nil {
		for _, thread := range recentResponse.Data {
			if seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			threads = append(threads, thread)
		}
	}
	matches := make([]codextui.TaskMention, 0, len(threads))
	for _, thread := range threads {
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
		})
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
