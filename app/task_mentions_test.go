package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/turn"
)

func taskMentionTestThread(id string, name string, preview string, cwd string) appserver.Thread {
	thread := appserver.Thread{ID: id, Preview: preview, CWD: cwd}
	if name != "" {
		thread.Name = &name
	}
	return thread
}

// TestSearchTaskMentionsMatchesRust covers Rust task_mentions::spawn_search's
// discovery half: request shape, merge/dedupe, filtering, title fallback,
// snippet provenance, the 50-result bound, and current-cwd-first ordering.
func TestSearchTaskMentionsMatchesRust(t *testing.T) {
	var methods []appserver.Method
	request := func(_ context.Context, method appserver.Method, params any, target any) error {
		methods = append(methods, method)
		switch method {
		case appserver.MethodThreadSearch:
			search := params.(appserver.ThreadSearchParams)
			if search.SearchTerm != "query" || search.Limit == nil || *search.Limit != 50 ||
				search.SortKey != appserver.SortRecencyAt || search.SortDirection != appserver.SortDesc ||
				search.Archived == nil || *search.Archived {
				t.Fatalf("thread/search params = %#v", search)
			}
			*(target.(*appserver.ThreadSearchResponse)) = appserver.ThreadSearchResponse{Data: []appserver.ThreadSearchResult{
				{Thread: taskMentionTestThread("match", "Named task", "preview", `/repo`), Snippet: "snippet"},
				{Thread: taskMentionTestThread("self", "Current", "preview", `/repo`)},
				{Thread: taskMentionTestThread("ephemeral", "Ephemeral", "preview", `/repo`)},
				{Thread: taskMentionTestThread("ambient", "Ambient", "preview", `/repo`)},
				{Thread: taskMentionTestThread("descendant", "", "Descendant preview", `/repo/sub`)},
				{Thread: taskMentionTestThread("elsewhere", "", "", `D:\other`)},
			}}
		case appserver.MethodThreadList:
			list := params.(appserver.ThreadListParams)
			if list.Limit == nil || *list.Limit != 50 || list.SortKey != appserver.SortUpdatedAt ||
				list.SortDirection != appserver.SortDesc || list.Archived == nil || *list.Archived ||
				!list.UseStateDBOnly {
				t.Fatalf("thread/list params = %#v", list)
			}
			*(target.(*appserver.ThreadListResponse)) = appserver.ThreadListResponse{Data: []appserver.Thread{
				taskMentionTestThread("match", "Named task", "preview", `/repo`), // duplicate is skipped
				taskMentionTestThread("listonly", "Listed task", "listed preview", `/repo`),
			}}
		default:
			t.Fatalf("unexpected method %s", method)
		}
		return nil
	}

	// The ambient thread must carry the feature thread-source marker.
	ambient := appserver.ThreadSource("ambient_suggestions")
	ephemeral := true
	requestWithMarkers := func(ctx context.Context, method appserver.Method, params any, target any) error {
		err := request(ctx, method, params, target)
		if method == appserver.MethodThreadSearch {
			response := target.(*appserver.ThreadSearchResponse)
			for index := range response.Data {
				switch response.Data[index].Thread.ID {
				case "ambient":
					response.Data[index].Thread.ThreadSource = &ambient
				case "ephemeral":
					response.Data[index].Thread.Ephemeral = ephemeral
				}
			}
		}
		return err
	}

	matches := searchTaskMentions(context.Background(), requestWithMarkers, " query ", "self", `/repo`)
	if len(methods) != 2 || methods[0] != appserver.MethodThreadSearch || methods[1] != appserver.MethodThreadList {
		t.Fatalf("methods = %#v", methods)
	}
	gotIDs := make([]string, 0, len(matches))
	for _, match := range matches {
		gotIDs = append(gotIDs, match.ThreadID)
	}
	wantIDs := []string{"match", "descendant", "listonly", "elsewhere"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("task ids = %#v, want %#v", gotIDs, wantIDs)
	}
	if matches[0].Title != "Named task" || matches[0].Snippet != "snippet" || matches[0].CWD != `/repo` {
		t.Fatalf("search-backed match = %#v", matches[0])
	}
	if matches[1].Title != "Descendant preview" {
		t.Fatalf("preview fallback = %#v", matches[1])
	}
	if matches[2].Title != "Listed task" || matches[2].Snippet != "" {
		t.Fatalf("list-only match = %#v", matches[2])
	}
	if matches[3].Title != "elsewhere" {
		t.Fatalf("id fallback = %#v", matches[3])
	}

	// An empty query issues no requests.
	methods = nil
	if matches := searchTaskMentions(context.Background(), requestWithMarkers, "  ", "self", `/repo`); matches != nil {
		t.Fatalf("empty query matches = %#v", matches)
	}
	if len(methods) != 0 {
		t.Fatalf("empty query issued requests: %#v", methods)
	}
	// Both requests failing yields no tasks instead of an error.
	failing := func(context.Context, appserver.Method, any, any) error { return errors.New("offline") }
	if matches := searchTaskMentions(context.Background(), failing, "query", "self", `/repo`); matches != nil {
		t.Fatalf("offline matches = %#v", matches)
	}
}

// TestSearchTaskMentionsWindowsCWDFirstMatchesRust covers the Windows
// case-insensitive current-directory prefix rule.
func TestSearchTaskMentionsWindowsCWDFirstMatchesRust(t *testing.T) {
	request := func(_ context.Context, method appserver.Method, _ any, target any) error {
		if method == appserver.MethodThreadSearch {
			*(target.(*appserver.ThreadSearchResponse)) = appserver.ThreadSearchResponse{Data: []appserver.ThreadSearchResult{
				{Thread: taskMentionTestThread("other", "Other", "preview", `D:\Other`)},
				{Thread: taskMentionTestThread("same", "Same", "preview", `d:\repo`)},
			}}
		}
		return nil
	}
	matches := searchTaskMentions(context.Background(), request, "query", "", `D:\repo`)
	if len(matches) != 2 || matches[0].ThreadID != "same" {
		t.Fatalf("windows ordering = %#v", matches)
	}
	// The descendant check requires a path separator boundary.
	requestNear := func(_ context.Context, method appserver.Method, _ any, target any) error {
		if method == appserver.MethodThreadSearch {
			*(target.(*appserver.ThreadSearchResponse)) = appserver.ThreadSearchResponse{Data: []appserver.ThreadSearchResult{
				{Thread: taskMentionTestThread("sibling", "Sibling", "preview", `D:\repository`)},
				{Thread: taskMentionTestThread("child", "Child", "preview", `D:\repo\sub`)},
			}}
		}
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
