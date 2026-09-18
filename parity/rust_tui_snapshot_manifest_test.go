package parity

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type rustTUISnapshotDir struct {
	Path     string
	Files    int
	Owner    string
	Focus    string
	Priority []string
	Required []string
}

func TestRustTUISnapshotManifestCoversPrioritySurfaces(t *testing.T) {
	root := rustSnapshotRoot(t)
	manifest := rustTUISnapshotManifest()

	if got := countSnapFilesRecursive(t, filepath.Join(root, "tui")); got != 1168 {
		t.Fatalf("Rust TUI snapshot total drift: got %d want 1168", got)
	}

	gotDirs := rustTUISnapshotDirs(t, root)
	wantDirs := make([]string, 0, len(manifest))
	priorityCoverage := map[string]int{}
	for _, entry := range manifest {
		if entry.Path == "" || entry.Owner == "" || entry.Focus == "" || entry.Files <= 0 || len(entry.Priority) == 0 {
			t.Fatalf("incomplete Rust TUI snapshot manifest entry: %#v", entry)
		}
		wantDirs = append(wantDirs, entry.Path)
		for _, priority := range entry.Priority {
			priorityCoverage[priority] += entry.Files
		}

		abs := filepath.Join(root, filepath.FromSlash(entry.Path))
		if got := countSnapFilesShallow(t, abs); got != entry.Files {
			t.Fatalf("Rust TUI snapshot count drift for %s: got %d want %d", entry.Path, got, entry.Files)
		}
		for _, required := range entry.Required {
			path := filepath.Join(root, filepath.FromSlash(required))
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("required Rust TUI snapshot %s missing for %s: %v", required, entry.Path, err)
			}
		}
	}
	sort.Strings(wantDirs)
	if !reflect.DeepEqual(gotDirs, wantDirs) {
		t.Fatalf("Rust TUI snapshot directory drift; missing=%v unexpected=%v gotCount=%d wantCount=%d", missingStrings(wantDirs, gotDirs), missingStrings(gotDirs, wantDirs), len(gotDirs), len(wantDirs))
	}

	for _, priority := range []string{"composer", "approval", "status", "history-cell"} {
		if priorityCoverage[priority] == 0 {
			t.Fatalf("Rust TUI snapshot manifest does not cover priority surface %q", priority)
		}
	}
}

func rustTUISnapshotManifest() []rustTUISnapshotDir {
	return []rustTUISnapshotDir{
		{
			Path:     "tui/src/analytics/snapshots",
			Files:    70,
			Owner:    "tui/analytics, backend-client",
			Focus:    "account analytics dashboards, usage charts, plan history, top chats, and terminal-style variants",
			Priority: []string{"analytics", "status"},
			Required: []string{
				"tui/src/analytics/snapshots/codex_tui__analytics__tests__dashboard__dashboard_cards_align_and_keep_stable_summary_heights.snap",
				"tui/src/analytics/snapshots/codex_tui__analytics__plot__tests__signed_chart_dark.snap",
				"tui/src/analytics/snapshots/codex_tui__analytics__plan__tests__unavailable_empty_and_zero_history_are_distinct.snap",
			},
		},
		{
			Path:     "tui/src/app/snapshots",
			Files:    42,
			Owner:    "tui/app, tui/chatwidget",
			Focus:    "desktop history UI, cancelled-turn composer restore, and thread goal action rendering",
			Priority: []string{"app", "composer", "history"},
			Required: []string{
				"tui/src/app/snapshots/codex_tui__app__history_ui__tests__desktop_thread_opened_history.snap",
				"tui/src/app/snapshots/codex_tui__app__tests__required_stream_reflow_during_capped_initial_replay.snap",
				"tui/src/app/snapshots/codex_tui__app__agents_overview__tests__overview_worktree_creation_busy_state.snap",
			},
		},
		{
			Path:     "tui/src/app/tests/snapshots",
			Files:    61,
			Owner:    "tui/app",
			Focus:    "app-level catalog and migration prompts",
			Priority: []string{"app", "model"},
			Required: []string{
				"tui/src/app/tests/snapshots/codex_tui__app__tests__model_catalog__model_migration_prompt_shows_for_hidden_model.snap",
				"tui/src/app/tests/snapshots/codex_tui__app__tests__background_task_defaults_tests__command_center_retained_worktree_error.snap",
			},
		},
		{
			Path:     "tui/src/bottom_pane/async_questions/snapshots",
			Files:    12,
			Owner:    "tui/bottom_pane",
			Focus:    "asynchronous question prompts with inline Other answers, capped and wrapped option layouts",
			Priority: []string{"approval", "request-user-input"},
			Required: []string{
				"tui/src/bottom_pane/async_questions/snapshots/codex_tui__bottom_pane__async_questions__tests__question_named_other.snap",
			},
		},
		{
			Path:     "tui/src/bottom_pane/chat_composer/snapshots",
			Files:    17,
			Owner:    "tui/bottom_pane/chat_composer",
			Focus:    "draft and voice composer layout snapshots",
			Priority: []string{"composer"},
			Required: []string{
				"tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__snapshot_tests__draft_composer.snap",
				"tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__snapshot_tests__voice_composer.snap",
			},
		},
		{
			Path:     "tui/src/bottom_pane/request_user_input/snapshots",
			Files:    16,
			Owner:    "tui/bottom_pane/request_user_input",
			Focus:    "request_user_input options, free-form input, countdowns, remapped keys, and tight-height layout",
			Priority: []string{"approval", "request-user-input"},
			Required: []string{
				"tui/src/bottom_pane/request_user_input/snapshots/codex_tui__bottom_pane__request_user_input__tests__request_user_input_options.snap",
				"tui/src/bottom_pane/request_user_input/snapshots/codex_tui__bottom_pane__request_user_input__tests__request_user_input_freeform.snap",
			},
		},
		{
			Path:     "tui/src/bottom_pane/snapshots",
			Files:    239,
			Owner:    "tui/bottom_pane",
			Focus:    "composer, footer, slash popup, approval overlays, MCP elicitation, queued input, and bottom pane layout",
			Priority: []string{"composer", "approval", "status", "mcp", "slash"},
			Required: []string{
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__chat_composer__tests__empty.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__chat_composer__tests__slash_popup_res.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__approval_overlay__tests__approval_overlay_permissions_prompt.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__approval_overlay__tests__network_exec_prompt.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__tests__status_and_queued_messages_snapshot.snap",
			},
		},
		{
			Path:     "tui/src/bottom_pane/tests/snapshots",
			Files:    2,
			Owner:    "tui/bottom_pane",
			Focus:    "actionable information banner dismiss and persistence",
			Priority: []string{"status"},
			Required: []string{
				"tui/src/bottom_pane/tests/snapshots/codex_tui__bottom_pane__tests__actionable_banner_tests__information_banner_dismissible.snap",
			},
		},
		{
			Path:     "tui/src/bottom_pane/textarea/snapshots",
			Files:    8,
			Owner:    "tui/bottom_pane/textarea",
			Focus:    "textarea wrapping: hanging tabs, mandatory breaks, end-of-line spaces, and vertical navigation after resize",
			Priority: []string{"composer"},
			Required: []string{
				"tui/src/bottom_pane/textarea/snapshots/codex_tui__bottom_pane__textarea__wrapping__tests__hanging_tab_cursor_and_scroll.snap",
			},
		},
		{
			Path:     "tui/src/chatwidget/realtime/snapshots",
			Files:    2,
			Owner:    "tui/chatwidget",
			Focus:    "realtime voice recording controls, footer conversation states, and voice meter sampling",
			Priority: []string{"status", "voice"},
			Required: []string{
				"tui/src/chatwidget/realtime/snapshots/codex_tui__chatwidget__realtime__tests__recording_controls_tests__voice_footer_renders_the_main_conversation_states.snap",
			},
		},
		{
			Path:     "tui/src/chatwidget/realtime_tests/snapshots",
			Files:    16,
			Owner:    "tui/chatwidget",
			Focus:    "realtime voice lifecycle, transcript replay, delegation handoffs, and privacy snapshots",
			Priority: []string{"voice", "history"},
			Required: []string{
				"tui/src/chatwidget/realtime_tests/snapshots/codex_tui__chatwidget__realtime__tests__lifecycle__voice_start_banner.snap",
				"tui/src/chatwidget/realtime_tests/snapshots/codex_tui__chatwidget__realtime__tests__transcripts__interleaved_partials_on_close.snap",
			},
		},
		{
			Path:     "tui/src/chatwidget/snapshots",
			Files:    304,
			Owner:    "tui/chatwidget, tui/tea",
			Focus:    "main chat widget terminal snapshots for status lines, approvals, plugins, hooks, review, usage, and unified exec",
			Priority: []string{"approval", "status", "history", "unified-exec", "review"},
			Required: []string{
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__status_widget_active.snap",
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__status_widget_and_approval_modal.snap",
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__unified_exec_begin_restores_working_status.snap",
			},
		},
		{
			Path:     "tui/src/chatwidget/tests/snapshots",
			Files:    51,
			Owner:    "tui/chatwidget",
			Focus:    "chatwidget approval request modal, async question reply, and history snapshots",
			Priority: []string{"approval", "history"},
			Required: []string{
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_modal_exec.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_history_decision_approved_short.snap",
			},
		},
		{
			Path:     "tui/src/clipboard_copy/snapshots",
			Files:    2,
			Owner:    "tui/clipboard_copy",
			Focus:    "clipboard copy failure routing for empty selections and tmux targets",
			Priority: []string{"render"},
			Required: []string{
				"tui/src/clipboard_copy/snapshots/codex_tui__clipboard_copy__routing_tests__empty_copy_reports_failure_without_touching_clipboards.snap",
				"tui/src/clipboard_copy/snapshots/codex_tui__clipboard_copy__tmux__tests__tmux_clipboard_target_rejects_no_attached_client.snap",
			},
		},
		{
			Path:     "tui/src/custom_terminal/tests/snapshots",
			Files:    3,
			Owner:    "tui/custom_terminal",
			Focus:    "cursor style rendering across single-column, styled, and owned wide frames",
			Priority: []string{"render"},
			Required: []string{
				"tui/src/custom_terminal/tests/snapshots/codex_tui__custom_terminal__tests__cursor__cursor_style_owned_wide_frames.snap",
			},
		},
		{
			Path:     "tui/src/exec_cell/snapshots",
			Files:    1,
			Owner:    "tui/exec_cell",
			Focus:    "bounded live command output preview and final transcript rendering",
			Priority: []string{"history-cell", "unified-exec"},
			Required: []string{
				"tui/src/exec_cell/snapshots/codex_tui__exec_cell__render__tests__truncated_live_output_preview_and_transcript.snap",
			},
		},
		{
			Path:     "tui/src/external_agent_config_migration/snapshots",
			Files:    9,
			Owner:    "tui/external_agent_config_migration",
			Focus:    "external agent configuration migration prompts, customization, source choice, and Windows variants",
			Priority: []string{"external-agent", "migration"},
			Required: []string{
				"tui/src/external_agent_config_migration/snapshots/codex_tui__external_agent_config_migration__tests__external_agent_config_migration_prompt.snap",
				"tui/src/external_agent_config_migration/snapshots/codex_tui__external_agent_config_migration__tests__external_agent_config_migration_prompt_windows.snap",
			},
		},
		{
			Path:     "tui/src/history_cell/snapshots",
			Files:    77,
			Owner:    "tui/history_cell",
			Focus:    "history cell rendering for exec, MCP, plan updates, errors, sessions, user messages, and web search",
			Priority: []string{"history-cell", "mcp", "status"},
			Required: []string{
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__single_line_command_compact_when_fits.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__plan_update_with_note_and_wrapping_snapshot.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__web_search_history_cell_snapshot.snap",
			},
		},
		{
			Path:     "tui/src/markdown_render/snapshots",
			Files:    10,
			Owner:    "tui/markdown_render, tui/mermaid",
			Focus:    "markdown render web-link labels, Unicode math (inline/display/accents), and Mermaid text rendering",
			Priority: []string{"markdown"},
			Required: []string{
				"tui/src/markdown_render/snapshots/codex_tui__markdown_render__web_links__tests__label_only_and_fallback_presentations_snapshot.snap",
				"tui/src/markdown_render/snapshots/codex_tui__markdown_render__math__tests__unicode_math_inline_snapshot.snap",
				"tui/src/markdown_render/snapshots/codex_tui__markdown_render__mermaid__tests__mermaid_styles_follow_the_supplied_theme.snap",
			},
		},
		{
			Path:     "tui/src/onboarding/snapshots",
			Files:    6,
			Owner:    "tui/onboarding",
			Focus:    "trust-directory onboarding states",
			Priority: []string{"onboarding"},
			Required: []string{
				"tui/src/onboarding/snapshots/codex_tui__onboarding__trust_directory__tests__renders_snapshot_for_git_repo.snap",
			},
		},
		{
			Path:     "tui/src/render/snapshots",
			Files:    1,
			Owner:    "tui",
			Focus:    "render/highlight palette snapshots",
			Priority: []string{"render"},
			Required: []string{
				"tui/src/render/snapshots/codex_tui__render__highlight__tests__ansi_family_foreground_palette.snap",
			},
		},
		{
			Path:     "tui/src/snapshots",
			Files:    186,
			Owner:    "tui, tui/markdown, tui/app",
			Focus:    "diff render, markdown render, keymap, resume picker, pager overlay, model migration, and status indicator snapshots",
			Priority: []string{"diff", "markdown", "status", "session", "keymap"},
			Required: []string{
				"tui/src/snapshots/codex_tui__diff_render__tests__diff_gallery_80x24.snap",
				"tui/src/snapshots/codex_tui__markdown_render__markdown_render_tests__markdown_render_complex_snapshot.snap",
				"tui/src/snapshots/codex_tui__resume_picker__tests__resume_picker_screen.snap",
			},
		},
		{
			Path:     "tui/src/status/snapshots",
			Files:    23,
			Owner:    "tui/status, tui/chatwidget",
			Focus:    "status command snapshots for account, limits, reasoning, profiles, fork metadata, stale data, and narrow layouts",
			Priority: []string{"status"},
			Required: []string{
				"tui/src/status/snapshots/codex_tui__status__tests__status_snapshot_includes_credits_and_limits.snap",
				"tui/src/status/snapshots/codex_tui__status__tests__status_snapshot_truncates_in_narrow_terminal.snap",
				"tui/src/status/snapshots/codex_tui__status__tests__status_snapshot_shows_auto_review_permissions.snap",
			},
		},
		{
			Path:     "tui/src/streaming/snapshots",
			Files:    6,
			Owner:    "tui/streaming",
			Focus:    "incremental Markdown rendering equivalence and visualization context",
			Priority: []string{"markdown", "history-cell"},
			Required: []string{
				"tui/src/streaming/snapshots/codex_tui__streaming__render__tests__incremental_render_representative_stream.snap",
			},
		},
		{
			Path:     "tui/src/tui/snapshots",
			Files:    3,
			Owner:    "tui",
			Focus:    "terminal scrollback viewport growth strategies (standard insertion without CSI S)",
			Priority: []string{"render"},
			Required: []string{
				"tui/src/tui/snapshots/codex_tui__tui__scrollback__tests__standard_viewport_growth_1.snap",
			},
		},
		{
			Path:     "tui/tests/suite/snapshots",
			Files:    1,
			Owner:    "tui",
			Focus:    "integration suite snapshot for automatic background server startup failures",
			Priority: []string{"status"},
			Required: []string{
				"tui/tests/suite/snapshots/all__suite__focus_palette__daemon_auto_start_failure.snap",
			},
		},
	}
}

func rustTUISnapshotDirs(t *testing.T, root string) []string {
	t.Helper()
	base := filepath.Join(root, "tui")
	dirs := []string{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || d.Name() != "snapshots" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dirs = append(dirs, filepath.ToSlash(rel))
		return filepath.SkipDir
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) error = %v", base, err)
	}
	sort.Strings(dirs)
	return dirs
}

func countSnapFilesShallow(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", root, err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".snap" {
			count++
		}
	}
	return count
}

func countSnapFilesRecursive(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(d.Name()) == ".snap" {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) error = %v", root, err)
	}
	return count
}
