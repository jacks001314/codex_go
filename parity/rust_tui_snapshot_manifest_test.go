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

	// Re-pinned to upstream 4abcb8d1 (the #46680 contrast/keyboard-hint/picker
	// lane and its #46691/#46692/#46694/#46695/#46697/#46708/#46709/#46710
	// follow-ons): the picker/transcript work added 62 snapshots and retired
	// three, on top of the local daemon alpha/source version mismatch snapshots
	// pinned at a5290028 (#46673).
	if got := countSnapFilesRecursive(t, filepath.Join(root, "tui")); got != 1239 {
		t.Fatalf("Rust TUI snapshot total drift: got %d want 1239", got)
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
			Path: "tui/src/app/snapshots",
			// #46579 added the agents-overview recent-session snapshot.
			Files:    43,
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
			Path: "tui/src/app/tests/snapshots",
			// #46566 added the unavailable-thread local-command snapshot;
			// #46691 added the two running-task exit pickers.
			Files:    64,
			Owner:    "tui/app",
			Focus:    "app-level catalog and migration prompts",
			Priority: []string{"app", "model"},
			Required: []string{
				"tui/src/app/tests/snapshots/codex_tui__app__tests__model_catalog__model_migration_prompt_shows_for_hidden_model.snap",
				"tui/src/app/tests/snapshots/codex_tui__app__tests__background_task_defaults_tests__command_center_retained_worktree_error.snap",
				"tui/src/app/tests/snapshots/codex_tui__app__tests__background_exit_tests__running_task_exit_picker_40.snap",
			},
		},
		{
			Path: "tui/src/bottom_pane/async_questions/snapshots",
			// #46691 added the capped option and clipped-Other boundary snapshots.
			Files:    14,
			Owner:    "tui/bottom_pane",
			Focus:    "asynchronous question prompts with inline Other answers, capped and wrapped option layouts",
			Priority: []string{"approval", "request-user-input"},
			Required: []string{
				"tui/src/bottom_pane/async_questions/snapshots/codex_tui__bottom_pane__async_questions__tests__question_named_other.snap",
				"tui/src/bottom_pane/async_questions/snapshots/codex_tui__bottom_pane__async_questions__tests__question_options_width_boundary.snap",
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
			Path: "tui/src/bottom_pane/snapshots",
			// #46680 added the picker browser layouts and the filled-tab windows;
			// #46691-#46710 added the shared picker presentation, description
			// column visibility, skill-popup category tags, file-search long
			// path, custom-prompt picker, hooks-browser compact and
			// wrap-boundary snapshots, and retired the fixed-scroll one.
			Files:    256,
			Owner:    "tui/bottom_pane",
			Focus:    "composer, footer, slash popup, approval overlays, MCP elicitation, queued input, and bottom pane layout",
			Priority: []string{"composer", "approval", "status", "mcp", "slash"},
			Required: []string{
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__chat_composer__tests__empty.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__chat_composer__tests__slash_popup_res.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__approval_overlay__tests__approval_overlay_permissions_prompt.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__approval_overlay__tests__network_exec_prompt.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__tests__status_and_queued_messages_snapshot.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__list_selection_view__picker_tests__short_browser_keeps_header_tabs_search_and_footer.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__list_selection_view__picker_tests__browser_page_navigation_uses_rows_that_fit.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__list_selection_view__picker_tests__shared_menu_presentation_at_wide_and_narrow_sizes.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__selection_tabs__tests__filled_tabs_window_around_active_tab.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__selection_tabs__tests__filled_tabs_truncate_unicode_without_hiding_active_tab.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__status_line_style__tests__light_status_line_corrects_pale_custom_theme_colors.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__multi_select_picker__tests__picker_appearance_renders_checkboxes_preview_and_overflow.snap",
			},
		},
		{
			Path: "tui/src/bottom_pane/tests/snapshots",
			// #46692 added the promoted action banner plus the three picker
			// hint presentations.
			Files:    6,
			Owner:    "tui/bottom_pane",
			Focus:    "actionable information banner dismiss and persistence",
			Priority: []string{"status"},
			Required: []string{
				"tui/src/bottom_pane/tests/snapshots/codex_tui__bottom_pane__tests__actionable_banner_tests__information_banner_dismissible.snap",
				"tui/src/bottom_pane/tests/snapshots/codex_tui__bottom_pane__tests__picker_hint_tests__popup_hint_picker.snap",
			},
		},
		{
			Path: "tui/src/bottom_pane/approval_overlay/snapshots",
			// #46692 moved the clipped-exec approval snapshot into its own
			// directory next to the new clipping tests.
			Files:    1,
			Owner:    "tui/bottom_pane/approval_overlay",
			Focus:    "clipped exec approval shows the complete command",
			Priority: []string{"approval"},
			Required: []string{
				"tui/src/bottom_pane/approval_overlay/snapshots/codex_tui__bottom_pane__approval_overlay__clipping_tests__clipped_exec_approval_opens_the_complete_command.snap",
			},
		},
		{
			Path: "tui/src/bottom_pane/mentions_v2/snapshots",
			// #46694 added the unified mention popup render tables.
			Files:    7,
			Owner:    "tui/bottom_pane/mentions_v2",
			Focus:    "unified mention popup layout, bounded filesystem columns, and scrolled/narrow tables",
			Priority: []string{"composer"},
			Required: []string{
				"tui/src/bottom_pane/mentions_v2/snapshots/codex_tui__bottom_pane__mentions_v2__render__tests__wide.snap",
				"tui/src/bottom_pane/mentions_v2/snapshots/codex_tui__bottom_pane__mentions_v2__render__tests__bounded_filesystem_columns_narrow.snap",
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
			Path: "tui/src/chatwidget/snapshots",
			// #46695/#46697 added the clipped full-access confirmation and the
			// two Windows sandbox picker fallbacks.
			Files:    307,
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
			Path: "tui/src/chatwidget/tests/snapshots",
			// #46574/#46565 added the question-notification and activity-group
			// ordering snapshots; #46709 added the compact-exploration one.
			Files:    54,
			Owner:    "tui/chatwidget",
			Focus:    "chatwidget approval request modal, async question reply, and history snapshots",
			Priority: []string{"approval", "history"},
			Required: []string{
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_modal_exec.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_history_decision_approved_short.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__exec_flow__compact_exploration_with_failures.snap",
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
			Path: "tui/src/history_cell/snapshots",
			// #46680 added the user-image prompt label contrast snapshot;
			// #46708-#46710 added the dynamic history-cell previews, compact
			// computer activity, image-label merge, compact patch, and
			// request-user-input completed/interrupted results.
			Files:    87,
			Owner:    "tui/history_cell",
			Focus:    "history cell rendering for exec, MCP, plan updates, errors, sessions, user messages, and web search",
			Priority: []string{"history-cell", "mcp", "status"},
			Required: []string{
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__single_line_command_compact_when_fits.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__plan_update_with_note_and_wrapping_snapshot.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__web_search_history_cell_snapshot.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__local_daemon_alpha_mismatch.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__local_daemon_source_mismatch.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__user_image_labels_follow_the_painted_prompt_surface.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__dynamic__tests__dynamic_preview_reports_hidden_lines_and_retains_full_output.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__patches__tests__compact_patches_retain_full_changes_and_failure_details.snap",
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
			Path: "tui/src/onboarding/snapshots",
			// #46695 added the four restricted/long-path trust-directory pickers.
			Files:    10,
			Owner:    "tui/onboarding",
			Focus:    "trust-directory onboarding states",
			Priority: []string{"onboarding"},
			Required: []string{
				"tui/src/onboarding/snapshots/codex_tui__onboarding__trust_directory__tests__renders_snapshot_for_git_repo.snap",
				"tui/src/onboarding/snapshots/codex_tui__onboarding__trust_directory__tests__folder_picker_restricted_40x24.snap",
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
			Path: "tui/src/snapshots",
			// #46680 added the diff syntax colors over a painted background;
			// #46691-#46697 added the cwd/model/oss/update pickers, the
			// startup-hooks picker progress, the compact resume-picker color
			// snapshots and the tool-output preview snapshots, and retired the
			// syntax-highlighted insert wrap and the thread-color picker one.
			Files:    195,
			Owner:    "tui, tui/markdown, tui/app",
			Focus:    "diff render, markdown render, keymap, resume picker, pager overlay, model migration, and status indicator snapshots",
			Priority: []string{"diff", "markdown", "status", "session", "keymap"},
			Required: []string{
				"tui/src/snapshots/codex_tui__diff_render__tests__diff_gallery_80x24.snap",
				"tui/src/snapshots/codex_tui__markdown_render__markdown_render_tests__markdown_render_complex_snapshot.snap",
				"tui/src/snapshots/codex_tui__resume_picker__tests__resume_picker_screen.snap",
				"tui/src/snapshots/codex_tui__diff_render__tests__syntax_colors_follow_the_actual_diff_background.snap",
				"tui/src/snapshots/codex_tui__resume_picker__color_tests__compact_picker_keeps_metadata_and_labeled_primary_actions.snap",
				"tui/src/snapshots/codex_tui__tool_output__tests__preview_caps_wrapped_output_and_counts_hidden_logical_lines.snap",
			},
		},
		{
			Path: "tui/src/thread_transcript/snapshots",
			// #46710 added the persisted-transcript tool presentations.
			Files:    4,
			Owner:    "tui/thread_transcript",
			Focus:    "persisted transcript tool presentations, historical command fallbacks, and agent-tool status",
			Priority: []string{"history-cell", "unified-exec"},
			Required: []string{
				"tui/src/thread_transcript/snapshots/codex_tui__thread_transcript__tools__tests__completed_tool_presentations.snap",
				"tui/src/thread_transcript/snapshots/codex_tui__thread_transcript__tools__tests__agent_tool_fallbacks_preserve_status_without_duplicating_v2_activity.snap",
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
			Files:    4,
			Owner:    "tui",
			Focus:    "integration suite snapshots for automatic background server startup failures and shared-daemon feature/host-policy/override mismatches",
			Priority: []string{"status"},
			Required: []string{
				"tui/tests/suite/snapshots/all__suite__focus_palette__daemon_auto_start_failure.snap",
				"tui/tests/suite/snapshots/all__suite__daemon_compatibility__daemon_feature_mismatch.snap",
				"tui/tests/suite/snapshots/all__suite__daemon_compatibility__daemon_host_policy_mismatch.snap",
				"tui/tests/suite/snapshots/all__suite__daemon_compatibility__daemon_override_mismatch.snap",
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
