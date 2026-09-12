package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/bottom_pane/mentions_v2"
)

// TestModelTaskMentionSearchPopulatesTaskCandidatesLikeRust mirrors Rust
// task_mentions::spawn_search reaching the mention catalog: a typed query issues
// one search for the current thread, its results become Task candidates linked
// as `@title` -> `thread://id`, stale generations are dropped, and an empty
// query clears the task rows without issuing a request.
func TestModelTaskMentionSearchPopulatesTaskCandidatesLikeRust(t *testing.T) {
	var queries []string
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		OnSearchTasks: func(query string, currentThreadID string, cwd string) ([]codextui.TaskMention, error) {
			queries = append(queries, query+"|"+currentThreadID)
			return []codextui.TaskMention{{
				ThreadID: "task-123",
				Title:    "Review the migration",
				CWD:      `D:\repo`,
				Snippet:  "snippet",
			}}, nil
		},
	})
	model.Update(TaskToolsAvailableMsg{ThreadID: "thread-1", Available: true})

	model.Update(runes("@"))
	_, cmd := model.Update(runes("r"))
	runTeaCmd(t, model, cmd)
	if len(queries) != 1 || queries[0] != "r|thread-1" {
		t.Fatalf("task searches = %#v, want one query for the current thread", queries)
	}
	if model.mentionPopup == nil {
		t.Fatal("mention popup closed")
	}
	var taskRow *mentionsv2.SearchResult
	for _, row := range model.mentionPopup.Rows() {
		if row.MentionType == mentionsv2.MentionTypeTask {
			copied := row
			taskRow = &copied
		}
	}
	if taskRow == nil {
		t.Fatalf("task candidate missing from popup rows: %#v", model.mentionPopup.Rows())
	}
	if taskRow.DisplayName != "Review the migration" || taskRow.Description != `D:\repo` {
		t.Fatalf("task row = %#v", taskRow)
	}
	if taskRow.Selection.InsertText != "@Review the migration" || taskRow.Selection.Path != "thread://task-123" {
		t.Fatalf("task selection = %#v", taskRow.Selection)
	}

	// A result from an older generation (or a different query) must not apply.
	generation := model.mentionTaskSearchGeneration
	model.applyTaskMentionSearchResult(TaskMentionSearchResultMsg{
		Generation: generation + 1,
		Query:      "r",
		Matches:    []codextui.TaskMention{{ThreadID: "stale", Title: "stale"}},
	})
	model.applyTaskMentionSearchResult(TaskMentionSearchResultMsg{
		Generation: generation,
		Query:      "other",
		Matches:    []codextui.TaskMention{{ThreadID: "stale", Title: "stale"}},
	})
	if len(model.mentionTasks) != 1 || model.mentionTasks[0].ThreadID != "task-123" {
		t.Fatalf("stale results applied: %#v", model.mentionTasks)
	}

	// Backspacing to an empty query clears the task rows without searching.
	_, cmd = model.Update(key(bubbletea.KeyBackspace))
	runTeaCmd(t, model, cmd)
	if len(queries) != 1 {
		t.Fatalf("empty query issued a search: %#v", queries)
	}
	if len(model.mentionTasks) != 0 {
		t.Fatalf("task rows not cleared: %#v", model.mentionTasks)
	}
}

// TestModelTaskMentionsRequireTaskToolsLikeRust covers Rust
// chat_widget.set_task_mentions_enabled(task_tools_available): the task search
// only runs for threads whose app server accepted the task-tool namespace, and a
// fork inherits the parent's capability.
func TestModelTaskMentionsRequireTaskToolsLikeRust(t *testing.T) {
	var queries []string
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		OnSearchTasks: func(query string, currentThreadID string, cwd string) ([]codextui.TaskMention, error) {
			queries = append(queries, query)
			return []codextui.TaskMention{{ThreadID: "task-1", Title: "Task"}}, nil
		},
	})

	// Without the capability the popup issues no task search.
	model.Update(runes("@"))
	_, cmd := model.Update(runes("r"))
	runTeaCmd(t, model, cmd)
	if len(queries) != 0 {
		t.Fatalf("task search ran without task tools: %#v", queries)
	}

	// After the thread's start reports the namespace, the search runs.
	model.Update(TaskToolsAvailableMsg{ThreadID: "thread-1", Available: true})
	_, cmd = model.Update(runes("q"))
	runTeaCmd(t, model, cmd)
	if len(queries) != 1 || queries[0] != "rq" {
		t.Fatalf("task searches = %#v", queries)
	}

	// A fork of an available thread inherits the capability.
	model.inheritTaskToolCapability("thread-1", "thread-forked")
	if !model.taskToolThreads["thread-forked"] {
		t.Fatalf("fork did not inherit task tools: %#v", model.taskToolThreads)
	}
	// A fork of an unavailable thread does not gain it.
	model.inheritTaskToolCapability("thread-plain", "thread-plain-fork")
	if model.taskToolThreads["thread-plain-fork"] {
		t.Fatalf("fork gained task tools without the parent: %#v", model.taskToolThreads)
	}
	// A downgraded start reports no capability instead of forgetting an earlier
	// one (Rust only remembers positive capability).
	model.Update(TaskToolsAvailableMsg{ThreadID: "thread-1", Available: false})
	if !model.taskMentionsEnabled() {
		t.Fatalf("downgrade forgot a remembered thread: %#v", model.taskToolThreads)
	}
	// A thread that was never reported and has no persisted marker stays disabled.
	model.State.SetThreadID("thread-never-started")
	if model.taskMentionsEnabled() {
		t.Fatalf("unknown thread enabled task mentions: %#v", model.taskToolThreads)
	}
}

// TestModelTaskMentionsUsePersistedCapabilityLikeRust covers Rust
// AppServerSession::task_tools_available's marker-directory half: a thread the
// current process has not seen still counts as available when the app reports a
// persisted capability.
func TestModelTaskMentionsUsePersistedCapabilityLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-persisted")
	var queries []string
	persisted := map[string]bool{"thread-persisted": true, "thread-unknown": false}
	model := NewModel(state, Options{
		OnSearchTasks: func(query string, currentThreadID string, cwd string) ([]codextui.TaskMention, error) {
			queries = append(queries, query)
			return nil, nil
		},
		OnTaskToolsAvailable: func(threadID string) bool { return persisted[threadID] },
	})

	model.Update(runes("@"))
	_, cmd := model.Update(runes("r"))
	runTeaCmd(t, model, cmd)
	if len(queries) != 1 || queries[0] != "r" {
		t.Fatalf("persisted capability not consulted: %#v", queries)
	}

	// A thread without a persisted marker and no in-session report stays disabled.
	state.SetThreadID("thread-unknown")
	_, cmd = model.Update(runes("s"))
	runTeaCmd(t, model, cmd)
	if len(queries) != 1 {
		t.Fatalf("unavailable thread ran a task search: %#v", queries)
	}
}
