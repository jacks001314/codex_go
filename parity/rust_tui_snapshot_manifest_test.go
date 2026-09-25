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

	// Re-pinned to upstream e75b36efde: #48101 added the truncated multiline
	// `/ps` command preview on top of the d0a64e91ae total of 1321.
	if got := countSnapFilesRecursive(t, filepath.Join(root, "tui")); got != 1322 {
		t.Fatalf("Rust TUI snapshot total drift: got %d want 1322", got)
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
			Files:    27,
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
			// #46733/#46739/#46751/#46752 added the owned-transcript browsing,
			// prompt-navigation, slash-picker, composer-gap and warnings-page
			// snapshots plus the fresh-thread animation and retained revert
			// history.
			Files:    73,
			Owner:    "tui/app, tui/chatwidget",
			Focus:    "desktop history UI, cancelled-turn composer restore, and thread goal action rendering",
			Priority: []string{"app", "composer", "history"},
			Required: []string{
				"tui/src/app/snapshots/codex_tui__app__history_ui__tests__desktop_thread_opened_history.snap",
				"tui/src/app/snapshots/codex_tui__app__tests__required_stream_reflow_during_capped_initial_replay.snap",
				"tui/src/app/snapshots/codex_tui__app__agents_overview__tests__overview_worktree_creation_busy_state.snap",
				"tui/src/app/snapshots/codex_tui__app__owned_transcript__tests__browsing__browsing_footer.snap",
				"tui/src/app/snapshots/codex_tui__app__owned_transcript__tests__prompt_navigation.snap",
				"tui/src/app/snapshots/codex_tui__app__warnings_tests__warnings_page.snap",
				// #47954 moved the startup tips out of the empty state and into
				// the transcript, so the fresh-thread animation now snapshots
				// the header rather than the draft layout.
				"tui/src/app/snapshots/codex_tui__app__owned_transcript__empty_state_animation_tests__fresh_thread_header.snap",
				"tui/src/app/snapshots/codex_tui__app__prompt_suggestions__tests__prompt_suggestion_request_history.snap",
			},
		},
		{
			Path: "tui/src/app/tests/snapshots",
			// #46566 added the unavailable-thread local-command snapshot;
			// #46691 added the two running-task exit pickers; #46751 moved the
			// two startup-warning summaries into the warnings-viewer snapshots.
			Files:    61,
			Owner:    "tui/app",
			Focus:    "app-level catalog and migration prompts",
			Priority: []string{"app", "model"},
			Required: []string{
				"tui/src/app/tests/snapshots/codex_tui__app__tests__model_catalog__model_migration_prompt_shows_for_hidden_model.snap",
				"tui/src/app/tests/snapshots/codex_tui__app__tests__background_task_defaults_tests__command_center_retained_worktree_error.snap",
				"tui/src/app/tests/snapshots/codex_tui__app__tests__transcript_composer__transcript_close_restores_inline_draft.snap",
				"tui/src/app/tests/snapshots/codex_tui__app__tests__background_exit_tests__remote_disconnect_exit.snap",
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
			Path: "tui/src/bottom_pane/chat_composer/snapshots",
			// #46734/#46749/#46751 added the transcript find/copy footers, the
			// mention menu above history, the status-surface layouts and the
			// warning-notice styles.
			Files:    29,
			Owner:    "tui/bottom_pane/chat_composer",
			Focus:    "draft and voice composer layout snapshots",
			Priority: []string{"composer"},
			Required: []string{
				"tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__snapshot_tests__draft_composer.snap",
				"tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__snapshot_tests__voice_composer.snap",
				"tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__footer_state__tests__transcript_find_copy_footer.snap",
				"tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__warning_notice__tests__warning_notice_styles.snap",
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
			// #46680-#46710 added the shared picker presentation and filled-tab
			// snapshots; #46734/#46749/#46751 added the clipped mention popup,
			// the customized shortcut overlays and the narrow warnings view.
			Files:    261,
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
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__warnings_view__tests__warnings_narrow.snap",
				"tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__shortcut_overlay__tests__shortcut_overlay_customized_wsl.snap",
			},
		},
		{
			Path: "tui/src/bottom_pane/tests/snapshots",
			// #46692 added the promoted action banner plus the three picker
			// hint presentations.
			Files:    7,
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
			Files:    9,
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
			// two Windows sandbox picker fallbacks; #46751 replaced the
			// warnings-summary presentation and #46752 added the tmux
			// pets-unavailable notice; #48015 added the declined-tool-suggestion
			// snapshot.
			Files:    314,
			Owner:    "tui/chatwidget, tui/tea",
			Focus:    "main chat widget terminal snapshots for status lines, approvals, plugins, hooks, review, usage, and unified exec",
			Priority: []string{"approval", "status", "history", "unified-exec", "review"},
			Required: []string{
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__status_widget_active.snap",
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__status_widget_and_approval_modal.snap",
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__unified_exec_begin_restores_working_status.snap",
				"tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__declined_tool_suggestion.snap",
			},
		},
		{
			Path: "tui/src/chatwidget/tests/snapshots",
			// #46574/#46565 added the question-notification and activity-group
			// ordering snapshots; #46711/#46731 added the history-projection and
			// dynamic-activity ones; #46732/#46734 added the transcript-copy
			// outcomes and the completion-only replay exploration group.
			Files:    70,
			Owner:    "tui/chatwidget",
			Focus:    "chatwidget approval request modal, async question reply, and history snapshots",
			Priority: []string{"approval", "history"},
			Required: []string{
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_modal_exec.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_history_decision_approved_short.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__exec_flow__compact_exploration_with_failures.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__history_projection__snapshot_formatter_reasoning_matches_compact_and_detailed_replay.snap",
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__dynamic_activity_tests__interrupted_dynamic_activity.snap",
			},
		},
		{
			Path:     "tui/src/clipboard_copy/snapshots",
			Files:    3,
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
			Path: "tui/src/exec_cell/snapshots",
			// #46711 added the grouped-history transcript presentation.
			Files:    2,
			Owner:    "tui/exec_cell",
			Focus:    "bounded live command output preview and final transcript rendering",
			Priority: []string{"history-cell", "unified-exec"},
			Required: []string{
				"tui/src/exec_cell/snapshots/codex_tui__exec_cell__render__tests__truncated_live_output_preview_and_transcript.snap",
				"tui/src/exec_cell/snapshots/codex_tui__exec_cell__transcript__tests__raw_grouped_history_retains_terminal_input_and_omits_reasoning.snap",
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
			// #48101 added the truncated multiline `/ps` command preview.
			Files:    91,
			Owner:    "tui/history_cell",
			Focus:    "history cell rendering for exec, MCP, plan updates, errors, sessions, user messages, and web search",
			Priority: []string{"history-cell", "mcp", "status"},
			Required: []string{
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__single_line_command_compact_when_fits.snap",
				"tui/src/history_cell/snapshots/codex_tui__history_cell__tests__ps_output_multiline_long_command_snapshot.snap",
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
			Files:    25,
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
			// #46695 added the four restricted/long-path trust-directory pickers;
			// #46752 added the animated welcome logo at 160x48.
			Files:    11,
			Owner:    "tui/onboarding",
			Focus:    "trust-directory onboarding states",
			Priority: []string{"onboarding"},
			Required: []string{
				"tui/src/onboarding/snapshots/codex_tui__onboarding__trust_directory__tests__renders_snapshot_for_git_repo.snap",
				"tui/src/onboarding/snapshots/codex_tui__onboarding__trust_directory__tests__folder_picker_restricted_40x24.snap",
				"tui/src/onboarding/snapshots/codex_tui__onboarding__welcome__tests__welcome_logo_160x48.snap",
			},
		},
		{
			Path: "tui/src/pager_overlay/snapshots",
			// #46719 moved the transcript-overlay snapshots next to the module
			// it extracted them from; #46721 added the non-macOS variant.
			Files:    3,
			Owner:    "tui/pager_overlay",
			Focus:    "paginated transcript overlay presentation and its non-macOS variant",
			Priority: []string{"history", "app"},
			Required: []string{
				"tui/src/pager_overlay/snapshots/codex_tui__pager_overlay__transcript_tests__transcript_overlay_paginated_history_states.snap",
				"tui/src/pager_overlay/snapshots/codex_tui__pager_overlay__transcript_tests__transcript_overlay_completed_stream.snap",
				"tui/src/pager_overlay/snapshots/codex_tui__pager_overlay__transcript_tests__transcript_overlay_paginated_history_states_non_macos.snap",
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
			// #46680-#46710 added the picker and pager-overlay snapshots;
			// #46719/#46721 moved the seven transcript-overlay ones into
			// tui/src/pager_overlay/snapshots, and #46733/#46750 added the
			// owned startup draft layout and the wheel/selection autoscroll
			// guard.
			Files:    189,
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
				"tui/src/snapshots/codex_tui__startup_draft__layout__tests__owned_startup_layout.snap",
				"tui/src/snapshots/codex_tui__transcript_view__tests__wheel_stops_selection_autoscroll.snap",
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
			Path: "tui/src/transcript_view/snapshots",
			// #46720-#46739 added the interactive transcript view: layout and
			// text helpers, follow control, prompt header, search,
			// selection hints, composer gap, and the compact/detailed copy
			// presentations.
			Files:    33,
			Owner:    "tui/transcript_view",
			Focus:    "interactive transcript viewport: layout cache, text/tab rendering, prompt header, search, selection hints, and copy feedback",
			Priority: []string{"history", "app", "composer"},
			Required: []string{
				"tui/src/transcript_view/snapshots/codex_tui__transcript_view__footer__tests__compact_navigation_hints.snap",
				"tui/src/transcript_view/snapshots/codex_tui__transcript_view__text__tests__compact_command_copy.snap",
				"tui/src/transcript_view/snapshots/codex_tui__transcript_view__search__tests__selection_owns_find_highlight.snap",
			},
		},
		{
			Path:     "tui/src/status/snapshots",
			Files:    24,
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
			Files:    7,
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
		{
			// #46864 split the analytics activity chart into its own module with
			// a palette snapshot; #46832 added the effect-gated status indicator
			// and configured-shortcut tooltip snapshots.
			Path:     "tui/src/analytics/activity_chart/snapshots",
			Files:    1,
			Owner:    "tui/analytics",
			Focus:    "activity-chart palette selection against the effective terminal color level",
			Priority: []string{"analytics", "status"},
			Required: []string{
				"tui/src/analytics/activity_chart/snapshots/codex_tui__analytics__activity_chart__palette__tests__current_palette_uses_effective_terminal_color_level.snap",
			},
		},
		{
			Path:     "tui/src/status_indicator_widget/snapshots",
			Files:    1,
			Owner:    "tui",
			Focus:    "status shimmer and progress effects obeying the master toggle",
			Priority: []string{"status"},
			Required: []string{
				"tui/src/status_indicator_widget/snapshots/codex_tui__status_indicator_widget__effects_tests__shimmer_and_progress_are_independent_and_obey_master_switch.snap",
			},
		},
		{
			Path:     "tui/src/tooltips/snapshots",
			Files:    1,
			Owner:    "tui",
			Focus:    "configured keybinding tips rendered at narrow widths",
			Priority: []string{"app", "composer"},
			Required: []string{
				"tui/src/tooltips/snapshots/codex_tui__tooltips__keybinding_tests__configured_shortcut_tips_render_at_narrow_width.snap",
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
