package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/turn"
)

func fmtArgs(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func taskMentionTestThread(id string, name string, preview string, cwd string) appserver.Thread {
	thread := appserver.Thread{ID: id, Preview: preview, CWD: cwd}
	if name != "" {
		thread.Name = &name
	}
	return thread
}

// TestSearchTaskMentionsMatchesRust covers Rust #62691's discovery half: the
// state-DB title filter and recent thread/list request shape, merge/dedupe with
// the filtered page first, filtering, title fallback, and current-cwd-first
// ordering.
func TestSearchTaskMentionsMatchesRust(t *testing.T) {
	var mu sync.Mutex
	var searchTerms []string
	// The two listings run concurrently, so the fake records problems instead
	// of failing from a worker goroutine.
	var problems []string
	note := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, fmtArgs(format, args...))
	}
	recordTerm := func(term string) {
		mu.Lock()
		defer mu.Unlock()
		searchTerms = append(searchTerms, term)
	}
	request := func(_ context.Context, method appserver.Method, params any, target any) error {
		if method != appserver.MethodThreadList {
			note("unexpected method %s", method)
			return nil
		}
		list := params.(appserver.ThreadListParams)
		if list.Limit == nil || *list.Limit != 50 || list.SortKey != appserver.SortUpdatedAt ||
			list.SortDirection != appserver.SortDesc || list.Archived == nil || *list.Archived ||
			!list.UseStateDBOnly || list.CWD != nil {
			note("thread/list params = %#v", list)
		}
		if list.SearchTerm == nil {
			recordTerm("")
			*(target.(*appserver.ThreadListResponse)) = appserver.ThreadListResponse{Data: []appserver.Thread{
				taskMentionTestThread("match", "Named task", "preview", `/repo`), // duplicate is skipped
				taskMentionTestThread("listonly", "Listed task", "listed preview", `/repo`),
			}}
			return nil
		}
		recordTerm(*list.SearchTerm)
		if *list.SearchTerm != "query" {
			note("title filter = %q", *list.SearchTerm)
		}
		*(target.(*appserver.ThreadListResponse)) = appserver.ThreadListResponse{Data: []appserver.Thread{
			taskMentionTestThread("match", "Named task", "preview", `/repo`),
			taskMentionTestThread("self", "Current", "preview", `/repo`),
			taskMentionTestThread("ephemeral", "Ephemeral", "preview", `/repo`),
			taskMentionTestThread("ambient", "Ambient", "preview", `/repo`),
			taskMentionTestThread("descendant", "", "Descendant preview", `/repo/sub`),
			taskMentionTestThread("elsewhere", "", "", `D:\other`),
		}}
		return nil
	}

	// The ambient thread must carry the feature thread-source marker; the
	// ephemeral marker rides the same filtered page.
	ambient := appserver.ThreadSource("ambient_suggestions")
	ephemeral := true
	requestWithMarkers := func(ctx context.Context, method appserver.Method, params any, target any) error {
		err := request(ctx, method, params, target)
		if err == nil && method == appserver.MethodThreadList {
			response := target.(*appserver.ThreadListResponse)
			for index := range response.Data {
				switch response.Data[index].ID {
				case "ambient":
					response.Data[index].ThreadSource = &ambient
				case "ephemeral":
					response.Data[index].Ephemeral = ephemeral
				}
			}
		}
		return err
	}

	matches := searchTaskMentions(context.Background(), requestWithMarkers, " query ", "self", `/repo`)
	mu.Lock()
	terms := append([]string(nil), searchTerms...)
	recorded := append([]string(nil), problems...)
	mu.Unlock()
	if len(recorded) > 0 {
		t.Fatalf("request problems: %s", strings.Join(recorded, "; "))
	}
	sort.Strings(terms)
	if strings.Join(terms, ",") != ",query" {
		t.Fatalf("title filters = %#v, want one recent and one filtered request", terms)
	}
	gotIDs := make([]string, 0, len(matches))
	for _, match := range matches {
		gotIDs = append(gotIDs, match.ThreadID)
	}
	wantIDs := []string{"match", "descendant", "listonly", "elsewhere"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("task ids = %#v, want %#v", gotIDs, wantIDs)
	}
	if matches[0].Title != "Named task" || matches[0].CWD != `/repo` {
		t.Fatalf("filtered match = %#v", matches[0])
	}
	if matches[1].Title != "Descendant preview" {
		t.Fatalf("preview fallback = %#v", matches[1])
	}
	if matches[3].Title != "elsewhere" {
		t.Fatalf("id fallback = %#v", matches[3])
	}

	// An empty query issues no requests.
	mu.Lock()
	searchTerms = nil
	mu.Unlock()
	if matches := searchTaskMentions(context.Background(), requestWithMarkers, "  ", "self", `/repo`); matches != nil {
		t.Fatalf("empty query matches = %#v", matches)
	}
	mu.Lock()
	terms = append([]string(nil), searchTerms...)
	mu.Unlock()
	if len(terms) != 0 {
		t.Fatalf("empty query issued requests: %#v", terms)
	}
	// Both requests failing yields no tasks instead of an error.
	failing := func(context.Context, appserver.Method, any, any) error { return errors.New("offline") }
	if matches := searchTaskMentions(context.Background(), failing, "query", "self", `/repo`); matches != nil {
		t.Fatalf("offline matches = %#v", matches)
	}
}

// TestSearchTaskMentionsKeepsRecentWhenFilteredPageIsFullLikeRust covers Rust
// #62691: a full title-filtered page cannot crowd out the recent candidates.
func TestSearchTaskMentionsKeepsRecentWhenFilteredPageIsFullLikeRust(t *testing.T) {
	filtered := make([]appserver.Thread, 0, taskMentionSearchLimit)
	for index := 0; index < taskMentionSearchLimit; index++ {
		filtered = append(filtered, taskMentionTestThread(fmtArgs("filtered-%d", index), fmtArgs("Filtered %d", index), "", "/repo"))
	}
	request := func(_ context.Context, method appserver.Method, params any, target any) error {
		if method != appserver.MethodThreadList {
			return errors.New("unexpected method")
		}
		list := params.(appserver.ThreadListParams)
		response := target.(*appserver.ThreadListResponse)
		if list.SearchTerm != nil {
			response.Data = filtered
			return nil
		}
		response.Data = []appserver.Thread{taskMentionTestThread("recent", "Recent task", "", "/repo")}
		return nil
	}
	matches := searchTaskMentions(context.Background(), request, "query", "", "/repo")
	if len(matches) != taskMentionSearchLimit+1 {
		t.Fatalf("matches = %d, want the filtered page plus the recent task", len(matches))
	}
	found := false
	for _, match := range matches {
		if match.ThreadID == "recent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recent task missing from a full filtered page: %#v", matches[len(matches)-1])
	}
}

// TestSearchTaskMentionsWindowsCWDFirstMatchesRust covers the Windows
// case-insensitive current-directory prefix rule.
func TestSearchTaskMentionsWindowsCWDFirstMatchesRust(t *testing.T) {
	request := func(_ context.Context, method appserver.Method, params any, target any) error {
		list, ok := params.(appserver.ThreadListParams)
		if method != appserver.MethodThreadList || !ok || list.SearchTerm == nil {
			return nil
		}
		*(target.(*appserver.ThreadListResponse)) = appserver.ThreadListResponse{Data: []appserver.Thread{
			taskMentionTestThread("other", "Other", "preview", `D:\Other`),
			taskMentionTestThread("same", "Same", "preview", `d:\repo`),
		}}
		return nil
	}
	matches := searchTaskMentions(context.Background(), request, "query", "", `D:\repo`)
	if len(matches) != 2 || matches[0].ThreadID != "same" {
		t.Fatalf("windows ordering = %#v", matches)
	}
	// The descendant check requires a path separator boundary.
	requestNear := func(_ context.Context, method appserver.Method, params any, target any) error {
		list, ok := params.(appserver.ThreadListParams)
		if method != appserver.MethodThreadList || !ok || list.SearchTerm == nil {
			return nil
		}
		*(target.(*appserver.ThreadListResponse)) = appserver.ThreadListResponse{Data: []appserver.Thread{
			taskMentionTestThread("sibling", "Sibling", "preview", `D:\repository`),
			taskMentionTestThread("child", "Child", "preview", `D:\repo\sub`),
		}}
		return nil
	}
	matches = searchTaskMentions(context.Background(), requestNear, "query", "", `D:\repo`)
	if len(matches) != 2 || matches[0].ThreadID != "child" {
		t.Fatalf("boundary ordering = %#v", matches)
	}
}

// TestApplySubmitTaskReferencesEncodesTaskMentionsLikeRust covers the composer
// binding decoding and the Rust submit-time task-reference application.
func TestApplySubmitTaskReferencesEncodesTaskMentionsLikeRust(t *testing.T) {
	title := "Review the migration"
	placeholder := "@" + title
	inputs := []turn.TurnUserInput{{
		Type: "text",
		Text: "@" + title + " next",
		TextElements: []turn.TextElement{{
			ByteRange:   turn.ByteRange{Start: 0, End: uint(len(title) + 1)},
			Placeholder: &placeholder,
		}},
	}}
	inputs = applySubmitTaskReferences(inputs, []string{"@" + title + "|thread://task-123"}, "other-thread")
	got := inputs[0].Text
	if !strings.HasPrefix(got, "## Referenced chats with Codex:") {
		t.Fatalf("referenced context = %q", got)
	}
	if !strings.Contains(got, `"threadId":"task-123"`) {
		t.Fatalf("thread reference missing: %q", got)
	}
	if !strings.Contains(got, "[@"+title+"](thread://task-123)") {
		t.Fatalf("task link missing: %q", got)
	}
	if len(inputs[0].TextElements) != 1 || int(inputs[0].TextElements[0].ByteRange.Start) < strings.Index(got, "[@"+title) {
		t.Fatalf("text element range = %#v", inputs[0].TextElements)
	}

	// The current thread's own reference is skipped.
	self := applySubmitTaskReferences([]turn.TurnUserInput{{Type: "text", Text: "@self"}}, []string{"@self|thread://self"}, "self")
	if strings.Contains(self[0].Text, "Referenced chats") {
		t.Fatalf("self reference applied: %q", self[0].Text)
	}
	// Non-thread bindings leave the text untouched.
	plugin := applySubmitTaskReferences([]turn.TurnUserInput{{Type: "text", Text: "hello"}}, []string{"@sample|plugin://sample"}, "")
	if plugin[0].Text != "hello" {
		t.Fatalf("plugin binding changed the text: %q", plugin[0].Text)
	}
	// Empty bindings and inputs are no-ops.
	if noop := applySubmitTaskReferences([]turn.TurnUserInput{{Type: "text", Text: "hi"}}, nil, ""); len(noop) != 1 || noop[0].Text != "hi" {
		t.Fatalf("empty bindings noop = %#v", noop)
	}
}

// Rust #62691: a stalled title filter is bounded and never delays the recent
// candidates that are already available.
func TestSearchTaskMentionsBoundsTheTitleFilterAndKeepsRecentLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var bounded bool
	request := func(requestCtx context.Context, method appserver.Method, params any, target any) error {
		if method != appserver.MethodThreadList {
			return errors.New("unexpected method")
		}
		list := params.(appserver.ThreadListParams)
		if list.SearchTerm != nil {
			deadline, ok := requestCtx.Deadline()
			mu.Lock()
			bounded = ok && time.Until(deadline) <= taskMentionSearchTimeout+time.Second
			mu.Unlock()
			<-requestCtx.Done()
			return requestCtx.Err()
		}
		*(target.(*appserver.ThreadListResponse)) = appserver.ThreadListResponse{Data: []appserver.Thread{
			taskMentionTestThread("recent", "Recent task", "recent preview", `/repo`),
		}}
		return nil
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	matches := searchTaskMentions(ctx, request, "query", "", `/repo`)
	mu.Lock()
	defer mu.Unlock()
	if !bounded {
		t.Fatal("the title filter was not bounded by the task-mention search timeout")
	}
	if len(matches) != 1 || matches[0].ThreadID != "recent" {
		t.Fatalf("matches = %#v, want the recent list despite the abandoned title filter", matches)
	}
}
