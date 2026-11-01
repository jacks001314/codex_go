# Real-usage alignment (usealign lane)

Running record for `.agents/skills/use/SKILL.md`: drive the installed Rust
Codex CLI and the freshly built Go CLI side by side with the real model
provider, compare screen and protocol data, then close the Go-side gaps.

Session: 2026-10-31, Go `1.0.6` (`bin\codex.exe`, rebuilt), Rust reference
`codex-cli 0.166.0` (unpacked into `.tmp-usealign/rust166`; the global npm
install is still 0.165.0 and was left untouched).

## How the two CLIs were run

| Side | Command | Home | Model |
|---|---|---|---|
| Rust | `codex --no-daemon` / `codex exec` | `%USERPROFILE%\.codex` | `deepseek-v4-flash` via `qax` |
| Go | `bin\codex.exe --no-daemon` / `bin\codex.exe exec` | `%USERPROFILE%\.gcode` | `deepseek-v4-flash` via `qax` |

`CODEX_HOME` must be set per side: the agent shell exports `CODEX_HOME=1`, and
the two CLIs keep their own homes (`~/.codex`, `~/.gcode`). Both point at the
local `qax` gateway (`http://127.0.0.1:40001/v1`), so every comparison below
used the real model, not a mock.

Harness: `.tmp-usealign/` (untracked) - `harness.py` drives either binary in a
ConPTY (pywinpty) at 120x40 and renders the screen with pyte; `tui_run.py`
runs named scenarios and writes `out/<scenario>-<impl>.txt`; `dbg_*.py` are
one-off captures.

## Differences found and closed

1. **Onboarding copy** - Go said `3. Provide your own API key`; Rust
   (`onboarding/auth.rs`) says `Use an OpenAI API key`. Bedrock was
   `Set up Amazon Bedrock` / "Use an AWS profile, access keys, ..." in Go and
   `Use Amazon Bedrock` / `Connect using your AWS credentials` in Rust.
   Fixed in `tui/onboarding/auth.go`.
2. **Onboarding header** - With the Bedrock/Vertex wizards offered, Rust swaps
   the paid-plan call to action for `Choose how you want to use Codex.`
   (`render_pick_mode`), and always renders
   `Welcome to Codex, OpenAI's command-line coding agent` above it. Go showed
   the paid-plan header and no welcome line. Fixed in
   `tui/onboarding/auth_flow.go` (+ `tui/onboarding/welcome.go` constant).
3. **Missing `/setup-default-sandbox`** - Rust advertises the command while
   the legacy restricted-token sandbox is active and elevation is allowed;
   Go's popup gated on `windowsSandboxLevel`, which was never initialized from
   config, so the entry never appeared. The startup level is now resolved in
   `app/windows_sandbox_tui.go` and passed through
   `tui/tea/model.go` (`Options.WindowsSandboxLevel`); the popup also checks
   that elevation is allowed (Rust `windows_sandbox_config.allows(Elevated)`).
4. **`$` skill popup layout** - Rust puts `[Skill]` in the description column
   (`selection_row_layout.rs`: max name width + 2, capped at 70% and at
   `width - tag`), then the description. Go right-aligned the tag at the popup
   edge. Fixed in `tui/tea/skill_popup.go`; the column test now pins the Rust
   snapshot text (`"  Shared  [Skill]"`, `"  Shared  [Skill] Secondary details"`).
5. **Default sandbox in a trusted project** - Rust
   `ConfigToml::derive_permission_profile` defaults to `workspace-write` when
   no `sandbox_mode` / permission profile is configured but the active project
   carries a trust decision; Go resolved `read-only`. Real evidence before the
   fix: `codex exec` printed `sandbox: read-only` (Go) vs
   `sandbox: workspace-write [workdir, /tmp, $TMPDIR]` (Rust) in the same
   trusted repository, and `/status` showed `Read Only (Ask for approval)` vs
   `Workspace (Ask for approval)`. Fixed in `config/permissions.go`
   (`trustedProjectDefaultSandboxMode`, project root discovered from cwd) and
   surfaced to the TUI in `app/interactive.go`; untrusted/unknown directories
   still resolve read-only.

6. **Startup diagnostics rendered inline** - Go kept a persistent
   `⚠ N startup issue(s) · ctrl+t for details` row under the splash and folded a
   `restart with /daemon` hint into it. Rust #46751 emptied
   `StartupWarningsCell::display_lines` (the footer badge plus the f2 viewer own
   the count) and #53173 moved the managed-daemon restart hint into the warnings
   viewer. Fixed in `tui/history_cell/startup_warnings.go` (display lines empty,
   `SummaryLine`/hint fields dropped), `tui/tea/model.go`
   (`renderStartupWarnings` keeps one transcript-only entry, `addHistoryCell`
   merges startup cells), and `tui/bottom_pane/warnings_view.go` +
   `tui/tea/warnings_view.go` + `tui/chatwidget/warnings.go` (viewer hint).
   Evidence: `out/startup-00-start-go.txt` no longer shows the banner,
   `out/startup-00-start-rust.txt` never did, and both
   `out/ctrl-t-warnings-{go,rust}.txt` still carry
   `⚠ Codex is ignoring 1 unrecognized configuration setting` in Ctrl+T.
7. **Footer warning badge clipped** - Go passed the whole terminal width to
   `WarningNoticeLine`, but the footer row is drawn after a two-column indent
   (`renderFooterHelpSection`), so at 120 columns the badge degraded to
   `⚠ 1 warning · f2 to v…` while Rust printed `⚠ 1 warning · f2 to view`.
   Fixed in `tui/tea/model.go`; regression
   `TestPassiveFooterWarningNoticeFitsIndentLikeRust`.

8. **East Asian ambiguous width model** - With the process locale in a CJK
   region `go-runewidth` treats east-asian-ambiguous code points (`←` U+2190,
   `·` U+00B7, `⚠` U+26A0, `…`) as two columns, while Rust `unicode-width`
   (and the terminal) count one. Every such glyph stole a column from the
   layout budget, so the footer badge right edge landed at column 93 in Go vs
   95 in Rust at 120 columns, and wrapping/truncation drifted wherever those
   glyphs appeared. Fixed in `tui/width_condition.go`: `init()` pins
   `runewidth.EastAsianWidth = false`, `runewidth.DefaultCondition.EastAsianWidth
   = false` and rebuilds the LUT with `CreateLUT()`. Regression
   `TestEastAsianAmbiguousWidthsMatchRust` asserts each glyph is one column and
   that `← for agents · ? for shortcuts` is 30 columns; this also un-clipped the
   footer back to the full `⚠ 1 warning · f2 to view`. Evidence:
   `out/startup-00-start-go.txt` vs `out/startup-00-start-rust166.txt` - footer
   rows are both `len=119, warnAt=95`; `out/cjk-rust166.txt` vs `out/cjk-go.txt`
   wrap a Chinese prompt and answer at identical columns.

9. **Missing model-metadata fallback warning** - After one real turn Rust 0.166
   reported `⚠ 2 warnings` while Go stayed at `⚠ 1 warning`. The second Rust
   warning is "Model metadata for `deepseek-v4-flash` not found. Defaulting to
   fallback metadata; this can degrade performance and cause issues.`
   (`Session::maybe_emit_model_warnings_for_turn`, emitted whenever the turn's
   `ModelInfo` origin is `Synthetic`). Go had the TUI-side dedupe
   (`chatwidget.WarningDisplayState`) and the synthetic-origin/`UsedFallbackModelMetadata`
   resolution, but no producer, and the embedded TUI dropped exec `warning-*`
   error items entirely. Fixed in `appserver/turn_runtime.go`
   (`maybeEmitModelMetadataWarning`), `exec/exec.go`
   (`fallbackModelMetadataWarning`, covering the embedded TUI and `codex exec`),
   `app/interactive.go` (`interactiveWarningMessageFromItem` routes `warning-*`
   items into the warning display state) and `tui/tea/model.go`
   (`WarningEventMsg`). Regressions:
   `TestTurnStartWarnsForFallbackModelMetadataLikeRust`,
   `TestFallbackModelMetadataWarningMatchesRust`,
   `TestRuntimeWarningEventCountsInFooterAndViewerLikeRust`. Evidence:
   `out/warnturn-viewer-rust166-w2.txt` (Rust `2 of 2`) vs
   `out/warnturn4-go.txt` + `-v1`/`-v2` (Go now `⚠ 2 warnings`, and the viewer
   lists the same message).

10. **Startup-window coalescing and warning order** - The f2 viewer still
    differed after entry 9: Rust showed `1 of 2 · Startup` / `2 of 2 · Startup`
    (config warning first), Go showed `1 of 2 · Warning` (model warning first).
    Rust's `ServerNotification::Warning` arm sends an ordinary warning into
    `StartupWarningsCell` while `startup_complete` is false - the cell keeps its
    diagnostics in a `BTreeSet`, so they print in sorted order - and only uses a
    `Warning` history cell afterwards; the warnings are also emitted before
    `turn.started`. Go appended a `Warning` cell regardless of the window and
    emitted the warnings after `turn.started`. Fixed in `exec/exec.go` (the
    stream collector and the config/model/sandbox warnings now precede
    `turn.started`), `tui/tea/model.go` (`WarningEventMsg` merges into the
    startup cell until `startupWarningsComplete`, leaving the status-warning
    path untouched like Rust's `on_warning`) and `tui/tea/warnings_view.go`
    (`retainedWarningCells` walks the startup cell before runtime warning
    cells). Regression:
    `TestRuntimeWarningEventCoalescesIntoStartupWindowLikeRust`. Evidence:
    `out/warnturn6-go.txt` + `-v1`/`-v2` now read `Warnings · 1 of 2 · Startup`
    and `2 of 2 · Startup`, and Go's `exec --json` emits thread.started, the
    config warning, the model warning, then turn.started - the same order as
    Rust 0.166.

11. **Unrecognized slash command and a Go-only `/help`** - Typing `/help` in
    Rust 0.166 prints
    `• Unrecognized command '/help'. Type "/" for a list of supported commands.`
    because the Rust `SlashCommand` enum has no `Help` variant; Go answered with
    its own command list. An unknown input such as `/does-not-exist` also
    differed: Go wrote the footer notice
    `Unknown command /does-not-exist. Type /help for commands.`. Fixed by
    dropping the `help` frame and its `/?` parse arm, adding
    `tui.UnrecognizedSlashCommandMessage`, and rendering the notice as a `•`
    info line in `tui/tea/model.go` (transcript cell) and on stdout in the
    line-mode `app/interactive.go`. The legacy welcome hint now says
    `Type / for commands or /exit to quit.` Evidence: `out/help2-rust166.txt`
    vs `out/help3-go.txt` and `out/unknown2-rust166.txt` vs `out/unknown3-go.txt`
    are now text-identical apart from the version string.

12. **Go-only slash commands removed** - Go accepted `approval`, `sandbox`,
    `attach`, `image`, `url-image`, `clear-attachments`, `editor` and
    `unarchive`; Rust 0.166 has none of them
    (`codex-rs/tui/src/slash_command.rs:12-84`, where `permissions` covers
    approval and sandbox). Every one of them answers
    `• Unrecognized command '/<name>'. Type "/" for a list of supported commands.`
    in the real binary (`out/attach-,image-,url-image-,clear-attachments-,
    editor-,unarchive-,approval-,sandbox--rust166-rust166.txt`). Fixed by
    dropping the eight frames in `tui/slash_command.go`, their `ParseCommand`
    arms in `tui/state.go`, the dispatch arms in `tui/tea/model.go` and
    `app/interactive.go`, and the then-unused
    `applyAttachmentCommand`/`clearAttachments`/`applyApprovalSetting`/
    `applySandboxSetting` helpers. Ctrl+G still opens the external editor, and
    attachments still reach the composer through paste/drop.
    Side finding while testing arguments: Rust submits a *known* command with
    arguments as a prompt when it does not support inline arguments
    (`chatwidget/slash_dispatch.rs:1201`), but an *unknown* name with arguments
    still prints the info line (`find_slash_command` fails first). Go turned
    `/does-not-exist foo` into a prompt. `slashInvocationDispatchable` now
    mirrors `parse_slash_name`/`find_slash_command`, including Rust's rule that
    path-like names (`/tmp/$imagegen file`) stay ordinary prompts.
    Evidence: `out/attach-go-go.txt`, `out/approval-go-go.txt` and
    `out/unknownargs-{rust166,go}.txt` are text-identical to the Rust captures,
    and the `/` popup (`out/slash-menu-01-{rust166,go}.txt`) now differs only in
    the version line.

13. **Slash popup column width while scrolling** - Scrolling the `/` popup in
    Rust keeps the description column fixed (`ColumnWidthMode::AutoAllRows`,
    `bottom_pane/command_popup.rs:27-31` and
    `selection_popup_common.rs:150-170`: the column is measured over every
    filtered row and clamped to `min(content_width-1, 70%)`). Go measured only
    the visible slice, so the description column jumped left as soon as the
    eight-row window moved past `/setup-default-sandbox`. Fixed in
    `tui/tea/slash_popup.go` (`slashPopupNameWidth` now takes the content width
    and measures all rows). Filtering had the same shape of bug from an
    artificial `max(/experimental, …)` floor plus a 28-column cap, which Rust
    does not have. Evidence: `out/slashscroll-{0..4}-{rust166,go}.txt` are
    byte-identical apart from the version line across four scrolled pages, and
    `out/slashfilter-{qu,a,s}-{rust166,go}.txt` match after filtering.
    Regression test: `TestSlashPopupNameColumnStaysPutWhileScrollingLikeRust`.

14. **`$` skill popup frame** - Rust wraps the skill list in the shared picker
    frame (`bottom_pane/skill_popup.rs:229-268`, `bottom_pane/picker_rows.rs`):
    the rows are inset, the scrolled-away side gets an `↑`/`↓` overflow row at
    column 0, and a `  enter insert · esc close` hint sits below the list. Go
    rendered the eight rows only, so the popup ended at the last skill row and
    also re-measured its name column from the visible slice. Fixed in
    `tui/tea/skill_popup.go`: `renderSkillPopup` emits the overflow rows and the
    hint, `skillPopupRows` reserves the same rows, and `skillPopupNameWidth`
    measures every item with Rust's 70% clamp. Evidence:
    `out/dollar-menu-01-{rust166,go}.txt` and
    `out/skillscroll-{1..3}-{rust166,go}.txt` are identical apart from the
    version line. Regression test:
    `TestSkillPopupRendersPickerFrameLikeRust`.

15. **Popup scroll window when scrolling back** - Both popups derived their
    window from the selection alone (`start = selected - maxRows + 1`), so
    stepping up after reaching the end dragged the list down one row per press
    and the `↓` marker reappeared. Rust keeps a persistent `ScrollState`
    (`bottom_pane/scroll_state.rs`, window arithmetic in
    `selection_popup_common.rs:572-586`): the window stays where it was until
    the selection leaves it. Fixed by adding `ScrollTop` to both popup states
    and a shared `popupVisibleRange` in `tui/tea/slash_popup.go`; the filter
    refresh keeps the offset for an unchanged query and resets it for a new
    one. Evidence: `$` at the end and after two `↑`
    (`out/skillmid-{0,1,2}-{rust166,go}.txt`) and the same for `/`
    (`out/slashmid-{0,1,2}-{rust166,go}.txt`) are identical apart from the
    version line. Regression test:
    `TestPopupWindowStaysPutWhenScrollingBackLikeRust`.

16. **`/skills` picker copy and frame** - Rust's `open_skills_menu`
    (`chatwidget/skills.rs:32-58`) hardcodes the `$` shortcut in the row text
    and keeps both descriptions free of trailing periods, while
    `open_skills_list` always inserts `$` regardless of `mentions_v2`. Go
    followed the mentions feature (`Tip: press @ … directly.`,
    `Enable or disable skills.`) and printed a Go-only
    `Skills list shortcut inserted.` notice after Enter. Fixed in
    `tui/chatwidget/skills.go` (`OpenSkillsListInsert` has no argument,
    `NewSkillsMenuView` matches the Rust copy) and
    `tui/tea/slash_misc_commands.go` (notice removed). The frame also moved to
    Rust's bottom-pane list layout: title and subtitle are one header block, two
    blank rows follow it (header gap + blank scroll row), and one blank row
    separates the results from the footer hint. That layout is applied to every
    modal built through `openSelectionViewModal` (new `modalState.selectionView`
    flag), while centered confirmation overlays keep the old spacing. Evidence:
    `out/skills1-{rust166,go}.txt` and `out/skills2-{rust166,go}.txt` are
    byte-identical apart from the version line.

17. **`/permissions` picker** - Rust 0.166 always discovers through the app
    server (`permission_discovery.rs:83-172`, no `default_permissions` gate) and
    then renders `open_permission_profiles_popup`
    (`chatwidget/permissions_menu.rs:148-260`): rows are Ask for approval
    `(current)`, Approve for me, Full Access, Read Only in that order, the
    `(current)` marker belongs to the row name, descriptions wrap at the pane
    edge and align their continuation under the description column, and the
    footer is the compact picker hint with no non-admin-sandbox note. Go fell
    back to the legacy preset picker (`Read Only` first, a long sandbox
    paragraph in the body, `(current)` inside the description, clipped text,
    `Esc cancel | Enter choose`). Fixed by (a) adding
    `OnListPermissionProfiles` to the embedded TUI
    (`app/interactive_permissions.go`, mirroring
    `RuntimeRouter.handlePermissionProfileList`), (b) always opening the profile
    picker after discovery and reusing the cached catalog, (c) filtering the
    built-in `:` ids out of the custom rows, adding Rust's empty-catalog
    subtitle, and moving `(current)` onto the label, (d) rendering
    `ModalKindPermissions` as a selection view with the compact hint, and (e)
    wrapping row descriptions for selection views. Evidence:
    `out/perm-rust166.txt` vs `out/perm-go.txt` are byte-identical apart from
    the version line.

18. **Full-access confirmation** - Rust opens this dialog through
    `SelectionViewParams::confirmation()` (`chatwidget/permission_popups.rs:425-521`):
    a centered dialog over the retained composer whose header paragraph (bold
    `Enable full access?` plus the wrapped warning) is laid out in the dialog
    rect, followed by the picker's header gap and blank scroll rows, and a
    `enter select · esc back` hint. Go rendered it as an inline
    `ModalKindPermissions` modal: the warning was clipped at the terminal edge,
    no dialog rect was applied, and the footer read
    `Esc cancel | Enter choose`. Fixed with a `confirmationView` modal flag that
    routes the view through `renderCenteredModalOverlay`, wraps the header at
    `width-4`, and keeps the picker layout; `openFullAccessConfirmation` sets the
    compact hint. The centered backdrop also re-enabled the empty-state blossom
    because it renders `View()` with `modal` cleared, so
    `suppressWelcomeBlossom` now hides it (Rust keeps the animation out of view
    stacks). Evidence: `out/permfull-{rust166,go}.txt` are identical apart from
    the version line and the known `← for agents` footer item.

19. **`/daemon` menu and its confirmations** - Rust builds the source menu
    with `SelectionViewParams::picker()` from a two-line header paragraph
    (bold `Daemon` plus the dim status) and three rows where the restart row is
    disabled without a number prefix; the two confirmations use
    `SelectionViewParams::confirmation()` (centered) with
    `require_explicit_confirmation` on the destructive row. Go opened the
    menu as a centered dialog with a duplicated `Daemon` heading, rendered the
    restart row as `Restart background server [disabled] Restart is available
    only for a managed local background server.` with an unwrapped reason, and
    the restart confirmation still used the older copy
    (`Codex will reconnect automatically when the server is ready.` /
    `Restart and reconnect`). Fixed with a `inlinePicker` modal flag that keeps
    a `ModalKindGeneric` picker in the bottom pane, Rust's
    `selection_row_layout.rs` numbering/copy for list rows (enabled rows take
    the ordinal, disabled rows keep the indent, reasons append as
    `(disabled: ...)`), a header that holds only the status, and a restart
    confirmation with Rust's copy plus an armed first enter. Evidence:
    `out/daemon4-go.txt` vs `out/daemon-rust166.txt` are byte-identical apart
    from the version line; `TestDaemonRestartConfirmationMatchesRustSnapshot`
    pins the dialog block to
    `codex_tui__app__daemon_menu__tests__daemon_restart_confirmation.snap`.

20. **`/mcp` output** - Rust's empty inventory is a history cell
    (`history_cell/mcp.rs::empty_mcp_output`) followed by nothing else, and the
    populated list is `🔌  MCP Tools` / rows / `Use /mcp verbose for tools and
    resources.`. Go additionally wrote a `MCP` footer notice, a
    `Loading MCP inventory...` notice that never cleared, and printed the docs
    URL inline instead of Rust's `MCP docs` hyperlink; the empty-state blossom
    also stayed on screen because `hasConversationMessages` only looked at
    user/assistant turns. Fixed by dropping the two Go-only notices, linking
    `MCP docs` through `tui.OSC8Hyperlink`, and porting Rust's
    `empty_state_animation::is_startup_cell` as `historycell.IsStartupCell` with
    a latched `welcomeBlossom.dismissed` flag that any non-startup cell sets
    (and `resetThreadScopedState` clears). Evidence:
    `out/mcp5-go.txt` matches Rust's `empty_mcp_output` copy line for line, and
    the populated render for the three servers Rust listed in
    `out/mcp166-rust166.txt` is byte-identical.

21. **`/diff` pager** - Rust opens `Overlay::new_static_with_lines` (a
    `PagerView`) titled `D I F F`: header `/ TITLE` drawn over a dim `/ ` fill,
    content from the top, `~` filler rows, a rule whose right edge carries
    ` N% `, then the navigation hint, the close-only hint and one blank row. Go
    reused the Ctrl+T transcript overlay: `D I F F ... 100%` header, content
    scrolled to the bottom, blank filler, and
    `q close · f3 find · esc browse prompts`. Added
    `NewStaticPagerOverlay`/`staticPager` to the overlay with Rust's geometry
    (`height-5` body, percent one column in from the edge) and pointed
    `applyDiffResult` at it, dropping the Go-only `Diff` notice. Evidence:
    `out/diff3-go.txt` reproduces Rust's chrome exactly (`out/diff166-rust166.txt`
    differs only where Rust's sandbox refused to compute the diff); pinned by
    `TestStaticPagerChromeLikeRust` and
    `TestStaticPagerStartsAtTopAndReportsScrollLikeRust`.

22. **`/debug-config`** - Rust appends
    `render_agents_config_lines` (`debug_config.rs:57-88`): a blank row,
    `[agents]:` and six bullets where unset keys print `<unset>`, `max_depth`
    falls back to `1 (V1 only; ignored by V2)` and the booleans default to
    `true`. Go printed the layer stack and requirements only, plus Go-only
    `Debug config` / `Reading debug config...` footer notices. Added
    `tui/debug_config_agents.go` (`AgentsDebugConfigFromValues`,
    `RenderAgentsConfigLines`) wired into `NewDebugConfigOutput`, and dropped
    the notices. Evidence: `out/dbg3-go.txt` reproduces Rust's `[agents]` block
    line for line (`out/dbg166-rust166.txt`); only the environment-specific
    layer list still differs.

23. **`/config` panel** - Rust renders the catalog through `ListSelectionView`
    (`config_panel/render.rs::picker` + `mod::settings`): a two-space inset, the
    tab row, six reserved result rows, the value in a 20-wide `category_tag` at
    `desc_col = max(name_column_width, prefix + widest label) + 2`, `[x] On` /
    `[ ] Off` toggles, `<label> ›` editors, disabled rows that print their
    reason (and `(disabled)` / `[-]` markers), an on-demand `/` search
    (`Search › ...` row, `Section · description` results) and the
    `left/right group · / search · enter <accept> · esc clear search/close`
    hint. Go rendered its own list: no indent, `github  — ...` descriptions,
    a permanent `Type to search` row, `Status line` / `Terminal title` labels, a
    `type to search · ←/→ tabs · enter open · esc close` footer, and
    letters typed straight into a filter. Rewrote `configPanelRows` to Rust's
    labels/descriptions/keywords, `configPanelValue` to
    `ConfigSettingValue::label`, added
    `configPanelRowDisabledReason` / `configPanelOptionDisabledReason`, rebuilt
    `renderConfigPanel` on `compute_desc_col` + `build_full_line` semantics
    (reserved result rows, `↑`/`↓` overflow rows, `SelectedBelowWhenNarrow`
    below 45 description columns) and added `renderConfigEffectsPanel` for the
    numbered Effects child picker. Search is on-demand (`/`) and Esc leaves it,
    and the `list` keymap (`j`/`k`/`h`/`l`, wrapping Up/Down, Home/End,
    PgUp/PgDn, Esc) now drives the panel while plain letters stay inert.
    Evidence: `out/cfgtab{0..4}-{rust166,go}.txt`, `out/cfgeffects-*`,
    `out/cfgsearch0-*`, `out/cfgs-{v,x,vim}-*`, `out/cfgesc-{before,after}-*`,
    `out/cfgjjk-*`, `out/cfgwrap-*` and `out/cfgl-*` are byte-identical; the only
    difference is the Theme value (`github ›` vs `Automatic ›`) because
    `~/.gcode` sets `tui.theme` and `~/.codex` does not. Running the Go build with
    `CODEX_HOME=~/.codex` produces `out/cfgsharedhome-go.txt`, a 0-diff match of
    `out/cfgtab0-rust166.txt`.



24. **`/config` editor child pickers** - Rust never cycles those five editors in
    place: `config_panel/render.rs::{notification_condition, session_picker_layout,
    resume_directory, copy_on_select, right_click_paste}` each push a
    `SelectionViewParams::picker()` child view onto the view stack, so the row
    opens `Notify when` / `Session picker layout` / `Resume directory` /
    `Copy on select` / `Right-click paste` with a numbered list, `(current)`
    markers, `enter select · esc back`, `initial_selected_idx` = the current
    value (falling back to the first enabled row) and numeric shortcuts that
    count enabled rows only. Go cycled the value on `enter` from the parent row.
    Added `configPanelChoice` to `configPanelState`, `openConfigPanelChoice`,
    `applyConfigPanelChoiceItem`, `moveConfigPanelChoice`,
    `jumpConfigPanelChoice`, `updateConfigPanelChoiceKey` and
    `renderConfigPanelChoice`; the parent does not move while the child is
    open, `Esc` backs out to the panel, `Ctrl+C` closes it, and a disabled row
    keeps an empty number gutter plus ` (disabled)` and its reason in the
    description column. Evidence: `out/cfgnotify-*`, `out/cfgsession-*`,
    `out/cfgresume-*`, `out/cfgcopy-*` and `out/cfgrightclick-*` are 0-diff at
    120x40 (version line aside), `out/cfgpick-pick{1,2}-*` show both the
    `Always ›` write and the revert to `Unfocused ›`, and
    `TestConfigPanelResumeDirectoryRemoteRenderLikeRust` pins Go's rendering to
    Rust's `config_resume_directory_remote` snapshot.

25. **`/config` → Keyboard shortcuts (`/keymap`)** - Rust 0.166 pushes a
    `SelectionViewParams::picker()` with `PickerSurface::Panel` and
    `max_visible_rows = 24`, so the page owns the whole window: a leading blank
    row, `Keymap`, the per-tab description and `N actions, M customized, K
    unbound.`, a filled tab bar (`All / Common / Customized (N) / Unbound (N) /
    App / Composer / Editor / Vim / Navigation / Agents / Approval / Debug`)
    with a `‹`/`›` window when the bar overflows, `Type to search shortcuts`,
    `Group  - Name  binding` rows (`*` custom, `-` unbound), a `↓`/`↑` scroll
    indicator and `left/right group · enter edit shortcut · * custom · -
    unbound · esc close`. Go still opened the older numbered
    `global:open_transcript` picker (`out/cfgkeymap-{rust166,go}.txt`).
    Go now builds the page from the same catalog: `tui/keymap_page.go` adds
    `KeymapPageRow`/`KeymapPageTab`/`KeymapPageRows`/`KeymapPageTabs`,
    `KeymapEmptyRow`, the 21-entry `keymapCommonActions` list and the seven
    context tabs (154 rows after hiding the two Go-only actions), and
    `tui/tea/keymap_panel.go` renders it with `openKeymapPicker`,
    `renderKeymapTabBar`, `keymapPanelDescriptionColumn`, the 24-row result
    window, PgUp/PgDn/Home/End, wrap-around Up/Down, left/right tab cycling and
    type-to-search over label, description and binding summary. The panel's
    chrome is measured so a 24-row terminal collapses the transcript to zero
    rows exactly like Rust instead of keeping one history line above the page.
    Rust's catalog order and Vim spellings are now the Go catalog order too:
    `vim_normal.enter_replace_mode`, `find_forward`, `find_backward`,
    `till_forward`, `till_backward`, a separate `vim_search` context
    (`forward`/`backward`/`next`/`previous`) and `jump_top`/`motion_jump_top`
    without single-key defaults. `keymapschema.ActionAliases` keeps Go's old
    names (`replace_mode`, `find_char_forward`, `search_prev`, ...) valid for
    configs written by earlier Go builds, and `pager.find` defaults to
    `f3, /` like Rust. The picker also mirrors Rust's
    `key_parts_to_config_key_spec` filter (the Insert key default of
    `enter_insert` stays out of the summary) and the implicit `g g` chord Rust
    installs for the buffer-top jumps.
    Evidence: `out/km{0..12}-*` at 120x40 differ only on the version line
    (`out/km120n*`), `out/km80n2{0..12}-*` at 80x24 are byte-identical, the
    searches `out/kmsearch-{app,right,vim}-*`, the Vim search rows
    `out/kmvims-*` and the Vim tab `out/kmvim120b-go.txt` vs
    `out/km7-rust166.txt` (version line only) all match. Regressions:
    `TestKeymapPageRowsLikeRust`, `TestKeymapPageVimTabLikeRust`,
    `TestKeymapConfigAppliesUpstreamVimActionsLikeRust`,
    `TestKeymapConfigAppliesLegacyGoVimAliases`,
    `TestKeymapConfigRejectsAliasCanonicalConflictLikeRust` and the rewritten
    `TestModelKeymapCommandOpensGuidedPickerAndCapturesKey`.
    The Debug tab now opens Rust's live `Keypress Inspector`
    (`keymap_setup/debug.rs`): `tui/tea/keymap_debug_inspector.go` captures
    every non-`Ctrl+C` keypress (`Esc` included), renders `Keypress Inspector`,
    `Detected:` / `Config key:` / `Raw event:` and the catalog-ordered
    `Assigned actions:` list, wraps with textwrap-style initial/subsequent
    indents, keeps the short tip until the 3s `MISSING_KEY_HINT_DELAY` elapses,
    and returns to the `/keymap` Debug tab on `Ctrl+C`. `tui/keymap_page.go`
    exports `KeymapStorableSpec`, `KeymapActionMatch` and
    `KeymapActionsForKey` (catalog order, `keymapPageHiddenActions` skipped,
    `Custom` / `Custom global` / `Default` sources), and `tui/tea/model.go`
    routes raw `KeyMsg`s to the inspector before any other handling.
    Evidence: `out/kmdbg-{debug,open,k,ctrlk,esc,A,left,close}-go.txt` (Rust
    0.166) vs `out/kmdbggo2-*`, and
    `out/kmdbgx-{insert,space,f1,home,pgdn,ctrlspace,backspace,tab,close}-go.txt`
    vs `out/kmdbgxgo2-*`; every snapshot differs only on the version banner
    except Insert and Ctrl+Space (see Still open). Regressions:
    `TestKeymapInspectorKeypressReportsLikeRust`,
    `TestKeymapInspectorUnsupportedKeyLikeRust`,
    `TestKeymapInspectorDelayedHintLikeRust`,
    `TestKeymapInspectorCtrlCClosesToDebugTabLikeRust`,
    `TestKeymapActionsForKeyLikeRust`,
    `TestKeymapActionsForKeyBindingSourceLikeRust`.

26. **`codex exec --json` command execution payload** - Rust 0.166 reports a
    command call as `CommandExecutionPresentation::from_raw` over the argv core
    resolved for the model's script (`core/src/shell.rs::derive_exec_args`,
    shlex-joined by `app-server-protocol/src/protocol/item_builders.rs`), so the
    JSON `command` is the whole shell invocation:
    `"C:\\...\\pwsh.exe" -Command 'echo hi'` (no `-NoProfile` because the
    session allows login shells), and `exec/src/exec_events.rs`
    `CommandExecutionItem` carries exactly
    `command`/`aggregated_output`/`exit_code`/`status` with `exit_code` present
    as `null` while the process runs. Go printed the bare script
    (`"command":"echo hi"`), added an internal
    `"metadata":{"source":"unifiedExecStartup"}` marker and omitted
    `exit_code` on `item.started`. `exec/exec.go` now derives the same argv with
    `execShellDisplayCommand` (`tool.ResolveCommand` + `shell.ShlexJoin`, using
    the turn's `allow_login_shell`), keeps the TUI's bare command and metadata
    on the in-process event, and only rewrites the JSON encoder's copy:
    `assignExecItemID` clears `metadata` and swaps in the resolved argv, and
    `execJSONCommandExecutionPayload` renders Rust's exact field set/order with a
    nullable `exit_code`. Evidence: `out/execjson3-tool-{rust166,go}.txt` are
    identical for the started/completed pair, and
    `out/execjson4-fail-{rust166,go}.txt` show `exit_code: 3, status: failed`
    with an empty `aggregated_output` on both. Regressions:
    `TestExecJSONCommandExecutionUsesResolvedShellArgvLikeRust`,
    `TestExecInternalCommandItemKeepsTUICommandAndMetadataLikeRust`.

27. **`codex exec` help/version surface and clap diagnostics** - Go answered
    `exec --help` with `unknown exec option --help` (exit 1); Rust prints a full
    clap page and exits 0, and `exec -h`, `exec -V`/`--version`,
    `exec help [resume|fork|review]` and `exec resume|fork|review --help|-h`
    were all missing. `cli/exec_help.go` now carries the eight 0.166 pages
    verbatim (long `--help` and short `-h` layouts, including clap's
    ten-space trailing blank lines), `ExecHelpKindForSubcommand` resolves the
    `help` topic, and `cli.ExecOptions.Help` records the action so
    `app.runExecMeta` prints the page (or `codex-cli-exec <version>`) before any
    config, auth or model work. The same round aligned the exec family's
    argument diagnostics with clap: `unexpected argument '<arg>' found` plus the
    `-- <arg>` tip and the scope's usage block, `unrecognized subcommand
    '<topic>'`, and value parsing for `--sandbox` / `--color`
    (`invalid value ... [possible values: ...]`, the `a similar value exists`
    tip, the empty-value `a value is required ...` form, and case sensitivity -
    Go previously lowercased `--color` and accepted `--sandbox bogus` without
    complaint). All of these now exit 2 through `cli.UsageError` and
    `app.writeClapDiagnostic`, matching Rust. The round also added Rust's
    `--approve-for-me` / `--not-so-yolo` (`SharedOptions.auto_review`): it
    appends `approvals_reviewer="auto_review"`, `approval_policy="on-request"`
    and `sandbox_mode="workspace-write"` after the user's `-c` overrides and
    conflicts with `--sandbox` / the approvals bypass. Evidence: a 34-case live
    diff of `bin\codex.exe` against the 0.166 build (`exec --help|-h|help`,
    `exec help <topic>`, `resume|fork|review --help|-h`, `--bogus` for every
    scope, `--sandbox`/`--color` value and missing-value errors, and both
    `--approve-for-me` conflicts) matches stdout, stderr and exit status byte
    for byte; the only remaining diff is the version string itself
    (`codex-cli-exec 0.0.0` vs `0.166.0`). Regressions:
    `cli/exec_help_like_rust_test.go` (page selection, frozen line counts,
    diagnostics, override order) and `app/exec_help_meta_like_rust_test.go`
    (page bytes, version line, exit-2 blocks).

28. **exec stdin prompt contract, runtime error prefixes and login output streams** -
    Go always read stdin eagerly and answered an empty pipe with
    `No prompt provided. Either specify one as an argument or pipe the prompt
    into stdin.`, while Rust prints `Reading prompt from stdin...` first and then
    `No prompt provided via stdin.`; a positional prompt with piped input also
    never showed Rust's `Reading additional input from stdin...` notice, and a
    positional prompt with a TTY stdin would have blocked on a read Rust skips.
    `prompt.ReadStdinPrompt` / `ResolveExecPrompt` / `ResolveRootPrompt` now
    mirror `exec::read_prompt_from_stdin`, `exec::resolve_prompt` and
    `exec::resolve_root_prompt` (behaviors RequiredIfPiped / Forced /
    OptionalAppend, `io.IsTerminalReader` detection), and the exec runner threads
    stderr through the review, resume, fork and root prompt paths. The round also
    ordered prompt resolution before output-schema loading (Rust's
    `build_exec_request` order), accepted `-` as the resume prompt positional, and
    made `exec resume|fork` report `Session not found: <id>` like Rust. Two
    remaining output-surface gaps closed with it: anyhow-style failures now print
    as `Error: <message>` from the process entry point, while Rust's direct
    `eprintln!; exit(1)` sites (`prompt.DirectExitError`, `app.DirectExitMessage`)
    keep their verbatim text without the prefix, and `codex login`/`logout`
    status lines moved from stdout to stderr (`login.rs` uses `eprintln!`).

29. **human exec transcript and `--output-last-message` tolerance** - Rust's
    human event processor renders every completed `AgentMessage` into the
    transcript as a styled `codex` label plus the reply
    (`event_processor_with_human_output.rs:100-110`), remembers it as the final
    message, and `handle_last_message` only warns when the file cannot be
    written, leaving the run at exit 0. Go never rendered the transcript line, so
    the `codex` block after the reply was missing, and a bad
    `--output-last-message` path aborted the run with exit 1. `exec/human_render.go`
    now handles the `agent_message` completion (`renderAgentMessage`, italic plus
    magenta label, unstyled body) and records
    `agentMessageRendered` / `lastAgentMessage`; `exec.writeLastMessageFile`
    reports `Failed to write last message file "<path>": <reason>` with Rust's
    `{:?}` quoting and still writes an empty file together with the
    `Warning: no last agent message; ...` line when the turn produced no message;
    `writeHumanFinalMessage` follows `should_print_final_message_to_stdout` /
    `..._to_tty` (reply on stdout unless both streams are terminals, stderr label
    only when the transcript did not render it); and `loadOutputSchema` reports
    `ioErrorMessage` so the path prefix shared with the caller is not duplicated.
    Evidence: live qax `deepseek-v4-flash` runs of
    `exec --output-last-message D:/nonexistent-dir/x.txt "Reply with exactly: LM-OK"`,
    a plain `exec` and an `exec --json` run match Rust in stdout, stderr ordering
    and exit status; the failure case differs only in the localized Windows
    `io::Error` text. Regressions: `exec/last_message_like_rust_test.go` and the
    updated `TestRunHumanPrintsFinalMessageToStderrWhenBothStreamsAreTTY`.

30. **Windows terminal detection for stdin and final output** - Rust asks
    `std::io::{stdin,stdout,stderr}().is_terminal()`, which on Windows calls
    `GetConsoleMode`. Go decided with `os.FileInfo.Mode()&os.ModeCharDevice`,
    which also matches the `NUL` device, and `exec.isTerminalWriter` accepted only
    wrappers implementing `IsTerminal()`, so a plain `*os.File` console never
    counted as a terminal. Live symptom: with stdin on `NUL`,
    `codex exec <prompt>` printed `Reading additional input from stdin...` on
    Rust but not on Go; in a real console Go would also have kept `--color auto`
    uncoloured and repeated the final message on stdout. New
    `prompt.IsTerminalFile` (`GetConsoleMode` on Windows, character device
    elsewhere) now backs `prompt.IsTerminalReader`, `exec.isTerminalWriter` and
    `app.isSessionTerminal`. Regressions:
    `prompt/terminal_detection_like_rust_test.go` and
    `prompt/terminal_windows_like_rust_test.go`.

31. **`exec resume` / `exec fork` missing-thread surface** - Rust's
    `resolve_resume_thread_id` returns `None` for an unknown session name and for
    an empty `--last` lookup, and `exec` then calls `thread/start`, so
    `codex exec resume <ghost>` runs the prompt on a brand new thread and exits
    0; a session id is taken at face value, reaches `thread/resume`, and fails
    with `thread/resume: thread/resume failed: no rollout found for thread id
    <id> (code -32600)` (the typed-request layer adds `failed: ... (code N)` and
    exec prefixes the method again). `fork` keeps `Session not found: <id>` for an
    unresolved name and prints the same layered error with `thread/fork`. Go
    answered `Session not found: <name>` for resume, and reported
    `thread not found: <id>` for both UUID cases. `exec` now resolves through
    `resolveExecResumeRecordForMethod`, returns the sentinel
    `errExecResumeNoTarget` for Rust's `None` result, falls back to a fresh
    thread in `promptForRequest` (mapping the sentinel back to
    `Session not found` for fork), and formats missing rollouts with
    `execAppServerThreadError`. Evidence: live runs against 0.166 -
    `exec resume notarealsession "Reply with exactly: RS-OK"` (exit 0, new
    session id), `exec resume --last` in a directory without history (exit 0,
    `LAST-OK`), `exec resume <missing-uuid>` and `exec fork <missing-uuid>` (exit
    1, byte-identical messages); plain `exec fork <ghost>` still reports
    `Session not found: ghost`. Regressions:
    `exec/resume_missing_thread_like_rust_test.go` (7 cases).

32. **OS error text, `model_instructions_file`, and the `exec` CLI trailing rules** -
    Rust prints `io::Error`, whose Windows Display is the localized
    `FormatMessageW` text followed by ` (os error N)`, while Go printed
    `syscall.Errno.Error()` (`The system cannot find the file specified.`). It
    also resolves `model_instructions_file` against the effective cwd and prints
    that absolute path, whereas Go used the raw value and let `*os.PathError`
    repeat `open <path>:`. `exec.rustOSErrorMessage` now renders errno values the
    Rust way (Windows message via `golang.org/x/sys/windows.FormatMessage`,
    trailing CRLF trimmed, Unix `errno.Error()`), `ioErrorMessage` uses it, and
    `baseInstructionsForConfig` takes the cwd and reports
    `failed to read model instructions file <abs>: <reason> (os error N)` /
    `model instructions file is empty: <abs>`. A live sweep also showed that
    Rust's root/exec `--image` is `num_args = 1..` (variadic) while
    `exec resume`/`fork` declare a single value, that a missing `--image` value
    prints `a value is required for '--image <FILE>...' but none was supplied`,
    and that `exec` binds exactly one PROMPT positional: `exec hello world`,
    `exec -- hello world` and `exec - extra` are clap diagnostics with no `--`
    tip, yet flags may follow the prompt (`exec "prompt" -i a.png b.png`).
    `cli.parseSharedOptionVariadicImages` (root `exec` only),
    `requireVariadicValues`, `missingImageValueError` and
    `unexpectedPositionalError` mirror all of that. Evidence: byte-identical
    live output for `--output-schema missing.json`,
    `--output-last-message D:/nonexistent-dir/x.txt`,
    `-c model_instructions_file=missing.md`, `exec -i` / `exec --image=`,
    `exec hello world`, `exec -- hello world`, `exec - extra`, plus real-model
    runs of `exec "Reply with exactly: IMG-OK" -i tiny.png tiny.png` and
    `exec -i tiny.png -m deepseek-v4-flash "Reply with exactly: IMG2-OK"`.
    Regressions: `cli/exec_image_positional_like_rust_test.go`, the updated
    `TestIOErrorMessageDropsPathFromPathError`.

33. **`exec resume` / `fork` / `review` positional binding and clap usage rendering** -
    Rust's `ResumeArgsRaw` binds `SESSION_ID` then `PROMPT` and lets clap reject a
    third positional, and `From<ResumeArgsRaw>` reinterprets the single positional
    of `--last` as the prompt, so `exec resume --last a b` resumes the newest
    thread and sends `b` while keeping `a` in the (ignored) session slot. Go
    instead answered `exec resume --last accepts at most one prompt`, joined any
    extra positional into the prompt (`resume <id> a b`, `fork <id> a b`,
    `review a b`), and printed a plain resume usage line for every unexpected-flag
    error. Go now binds the same slots and returns
    `unexpected argument '<arg>' found` with the static resume/fork/review usage
    page, while `execResumeFlagUsageText` reproduces clap's rendered usage for
    flags (`<SESSION_ID> <PROMPT>` for supplied positionals,
    `[OPTIONS]` dropped, `--last`/`--all` echoed). Evidence: live diagnostics
    match 0.166 byte for byte for `resume <uuid> a b`, `resume --all <uuid> a b`,
    `resume id --bogus`, `resume --last a --bogus`, `resume --last a b --bogus`,
    `fork id a b` and `review a b`; a real qax `deepseek-v4-flash` run of
    `exec resume --last a b` exits 0 and shows `user` / `b`. Regressions:
    `cli/exec_subcommand_positionals_like_rust_test.go` (3 cases, 7 usage
    renderings).

34. **`apply_patch` parser and I/O error text** - Rust's `--codex-run-as-apply-patch`
    entry point prints `Invalid patch: <message>` for whole-patch problems and
    `Invalid patch hunk on line <n>: <message>` for hunk problems, while the
    app-server tool reports thiserror's Display form (`invalid patch: ...` /
    `invalid hunk at line <n>, ...`). Go's parser used its own vocabulary
    (`missing begin marker`, `unexpected line %q`, `add file hunk requires
    content`) and never reported line numbers. `Parse` is now a port of
    `streaming_parser.rs`: strict and lenient (`<<EOF`) boundary checks, the
    per-mode state machine, the duplicate/empty `Environment ID` errors, the
    "is not a valid hunk header" / "Update file hunk for path 'x' is empty" /
    "Update hunk does not contain any lines" / "Unexpected line found in update
    hunk" / "Expected update hunk to start with a @@ context marker" messages
    with Rust's 1-based lines, plus Rust's acceptance of an Add hunk with no
    `+` lines (creates an empty file; Go used to reject it). `FormatCLIError`
    renders the standalone wording, `FormatError` keeps the tool wording.
    `fs.ReadFile`/`fs.WriteFile` failures now print
    `Failed to read file to update <abs>: <localized> (os error N)` /
    `Failed to write file <abs>: <localized> (os error N)` through a local
    `rustIOMessage` (applypatch cannot import exec). Evidence: 19 live
    `--codex-run-as-apply-patch` probes against 0.166 match byte for byte
    (stdout, stderr, exit status, resulting files), including the empty-add-
    then-add chain and the heredoc wrapper; regressions:
    `applypatch/rust_error_text_like_rust_test.go` (12 parse cases, empty add,
    update read text, heredoc).

35. **Root and `doctor` clap diagnostics, single-PROMPT binding** - Rust's root
    command reports unknown options with clap's block
    (`error: unexpected argument '--bogus' found`, a `tip:` line, the two-line
    root usage and `For more information, try '--help'.`) and exits 2, while Go
    printed `Error: unknown root option --bogus` and exited 1. Go now renders
    the same block: `=value` is dropped from the reported name, the `--` tip is
    emitted, and flags that only the `exec` subcommand accepts suggest
    `'exec --json' exists` when `exec` follows. Missing values now use clap's
    `a value is required for '<long> <PLACEHOLDER>' but none was supplied`
    wording for every root/shared value option (model, config, enable, disable,
    remote, remote-auth-token-env, local-provider, profile, cd, add-dir,
    ask-for-approval with its possible values, sandbox, image and doctor's
    probe-filesystem-path). `codex doctor` gained the same diagnostics with
    `Usage: codex doctor [OPTIONS]` and no `--` tip (it declares no
    positionals). The root parser also now binds exactly one PROMPT like
    clap: options are still parsed after it, a second positional is
    `unexpected argument '<arg>' found` with the root usage, and words after
    `--` fill the PROMPT slot even when they name a subcommand. Evidence: 34
    live diagnostics match Rust 0.166 byte for byte (`--bogus`, `-Z`,
    `--bogus=1`, `--json [exec]`, every missing-value flag, `hello world`,
    `hello --bogus`, `hello -- world`, `-- exec`, `-- -x y`, `apply-patch
    --bogus`, `doctor --bogus|--bogus=1|foo|--probe-filesystem-path`, plus the
    four `exec`-only flags). Regressions:
    `cli/root_clap_diagnostics_like_rust_test.go` (17 cases); the
    `TestParseInteractivePrompt` and `TestInteractivePromptUsesExecRunner`
    expectations moved to the single-PROMPT contract.

36. **Subcommand clap diagnostics: missing required arguments, did-you-mean and the
    nested `setup` parser** - Rust renders clap blocks for every subcommand and
    exits 2, while Go answered `Error: mcp get requires NAME` and exited 1. Go now
    reproduces clap for the 40 subcommand surfaces:
    - `unexpected argument '<flag>' found` with the subcommand usage, the `--`
      tip only when the command declares a positional, and `=value` dropped from
      the reported name (`mcp add --bogus=1`).
    - `the following required arguments were not provided:` with one indented
      line per missing argument, reported before any semantic validation
      (`queue` lists `--thread <THREAD>` and `--message <TEXT>`, `mcp add` lists
      the `(url|command)` group before `<NAME>`). The usage line is clap's smart
      usage: `[OPTIONS]` disappears except for `mcp add`, whose requirement is an
      argument group.
    - `unrecognized subcommand '<name>'` plus clap's did-you-mean tip. Go ports
      clap's metric (`strsim::jaro`, `confidence > 0.7`, ascending order, plural
      `some similar subcommands exist:` wording), which is what makes
      `mcp logot` print `'login', 'logout'` and `mcp bogus` print `'logout'`
      despite the score landing on 0.7000000000000001.
    - `codex sandbox setup` runs through a nested parser in Rust, so clap's error
      travels out as an anyhow error: the message keeps clap's own `error: `
      prefix, names the `setup` program, ends with a trailing newline (the CLI's
      `Error: ` line turns it into a blank line) and exits 1. The usage block
      follows clap's smart rendering per case: `[OPTIONS]` for a bare unexpected
      argument, `--elevated`/`--codex-home <DIR>` echoed once supplied,
      `--codex-home <DIR>` first when `--user` requires it, and the conflict
      message naming the argument that was accepted first.
    - Missing values for subcommand flags now use the `<PLACEHOLDER>` from each
      command's `--help` page (`--url <URL>`, `--env <KEY=VALUE>`, `--attempt <N>`,
      `--limit <N>`, `--sock <SOCKET_PATH>`, `--rules <PATH>`, ...). The four
      spellings that differ per command (`--env`, `--thread`, `--profile`,
      `--remote`) are overridden at their call sites.
    `mcp add` also parses like clap now: `--url` before the name, `--` handing the
    next word to NAME, and a lone `-` kept as a value. Evidence: 158 live
    comparisons against 0.166 (80 unknown-flag, 56 missing/misplaced argument,
    17 missing-value, 5 follow-up cases) are byte-identical on stdout, stderr and
    exit status. Regressions:
    `cli/subcommand_clap_diagnostics_like_rust_test.go` (26 missing-required,
    10 unexpected-argument, 12 did-you-mean, 10 sandbox-setup and 20
    missing-value cases plus the Jaro metric), and the stale Go-wording
    expectations in `cli/cli_test.go`, `cli/exec_server_auth_test.go` and
    `cmd/codex-exec-server/main_test.go` moved to the clap text.

37. **Shared `--enable`/`--disable` inside every subcommand, plus clap's
    used-option usage rendering** - Rust flattens `CliConfigOverrides` and the
    feature toggles into each subcommand, so `codex login --enable foo`,
    `codex mcp list --disable f` and `codex features list --enable=f` parse
    exactly like their root-scope spellings; Go answered `unexpected argument
    '--enable' found` because the flags only existed on the root parser. Go now
    pre-scans every subcommand's argv for `--enable`/`--disable` (both
    `--flag value` and `--flag=value`, repeatable, stopping at `--`) and records
    the values in `RootOptions`, so the shared startup path validates them the
    same way for both spellings: an unknown name still exits 1 with
    `Error: Unknown feature flag: <name>` and an empty value still reaches the
    runtime instead of failing in the parser. `codex sandbox setup` is the one
    exception - Rust parses it with its own clap definition - so it keeps
    rejecting the flags with its nested-parser diagnostic. A pending value
    (`login --enable`, `login --enable --config a=b`, `mcp list --enable
    --json`) prints clap's `a value is required for '--enable <FEATURE>' but
    none was supplied` without a usage block (`--disable <FEATURE>` likewise).
    The same round tightened `requireValue` to clap's value rule: a
    leading-dash token is never taken as an option value, so `codex mcp get -c
    --json` now reports the pending `--config <key=value>` instead of eating
    `--json`.
    Because the flags now reach parsers that can fail after consuming options,
    Go also renders clap's smart usage for the table-driven diagnostics: once an
    option was consumed the `[OPTIONS]` placeholder is replaced by the options
    clap saw, in argv order and in front of the positionals
    (`Usage: codex mcp get --enable <FEATURE> --json <NAME>`,
    `Usage: codex delete --force --enable <FEATURE> <SESSION>`,
    `Usage: codex features list --enable <FEATURE>`,
    `Usage: codex queue --thread <THREAD> --message <TEXT> --enable <FEATURE>`).
    Options already listed by the static block are not repeated, `mcp add` keeps
    `[OPTIONS]` because its remaining requirement is an argument group, and the
    hand-written `exec` family usage lines are untouched. Evidence: 144 live
    comparisons against 0.166 (36 subcommand surfaces x `--enable zzz`,
    `--disable zzz`, `--enable`, `--enable=zzz`) plus 18 targeted cases; the only
    remaining differences are the three commands listed under Still open.
    Regressions: `cli/subcommand_shared_feature_flags_like_rust_test.go` (8
    accepted-spelling, 5 pending-value, the `sandbox setup` exception, the `--`
    rule and 11 smart-usage cases).

38. **Group help pages, `arg_required_else_help` and group-level `-c`** - Rust
    gives every group command a clap page and, for the eight groups that require
    a subcommand, declares `arg_required_else_help`; Go answered
    `Error: mcp requires a subcommand` (exit 1), `unknown mcp subcommand
    --help`, or `unexpected argument '--help' found`. Go now carries the pages
    captured verbatim from 0.166 in `cli/group_help.go` for `mcp`, `features`,
    `plugin`, `plugin marketplace`, `debug`, `debug app-server`, `execpolicy`
    and `app-server daemon`, both layers of clap's rendering (the `-h` summary
    and the `--help`/`help` long page), and routes them before the group parser:
    - `codex mcp` (bare) writes the short page to **stderr** and exits 2,
      because clap answers `arg_required_else_help` with the summary page, not
      with an error block.
    - `codex mcp -h` writes the short page and `codex mcp --help` /
      `codex mcp help` the long page to stdout with exit 0. The help action
      fires as soon as clap parses the flag, so `codex plugin --help --bogus`
      prints the page instead of reporting the trailing token.
    - a group invoked with options but without its subcommand now prints
      clap's `'codex mcp' requires a subcommand but one was not provided` with
      the `[subcommands: ...]` list and the group usage, instead of the old
      one-line Go error at exit 1.
    The same round let `-c`/`--config` through at group level, where Rust's
    `CliConfigOverrides` are flattened too: `codex mcp -c a=b list`,
    `codex plugin marketplace --config=a=b list`, `codex debug -c` (pending
    value, no usage block) now behave like Rust. The values land in
    `RootOptions`, so `mergeRootConfigOverrides` keeps the root-first order the
    subcommand parsers already produced for leaf-level `-c`. Evidence: 56 live
    comparisons against 0.166 (8 groups x {bare, `-h`, `--help`, `help`,
    `--enable zzz`, `--bogus`, `--help --bogus`}) and 32 group-level `-c` cases
    (`-c a=b`, `--config=a=b`, `-c`, repeated `-c`) are byte-identical on
    stdout, stderr and exit status. Regressions:
    `cli/group_help_like_rust_test.go` (routing for the 8 groups, page shape,
    4 missing-subcommand diagnostics, group-level overrides) and
    `app/group_help_meta_like_rust_test.go` (11 long/short/help cases plus the
    eight bare-invocation stderr/exit-2 cases).

39. **Every `-h`/`--help` page, root meta actions and `--help=<value>`** - Go
    answered `unexpected argument '--help' found` for every command outside the
    eight groups of entry 38. `cli/subcommand_help.go` now carries the pages
    captured verbatim from 0.166 for the root command and all 79 subcommand
    paths (80 keys, both the `-h` summary and the `--help`/`help` long layer,
    including `execpolicy check`, `stdio-to-uds`, `tcp-tunnel` and
    `responses-api-proxy`, which no parent page lists under `Commands`), plus a
    per-command flag table that records which flags take a value.
    `SubcommandHelpRequestFor` walks the argv the way clap does:
    - the path advances only over known subcommands, so `codex mcp list --json
      --help` reaches the `mcp list` page past a boolean flag while an unknown
      flag before `--help` hands the argv back to the parser;
    - `-h` selects the summary layout, `--help` the long one, and `help <topic>`
      the long page of a nested topic (`codex mcp help get`);
    - a value-taking flag swallows the next token, so `queue --thread --help`
      keeps clap's pending-value behaviour instead of printing help, while
      `mcp get -c foo --help` and `mcp get -cfoo --help` do print it; `--` and
      unknown flags hand the argv back to the command's parser;
    - `-h<cluster>` fires on the first character like clap, except for
      `execpolicy check`, whose `<COMMAND>...` positional allows hyphen values
      (the generator records that exception by probing 0.166);
    - `--help=<value>` renders clap's `unexpected value '<value>' for '--help'
      found; no more were expected` block with the usage 0.166 prints, captured
      per command because clap echoes the help flag among the options it saw.
    Root parsing gained clap's meta actions, which fire before any subcommand
    runs: `codex -h exec`, `codex -hx exec` and `codex --help -c foo` print the
    root page, `codex -V exec` prints the version, `codex help <topic>` (at any
    depth, also after root options) prints that page, and an unknown topic
    segment renders `unrecognized subcommand '<name>'` with the usage of the
    deepest command clap resolved (`codex help sandbox bogus`). The exec family
    keeps its own captured pages but gained the same `-h<cluster>` and
    `--help=<value>` handling, and a regression test pins `ExecHelpText` to the
    generic pages so `codex help exec` and `codex exec --help` stay
    byte-identical. Evidence: 434 live comparisons against 0.166 (80 paths x
    {`--help`, `-h`, `--help=x`, `-hx`} + 79 `help <path>` + 12 short-flag + 23
    root cases) are byte-identical on stdout, stderr and exit status apart from
    the build version string and `execpolicy check -hx`, which falls through to
    the missing-required block listed under Still open; the earlier 160-page
    probe and the 80 unknown-argument / 56 missing-argument regression probes
    still report 0 diffs. Regressions:
    `cli/subcommand_help_like_rust_test.go`,
    `cli/root_meta_help_like_rust_test.go` and
    `app/subcommand_help_meta_like_rust_test.go`.

40. **Clap blocks for `execpolicy check`, `stdio-to-uds` and `tcp-tunnel`** -
    these three parsers answered in Go wording at exit 1 (`Error: execpolicy
    check requires --rules PATH`, `Error: stdio-to-uds requires exactly one
    SOCKET_PATH`, `Error: the following required arguments were not provided:
    --proxy-url <PROXY_URL>`), so `codex execpolicy check -hx` could not reach
    clap's block either. Go now renders clap's diagnostics for all three:
    - `execpolicy check` prints the required list in declaration order
      (`--rules <PATH>` then `<COMMAND>...`) with a usage that echoes the
      booleans clap already consumed (`--pretty`,
      `--resolve-host-executables`), and keeps treating a hyphen-leading token
      as the start of `<COMMAND>...`, so `codex execpolicy check --dry-run
      --rules r` still reports only the missing `--rules <PATH>`.
    - `stdio-to-uds` prints the positional-only usage
      (`Usage: codex stdio-to-uds <SOCKET_PATH>`) when the path is missing, and
      clap's `unexpected argument` block with the `[OPTIONS]` usage (plus the
      `--` tip for an unknown flag) once an option-like token is involved; `--`
      still binds the path.
    - `tcp-tunnel` moved its argument diagnostics into `tcptunnel.ParseArgs`,
      which returns clap-shaped `Diagnostic`s the CLI renders as usage errors:
      pending values carry the placeholder (`a value is required for
      '--proxy-url <PROXY_URL>' ...`), unknown flags print the `[OPTIONS]`
      usage, missing options are listed in declaration order with the options
      clap consumed echoed in argv order, the `requires = "auth_token_stdin"`
      pair adds `--auth-token-stdin` to both the list and the usage,
      `--flag=value` on a boolean renders clap's `unexpected value` block
      (required-only usage while at most one value option was consumed, clap's
      required-group rendering otherwise), and the value parsers mirror Rust's
      `url::Url` (`relative URL without a base`, `empty host`, `invalid port
      number`, `invalid IPv6 address`) and `SocketAddr` (`invalid socket
      address syntax`).
    Evidence: 34 live comparisons against 0.166 (16 execpolicy/stdio/tcp-tunnel
    argument shapes plus 18 tcp-tunnel value and usage shapes) differ only in
    the `stdio-to-uds` connect attempt listed under Still open; the 434-case
    help probe now reports 0 diffs (was 1), and the 80 unknown-argument and 56
    missing-argument regressions stay at 0. Regressions:
    `cli/hidden_command_diagnostics_like_rust_test.go` and
    `app/hidden_command_diagnostics_meta_like_rust_test.go`.

41. **`mcp add` / `mcp login` OAuth client registration and clap `requires`
    blocks** - Rust's streamable-HTTP options declare
    `requires`/`requires_all` relations: `--oauth-client-id`,
    `--oauth-resource`, `--oauth-client-registration` and
    `--bearer-token-env-var` all require `--url`, next to
    `--oauth-client-secret requires_all = ["url", "oauth_client_id"]`, and both
    `AddMcpStreamableHttpArgs` and `LoginArgs` accept
    `--oauth-client-registration <AUTO|CIMD|DCR>` through a case-sensitive
    `value_enum`.
    - `MCPOptions` now carries `OAuthClientRegistration`, parsed for `mcp add`
      and `mcp login` in both `--flag value` and `--flag=value` spellings; empty
      and out-of-enum values print clap's
      `a value is required for '--oauth-client-registration <AUTO|CIMD|DCR>' but
      none was supplied` / `invalid value ...` block plus
      `[possible values: auto, cimd, dcr]`, exit 2, and no usage block.
    - The four Go-worded `requires` errors are replaced by clap's blocks: a
      relation that leaves `--url` as the only missing argument is reported
      against the root command with the bare argument id
      (`error: the following required argument was not provided: url`, root
      usage), while `requires_all` violations keep the `mcp add` usage and list
      `--url <URL>` / `--oauth-client-id <CLIENT_ID>` in clap's plural wording.
      Presence is read from argv before the `--` terminator, so
      `--oauth-client-id ""` still trips the requirement like clap's
      `Option<String>`.
    - `--oauth-client-secret ""` keeps Rust's run-time
      `--oauth-client-secret must not be empty` (exit 1), and the parsed
      strategy is threaded into both CLI login paths
      (`performMCPCLIOAuthLogin*` -> `StartOAuthLoginServer.ClientRegistration`)
      and the `mcp` service login fallback, defaulting to `auto` when absent.
    Evidence: 23 live comparisons against 0.166
    (`out/round26-mcp-oauth.txt`) are byte-identical on stdout, stderr and exit
    status; `go test ./cli` passes with
    `cli/mcp_oauth_registration_like_rust_test.go`, and
    `go test -vet=off ./app -run 'MCP|OAuth'` passes with
    `app/mcp_oauth_registration_meta_like_rust_test.go`.

42. **Nested `sandbox setup` presence semantics and usage echo** - Rust's
    nested clap parser keys the required `<--user <USER>|--current-user>` group,
    the `--user`/`--current-user` conflict and `--user requires = "codex_home"`
    off *presence*, so `--user=` and `--user ""` satisfy the group and only fail
    later on the codex-home requirement or the elevation gate; an empty
    `--codex-home` value is rejected by clap's `PathBuf` parser.
    - `SandboxOptions` now records `--user` presence plus the consumed setup
      options in argv order. `validateSandboxSetupParseDependencies` uses that
      presence, and `--codex-home` with an empty value (either spelling) reports
      `a value is required for '--codex-home <DIR>' but none was supplied`.
    - The nested usage blocks echo consumed options in argv order
      (`setup --elevated --codex-home <DIR> <--user <USER>|--current-user>`),
      list a required `--codex-home` first only when it was never parsed, drop
      `[OPTIONS]` once any option was consumed, and keep `--user` first in the
      conflict when it preceded `--current-user`, even with an empty value.
    - `sandbox.SetupCommand.UserSet` carries the presence into the run layer, so
      `--user "" --codex-home <dir>` reaches the elevation gate like Rust
      instead of failing the Go-only "user required" validation.
    Evidence: 26 live comparisons against 0.166
    (`out/round27-sandbox-setup.txt`) are byte-identical on stderr and exit
    status; `go test ./cli ./sandbox` passes with the extended
    `TestSandboxSetupClapDiagnosticsLikeRust`,
    `TestSandboxSetupUserPresenceLikeRust` and `TestValidateUserPresence`.

43. **`mcp add` transport group selection** - Rust's `AddMcpTransportArgs`
    flattens an optional stdio struct (`command`, `env`) and an optional
    streamable-HTTP struct, so clap's `transport` group makes `--url` and a
    `COMMAND` conflict, while `--env` alone selects the stdio transport even
    when `--url` is present and then leaves the command empty.
    - `mcp add --url <URL> -- <COMMAND>...` now prints clap's conflict block
      (`the argument '--url <URL>' cannot be used with '[COMMAND]...'`) with the
      `mcp add` usage and exit 2 instead of the Go-only
      `mcp add accepts either --url or COMMAND, not both`.
    - `mcp add --url <URL> --env K=V` now reports Rust's run-time
      `command is required` (exit 1) instead of
      `mcp add --env is only valid with stdio COMMAND`.
    - `--env K=V` with no transport keeps clap's required-group block
      (`<COMMAND|--url <URL>>`), and the `requires = "url"` relations still fire
      first (`--bearer-token-env-var` + `--env` + command).
    Evidence: 11 live comparisons against 0.166
    (`out/round28-mcp-add-groups.txt`) differ only in the config.toml shape
    recorded under Still open; `go test ./cli` passes with
    `TestMCPAddTransportGroupLikeRust`, and
    `go test -vet=off ./app -run 'MCP|OAuth'` passes with
    `TestMCPAddTransportGroupMetaLikeRust` and
    `TestMCPAddEnvWithoutCommandLikeRust`.

44. **Session subcommand clap surface and variadic `--image`** - Rust's
    `resume`/`fork`/`archive`/`unarchive`/`delete` all flatten
    `SharedCliOptions`, whose `--image` is `num_args = 1..`; Go's session
    parsers still read a single value, so `codex resume -i a.png b.png` bound
    `b.png` as SESSION_ID (`Session not found: b.png`) and
    `codex archive nosuch -i a.png b.png` printed the Go-only
    `archive accepts exactly one SESSION`.
    - `--image`/`-i` now swallows every following non-flag token (comma
      splitting kept). An inline value (`--image=a.png`, `-i=a.png`) stops the
      run, matching clap, so `archive --image=a.png b.png X` reports
      `unexpected argument 'X' found`; `--` also terminates the run and the
      tokens behind it fill the positional slots (`archive -i a.png b.png --
      c` archives `c`).
    - Diagnostics are now clap's: unknown flags render the smart usage block
      (consumed options in argv order plus bound slots, `[OPTIONS]` dropped once
      anything was consumed), a third positional keeps the plain table block
      (`unexpected argument 'c' found`), and `--last` with SESSION_ID + PROMPT
      reports clap's `the argument '--last' cannot be used with '[PROMPT]'`.
    - Removed spawn-history flags get clap's suggestion: `fork --last-n 2`
      prints `tip: a similar argument exists: '--last'` and echoes `--last` in
      the usage block.
    - `codex queue` now mirrors `queue_cmd.rs`: clap validates `--thread` and
      `--message` before `restricted_options` runs, so `queue -i a.png` reports
      the missing-required block while `queue --thread t --message m -i a.png`
      reports the raw clap error (exit 2, no usage block and no `--help` hint).
      Hidden inherited options stay out of the smart usage, and the options
      QueueCommand never declares (`-a`, `--search`, `--no-alt-screen`,
      `--no-daemon`, `--no-worktree`, `--model-reasoning-effort`) are clap
      unexpected arguments.
    Evidence: live comparisons against 0.166 - `out/round29-session-diag.txt`
    (14/14), `out/round29-mutation.txt` (10/10), `out/round29-queue.txt`
    (30/30), plus 12 queue-usage and 12 session-usage cases at 0 diffs. The
    12-case `resume`/`fork` image run (`out/round29-resume-img.txt`) now differs
    only in the non-TTY TUI gate recorded under Still open. `go test ./cli`
    passes with `TestSessionVariadicImagesLikeRust`,
    `TestSessionVariadicImagesConsumeSessionLikeRust`,
    `TestSessionUnexpectedArgumentDiagnosticsLikeRust`,
    `TestSessionLastPromptConflictLikeRust`,
    `TestSessionRemovedFlagDiagnosticsLikeRust` and
    `TestQueueClapDiagnosticsLikeRust`.

45. **`--worktree` post-parse gate, `--strict-config` timing and exec-global
    options** - Rust's `reject_unsupported_worktree_for_subcommand`
    (`codex-rs/cli/src/main.rs`) runs after clap parsed the subcommand, and only
    a new interactive session, `codex fork <ID>`, `codex exec` and
    `codex exec fork` may create a managed worktree.
    - Go rejected `--worktree` while parsing, so the session and management
      commands produced clap-shaped or Go-only errors. A new
      `Parsed.validateWorktreeSupport` now runs first in `Parsed.Validate()` and
      prints Rust's four messages verbatim: the generic
      `` `--worktree` supports new interactive sessions, `codex fork`,
      `codex exec`, and `codex exec fork` ``, `` `codex fork --worktree`
      requires an explicit session ID ``, `` `--worktree` cannot resume an
      existing session; use `codex exec fork --worktree` `` and
      `` `--worktree` is not supported for code review ``.
    - The gate reads the flag from the root or from the subcommand's flattened
      shared options, so `resume --worktree abc` and
      `queue --worktree --thread t --message m` - both clap-valid in Rust -
      reach the same rejection.
    - `--strict-config` no longer short-circuits before `parseSubcommand`, so
      clap's own diagnostics win over the post-parse gate;
      `unsupportedStrictConfigSubcommandName` allows `Agents`, and the new
      `remoteControlSubcommandName` renders `remote-control [start|stop|pair]`
      for both the strict-config and `--remote` gates.
    - `codex agents --strict-config` now prints clap's
      `unexpected argument '--strict-config' found` with
      `Usage: codex agents [OPTIONS]`, matching Rust's `AgentsCommand`, which
      does not flatten the flag; the root spelling `--strict-config agents`
      stays valid.
    - `codex exec review` accepts the exec-global options Rust marks in
      `mark_exec_global_args` (`--model`/`-m`,
      `--dangerously-bypass-approvals-and-sandbox`,
      `--dangerously-bypass-hook-trust`, `--worktree`) and still rejects the
      other shared options as clap unexpected arguments, so
      `exec review --worktree` reaches the review rejection instead of failing
      at the clap layer.
    Evidence: live comparisons against 0.166 - `out/round30-worktree.txt`
    (15/15), `out/round30-worktree-order.txt` (5/5),
    `out/round30-exec-review.txt` (4/4), `out/round30-strict-config.txt`
    (12/12), `out/round30-remote-control.txt` (9/9) and
    `out/round30-exec-review-globals.txt` (4/6, the diffs are clap's
    did-you-mean). The clean-worktree rerun on the commit reproduced every
    count. Round 29 regressions stay green: 14/14, 30/30 and 10/10. `go test ./cli` passes with `TestWorktreeSupportedCommandsLikeRust`,
    `TestWorktreeRejectedCommandsLikeRust`,
    `TestWorktreeRunsBeforeSubcommandGatesLikeRust`,
    `TestStrictConfigGateLikeRust`,
    `TestAgentsRejectsStrictConfigAfterSubcommandLikeRust`,
    `TestExecReviewAcceptsExecGlobalOptionsLikeRust` and
    `TestMcpListRejectsWorktreeLikeRust`.

46. **Long-flag did-you-mean and clap's smart usage** - clap scores an unknown
    long flag against the command's declared long options with strsim's Jaro
    metric (`clap_builder/src/parser/features/suggestions.rs`), keeps every
    candidate above 0.7, names the highest scoring one in the tip and marks it
    present so the smart usage block echoes it. Go only modelled a handful of
    hand-picked spellings, so every misspelled flag printed the plain
    `--` workaround tip.
    - `clapCommandLongFlags` records the long options each command declares, in
      declaration order, for the root command, the `exec` family, the session
      commands and `codex review`; `clapSuggestLongFlag` ports the Jaro scoring
      and clap's tie-break (equal scores keep the candidate declared last, which
      is what `did_you_mean(...).pop()` returns).
    - Hidden aliases stay in the tables: `--experimental-json`, `--yolo` and
      `--not-so-yolo` are scored and named in the tip, while the usage block
      echoes the primary long (`--experimental-jso` suggests
      `--experimental-json` but prints `--json`).
    - `clapSmartUsage` renders clap's usage block for every command with
      positionals: the options accepted before the unknown flag in argv order,
      then the slots with bound ones in angle brackets.
      `codex exec resume -m x --bogus` now prints
      `Usage: codex exec resume --model <MODEL> [SESSION_ID] [PROMPT]` instead of
      the `[OPTIONS]` page, and `exec fork --image a.png --bogus` keeps clap's
      single-value `<FILE>` placeholder.
    - The root command keeps its static page and falls back to clap's second
      pass: when no root flag is similar, the flags of the subcommand named later
      in argv are scored, so `codex --skp-git-repo-check exec` reports
      `tip: 'exec --skip-git-repo-check' exists`.
    Evidence: live comparisons against 0.166 - `out/round31-flag-suggestions.txt`
    (20/20), `out/round31-smart-usage.txt` (13/13) and
    `out/round31-flag-boundaries.txt` (26/26), plus the round 29/30 regressions
    (14/14, 30/30, 10/10, 15/15). `go test ./cli` passes with
    `TestLongFlagDidYouMeanLikeRust`,
    `TestLongFlagSmartUsageEchoesConsumedOptionsLikeRust`,
    `TestRootUnknownFlagSubcommandFallbackLikeRust` and
    `TestUnknownLongFlagWithoutSuggestionLikeRust`, and the round 44 session
    suggestion tests keep passing through the new table lookup.

47. **Long-flag tables for the rest of the command tree, and clap's usage
    rules** - entry 46 covered the root, the `exec` family, the session commands
    and `codex review`. The other groups still answered an unknown flag with a
    Go-only `unknown <command> option <flag>`, a bare `unexpected argument ...
    found` without a usage block, or the static page where clap echoes the
    options it already accepted.
    - `clapCommandLongFlags` now records 71 command paths. The 64 added here are
      agents, app, app-server (+ daemon start/stop/restart/version/bootstrap/
      update/enable-remote-control/disable-remote-control and the generate-*
      leaves), apply, cloud (+ exec/status/list/apply/diff), completion, debug
      (+ app-server send-message-v2, models, prompt-input), doctor, exec-server
      (+ forward), features (+ list/enable/disable), login (+ status), logout,
      mcp (+ add/get/list/login/logout/remove), migrate-nfs-db,
      migrate-rollouts, plugin (+ add/list/remove and every marketplace
      subcommand), queue, remote-control (+ start/stop/pair), sandbox (+
      uninstall) and update. The audit script walks the 0.166 `--help` tree (86
      paths) and finds an option-bearing path without a table only where the
      session commands share `clapSessionMutationLongFlags`, plus Rust's hidden
      aliases, which the tables keep on purpose because clap still scores them.
    - `clapStringSmartUsage` follows `clap_builder/src/output/usage.rs`:
      `[OPTIONS]` survives only while nothing is marked present, the required
      options come first, the accepted ones follow in argv order and the
      positionals close the block (`Options::write_args` unrolls the
      requirements before the `used` ids). The optional `[COMMAND]` placeholder
      stays on the static page.
    - Group parsers report slot-aware clap diagnostics instead of Go strings:
      `plugin add|remove` and every `plugin marketplace` leaf (`unexpected
      argument 'src'`, a missing `<PLUGIN[@MARKETPLACE]>`), `features
      enable|disable` (an unknown flag beats the extra positional, a bound slot
      renders `<FEATURE>`), `app-server generate-ts|generate-json-schema|
      generate-internal-json-schema` (`--outs` suggests `--out <DIR>`, a missing
      `--out` prints clap's required-argument block), the bare `app-server
      daemon <sub>` commands (which now echo `--config` in the smart usage),
      `debug app-server send-message-v2`, `exec-server forward` (its own page
      with the three required options), `login status`, `sandbox uninstall` and
      `doctor`.
    - clap's `requires` relation on `app-server daemon update --yes` prints the
      missing `--from-cli` block instead of Go's own sentence, and
      `plugin marketplace upgrade <a> <b>` reports the second positional like
      clap.
    - Commands whose positional captures trailing values (`mcp add`, `sandbox`)
      stack the did-you-mean tip and the `--` workaround tip, and a suggested
      flag whose placeholder carries an `=` (`--config <key=value>`) is no
      longer truncated by the inline-value split.
    Evidence: live comparisons against 0.166 - 127 new cases
    (`out/round32-group-suggestions.txt` 26/26,
    `out/round32-group-boundaries.txt` 26/26, `out/round32-group-edges.txt`
    20/20, `out/round32-group-named-cases.txt` 12/12, the daemon / marketplace /
    uninstall probes 14/14, 13/13 and 3/3) plus 118 round 29-31 regression cases
    (`out/round32-all-probes.txt`, 0 diffs). `go test ./cli ./sandbox
    ./applypatch ./prompt ./review ./cmd/... ./tcptunnel` passes with
    `TestSubcommandGroupFlagDiagnosticsLikeRust` (120 Rust-captured cases);
    `go test -vet=off ./app` still fails only `TestExecJSONEndToEnd`.

48. **Short-flag diagnostics, the pending-value order and `-c` on every leaf** -
   entry 47 taught the long flags, but a short alias was still resolved as a
   value flag from one shared table (so `plugin add -m x` echoed
   `--model <MODEL>` instead of `--marketplace <MARKETPLACE>`), an unknown
   short alias reached the pending-value diagnostic instead of clap's
   `unexpected argument '-x' found`, and the global `-c`/`--config` was only
   wired into some parsers.
   - `clapCommandShortFlags` records the 21 command paths whose short aliases
     differ from the inherited `-c`/`-h` pair (taken from the 0.166 `--help`
     tree), and `clapCommandShortFlagLongs` keeps the long spelling clap echoes
     for the aliases that differ per command: `-m` is `--marketplace` under
     `plugin`, `-o` is `--output-last-message` for the `exec` family,
     `-y` is the boolean `--yes` on `app-server daemon update`, `-o`/`-p` are
     `--out`/`--prettier` on the `app-server generate-*` leaves and `sandbox -p`
     keeps its own `<CONFIG_PROFILE>` placeholder.
   - An unknown short alias is named by its first letter (`codex -X1` reports
     `-X`) and always renders the command's *static* usage page, because clap
     resolves short flags before it records the accepted options:
     `mcp get --json -X` keeps `[OPTIONS] <NAME>` while the long-flag branch
     would have echoed `--json`.
   - `UsageError` carries `PendingFlag`/`PendingNext`, so a value-less option is
     diagnosed in clap's order: `-c -x1` reports `unexpected argument '-x' found`
     (static page), `-c --bogus` reports the long flag through the did-you-mean
     table, and a *declared* flag (`-c --json`, `-c -c x`, `-c -h`) keeps
     `a value is required for '--config <key=value>'`. `requireValue`,
     `consumeConfigOverrides` and `splitSubcommandFeatureFlags` all raise the
     pending form; `decorateRootPendingValueError` covers the root command.
   - `clapEffectiveCommand` resolves the nested command path from argv before the
     flag tables are consulted, so `app-server daemon update -y` and
     `plugin marketplace add` style paths pick their own spellings.
   - clap's global `-c`/`--config` now reaches the leaves that rejected it:
     `doctor`, `update`, `completion`, `features list|enable|disable`,
     `debug models|prompt-input|trace-reduce|clear-memories|app-server`, and the
     smart usage echoes `--config <key=value>` in argv order
     (`codex doctor -c a=b --bogus` prints `Usage: codex doctor --config
     <key=value>`).
   - `--` ends option parsing for `mcp get`, so `mcp get -- --json` names the
     server `--json`, `mcp get --` reports the missing `<NAME>`, and an
     overflowing positional (`mcp get -- a b`, `mcp get a b`) reports
     `unexpected argument 'b' found` against the static page instead of the
     Go-only `mcp get accepts exactly one NAME`.
   Evidence: live comparisons against 0.166 - `out/round33-short-flags.txt`
   (20/20), `out/round33-usage-spelling.txt` (8/8), the pending-value probes
   6/6 and 9/9, the `--`/`-c` probes 5/5, 8/8 and 11/12 (the remaining case is
   `sandbox -c a=b echo hi`, where Go's sandbox launcher hangs instead of
   reporting the Windows `CreateProcessAsUserW` failure) plus 307 round 29-32
   regression cases at 0 diffs (`out/round33-regress.txt`). `go test ./cli
   ./sandbox ./applypatch ./prompt ./review ./cmd/... ./tcptunnel` passes, with
   `TestShortFlagDiagnosticsLikeRust`, `TestPendingValueResolutionLikeRust`,
   `TestShortFlagUsageSpellingLikeRust` and
   `TestSubcommandConfigOverrideAcceptanceLikeRust` pinning the Rust-captured
   cases.

49. **The global option surface, deferred value checks and clap's value hints** -
   entries 47 and 48 taught the flag tables and the pending-value order, but the
   global `-c`/`--config` only reached part of the tree, the `exec` family
   accepted shared options clap never marks global, and a bad value aborted the
   scan before an undeclared flag later in argv could win.
   - `clapConfigOverrideArg` centralises clap's global override spelling
     (`-c`, `--config`, `-cvalue`, `--config=value`) and every leaf parser now
     calls it, so `-c` is accepted on all 18 paths the 0.166 help tree exposes
     (root, the `exec` family, the session commands, `doctor`, `update`,
     `completion`, `features`, `debug`, `app-server` (+ daemon / generate /
     proxy), `cloud`, `exec-server` (+ forward), `mcp`, `plugin`, `sandbox`,
     `migrate-*`, `remote-control` and `app`). `app-server daemon update
     -c a=b -y` keeps clap's argv order in the required-argument block.
   - `exec review` accepts exactly the options `codex exec` marks global
     (`--model`, the two dangerous bypasses, `--worktree`, plus exec's own
     `global = true` set), so `exec review --json --strict-config
     --thread-source --cyber-access-program --skip-git-repo-check --ephemeral
     --ignore-user-config --ignore-rules --output-schema
     --output-last-message` parse like Rust, while `--sandbox`, `--color`,
     `--profile`, `--cd` and `--oss` stay undeclared after `exec resume` /
     `exec fork` (`unexpected argument '--color' found`, with clap's `--yolo`
     did-you-mean tip).
   - `deferClapValueError` records a clap value-parser failure and keeps
     scanning, so an undeclared flag later in argv still wins
     (`exec --sandbox x --bogus` reports `--bogus`); the recorded error is
     reported once the scan ends (`cloud list --limit x`). Raw value-parser
     diagnostics take part too, which fixed `app-server --code-mode-host`: Go
     used to drop the URL rejection and start the server.
   - clap's empty-value rule: `--flag=` (one token) reports on the spot, while
     `--flag ""` defers, and an enum option reports a *missing* value
     (`a value is required for '--color <COLOR>' but none was supplied` plus the
     possible values) instead of an invalid one.
   - Missing-value placeholders come from the flag itself instead of Go's
     generic `requires a value`: `--thread <THREAD_ID>`,
     `--max-mib-per-second <MIB>`, `--linux-sandbox-pid-namespace <MODE>`, and
     `optionalFlagValue` never binds a leading-dash token as a value
     (`migrate-rollouts --thread --apply` reports the pending value).
   - Numeric value parsers distinguish the two clap messages: `--limit`,
     `--attempts` and `--attempt` report `must be an integer between 1 and 20`
     for a non-number and `must be between 1 and 20` for a range violation.
   - `similarValueTip` scores possible values with Jaro-Winkler (> 0.8), the
     metric behind clap's `did_you_mean`: `--sandbox dangr` suggests
     `danger-full-access`, `readonly` suggests `read-only` and `on_request`
     suggests `on-request`, while `onc`, `bogs` and `ON-REQUEST` stay silent.
   - The flag clap suggests also joins the smart usage block
     (`doctor --summary --jso` prints `Usage: codex doctor --summary --json`),
     and a boolean long flag no longer eats the following token while that
     block is built (`exec review --uncommitted -m x --bogus` echoes
     `--uncommitted --model <MODEL>`).
   - `reorderUsageOptionRun` rebuilds the option run clap already listed in argv
     order, so the exec family echoes the collected global `--enable`/`--disable`
     where the user typed them: `exec resume --enable f --last --bogus` prints
     `--enable <FEATURE> --last [SESSION_ID] [PROMPT]` and the reverse order
     keeps `--last --enable <FEATURE>`. A block whose required options argv never
     repeated (`exec-server forward`) still appends at the end.
   Evidence: the whole 0.166 option surface - 68 command paths x every declared
   flag with a bad value plus a trailing `--bogus` (`out/r34-flag-surface.txt`,
   497/497 byte-identical) - and the 78 cases this round fixed
   (`out/round34-fixed.txt`, 78/78), plus 400+ round 29-33 regression cases at
   0 diffs and 16 live `--enable`/`--disable` ordering cases
   (`out/round35-enable-order.txt`, 16/16). `go test ./cli ./sandbox
   ./applypatch ./prompt ./review ./cmd/... ./tcptunnel` passes, with
   `TestGlobalOptionSurfaceLikeRust` (20 Rust-captured diagnostics) and
   `TestExecReviewAcceptsGlobalOptionsLikeRust` pinning the new surface;
   `go test -vet=off ./app` keeps only the known baselines.

50. **Inline `--flag=` values, per-command placeholders and `exec-server`'s
   required groups** - the 0.166 surface sweep only probed the space form
   (`--flag <value>`), so this round gave every value flag three inline shapes:
   `--flag=bogusval --bogus`, `--flag=` and `--flag --strict-config`. 185 of
   the 1,068 probes diverged.
   - clap validates an inline value while it matches that token, so
     `--flag=bogusval` reports the value-parser error even when an undeclared
     flag follows, while the space form still defers. `deferClapValueError`
     returns inline diagnostics immediately:
     `app-server --ws-auth=bogusval --bogus` reports `invalid value 'bogusval'
     for '--ws-auth <MODE>'` with the possible values, and `exec
     --thread-source=bogusval --bogus` now parses the inline form at all.
   - clap rejects an empty inline value wherever its parser wants a non-empty
     one: `--cd=`, `--add-dir=`, `--image=`, `--output-schema=`,
     `--output-last-message=`, `--message=`, `--staging-dir=`, `--out=`,
     `--prettier=`, `--cloud-skills-snapshot=`, `--ws-token-file=`,
     `--ws-shared-secret-file=`, `--ws-auth=` and `--cyber-access-program=`
     print `a value is required for '<flag> <PLACEHOLDER>' but none was
     supplied` (plus possible values where clap lists them) instead of a silent
     empty string or a Go-only message.
   - the empty arms reuse Rust's parser wording: `--profile=` reports
     `invalid --profile value` with the empty name and the
     `pass a plain name such as work` hint, `--thread=` (migrate-rollouts)
     reports `invalid length: expected length 32 for simple format, found 0`,
     `--startup-client-info=` reports `EOF while
     parsing a value at line 1 column 0`, and the integer parsers separate
     `cannot parse integer from empty string` from `invalid digit found in
     string`.
   - placeholders are per command again: `exec fork`/`exec resume` echo
     `--image <FILE>` (not `<FILE>...`), `exec-server forward` echoes
     `--remote <URL>` (not `<ADDR>`), `sandbox --permission-profile` reports
     `<NAME>` and `sandbox --sandbox-state-readable-root` reports
     `<SANDBOX_STATE_READABLE_ROOT>`.
   - possible-values flags validate inside the scan: `--ws-auth`,
     `--remote-transport`, `--listen` and `--cyber-access-program` report
     clap's `invalid value '<v>' for '<flag> <PH>'` with their enum values
     instead of Go's wording (`unknown --ws-auth mode`, `unsupported --listen
     URL`), and `app-server --listen` keeps the empty-value arm.
   - `exec-server` renders clap's required groups: `forward` requires
     `--remote`, `--environment-id` and `--connect`, any `--aws-*` option pulls
     in `--aws-sigv4`, and both the missing list and the usage line follow the
     options argv actually supplied (`forward --aws-profile=` lists all four;
     `exec-server --aws-profile=` lists the remote pair plus `--aws-sigv4`).
     Go used to start the server and hang instead of failing.
   - `--ws-*` on the `forward` subcommand is an unexpected argument like Rust
     (the subcommand declares no listener auth), and `queue --thread=` counts as
     a supplied (empty) value, so the required list keeps only `--message`.
   - `printTable` stops at the headers a table declares: the two-column
     `plugin marketplace list` used to index past `widths` and panic on a
     non-empty marketplace list (`--config=`).
   Evidence: `out/round36-inline.txt` (1,068 probes, 1,008 byte-identical, 60
   left: 14 TERM/Desktop TUI environment, 26 daemon-version or signed-out
   runtime noise), the earlier 497-case bad-value sweep
   (`out/round36-r34-flag-surface.txt`, 497/497), the 78 round-34 fixes
   (`out/round36-r34-fixed.log`, 78/78), the 16 ordering cases
   (`out/round36-r35-order.log`, 16/16) and 31 round 29-34 probes
   (`out/round36-regress.txt`, `out/round36-regress2.txt`) at 0 diffs.
   `go test ./cli ./sandbox ./applypatch ./prompt ./review ./cmd/...
   ./tcptunnel` passes; `TestParseExecServerForwardModeLikeRust`,
   `TestParseExecServerWebSocketAuthFlagsLikeRust`,
   `TestParseAppServerListenValidation` and `TestParseQueueCommandRejectsInvalidLikeRust`
   were re-captured from 0.166.

51. **Empty `-c`/`--config=` overrides: one parser error, per-command
   contexts** - the inline sweep's `--config=` column had 20 divergences (plus
   the two `cloud list` flag probes that hung on the missing sign-in gate):
   most commands dropped the empty override and kept running (`features list`,
   `plugin list`, `debug models`, `migrate-rollouts`, `sandbox`, `app-server`),
   the exec family printed Go's own `Error: invalid override (missing '='):`,
   and `login`/`logout` reported `Error loading configuration: ...`.
   - `config.ParseOverrides` returns a `config.OverrideError` carrying Rust's
     `parse_overrides` wording: `Invalid override (missing '='): <raw value>`
     and `Empty key in override: <raw value>`. Both echo the value untrimmed,
     so `-c "  =x"` keeps its spaces. `config.ValidateOverrides` exposes the
     check without applying the values.
   - `shortAttachedValue` (`cli/usage_clap.go`) drops the single `=` clap
     accepts between a short flag and its attached value, so `-c=key=value`
     parses as `key=value` and `-c=` as an empty value (Rust lands on
     `Invalid override (missing '='): `), while `-c==x` still reports
     `Empty key in override: =x`.
   - `app/override_diagnostics.go` maps each command to the scopes Rust parses
     (root scope first, then the command's own `-c`) and to the context Rust
     prints:
     - `exec`, `exec resume`, `exec review`, `review`, `login`, `login status`
       and `logout` print `Error parsing -c overrides: <inner>` as a
       pre-rendered message, without the runtime `Error: ` head.
     - `app-server` prints `Error: error parsing -c overrides: <inner>`, and
       its own `-c` values now reach `loadAppServerConfig`, which used to read
       only the root scope.
     - `app-server daemon restart`/`update` print `Error: <inner>`;
       `start`/`stop`/`version` never load configuration and stay untouched.
     - `features list`, `plugin list`, `plugin marketplace list`/`upgrade`,
       `debug models`, `debug prompt-input`, `migrate-rollouts` and `sandbox`
       print `Error: <inner>` before their own required-argument checks, which
       is the order Rust reports (`sandbox --config=` no longer answers
       `sandbox requires COMMAND`). `sandbox uninstall` keeps running before
       configuration like Rust.
     - `features list` and `debug prompt-input` also apply the command-scope
       overrides they used to drop.
   - `codex cloud` resolves the credential before configuration, like Rust: a
     signed-out host prints `Not signed in. Please run 'codex login' to sign in
     with ChatGPT, then re-run 'codex cloud'.` (Go used to hang on a network
     call with no credentials), so `cloud --config=`, `cloud list --config=`
     and the `--cursor=`/`--env=` probes match instead of reporting an
     override or timing out.
   Evidence: `out/round37-inline.txt` (1,068 probes, 1,030 byte-identical, 38
   left - all TERM/Desktop TUI, daemon-version, socket or install-detection
   chatter, with no `--config=` case left beyond those), the 497-case
   bad-value sweep (`out/round37-r34-flag-surface.txt`, 497/497), the 78
   round-34 fixes (`out/round37-r34-fixed.log`, 78/78), the 16 ordering cases
   (`out/round37-r35-order.log`, 16/16) and 31 round 29-34 probes
   (`out/round37-regress.txt`, `out/round37-regress2.txt`) at 0 diffs.
   `TestConfigOverrideDiagnosticsMatchRust`,
   `TestCloudSignInHintPrecedesOverrideDiagnostics` and
   `TestShortConfigOverrideDropsAttachedEquals` pin 22 captured cases.

52. **`migrate-rollouts`: the uuid `--thread` grammar, the progress line and the
    human report** - the inline sweep still left `migrate-rollouts
    --thread=bogusval --bogus` reporting `invalid uuid`, and every human report
    was missing the stderr progress line, the elapsed suffix, the disk line and
    the exception block.
    - `cli/uuid_value.go` ports uuid 1.20's `try_parse` plus
      `InvalidUuid::into_err`, so `--thread` accepts exactly Rust's shapes
      (32-digit simple, 36-digit hyphenated, 38-character `{...}` and
      45-character `urn:uuid:` + hyphenated) and reports the crate's wording:
      `invalid character: expected an optional prefix of `urn:uuid:` followed by
      [0-9a-fA-F-], found `<c>` at <index>`, `invalid length: expected length 32
      for simple format, found <n>`, `invalid group count: expected 5, found <n>`
      and `invalid group length in group <g>: expected <n>, found <n>`. The crate
      measures the last group against the whole input, so
      `{12345678-1234-1234-1234-1234567890}` reports `expected 12, found 12`.
      Value errors keep clap's inline/space split: `--thread=b` reports at once,
      `--thread b --bogus` still reports the unexpected argument.
    - `rollout.MigrateRollouts` takes a `Progress` callback fed once per
      discovered path (nil status when the thread selection prunes it), and
      discovery now follows Rust's per-root order: names starting with
      `rollout-`, a `.jsonl.zst` shadow skipped when its plain file exists, and
      each root sorted descending with the active tree before the archived one.
    - `app/migrate_rollouts_progress.go` renders Rust's `MigrationProgress`:
      `Scanning local rollouts...` on stderr, the 250 ms TTY line
      (`\r\x1b[2K<line>`) or the non-TTY line every 1,000 paths and at the end,
      `Checking`/`Migrating rollouts  <n>/<total> (<p>%)  •  ...  •  <elapsed>`
      with Rust's elapsed format, and the clearing write on finish. `--json`
      stays quiet.
    - `printMigrateRolloutsHumanReport` prints `<completion> in <elapsed>.`, the
      `Disk used for thread storage: <before> -> <after>` line for `--apply`
      (`format_bytes`' KB/MB/GB thresholds over `sessions` +
      `archived_sessions` and the thread-history database plus `-wal`/`-shm`) and,
      without `--verbose`, Rust's exception block: a blank line, up to 20
      `skipped busy`/`failed` rows and
      `... and <n> more; rerun with --json for the complete report.`.
    - `--apply` initializes local thread metadata (and its rollout backfill)
      before scanning, like Rust's `state_db::try_init`, so the state index
      prunes rollouts that provably belong to unselected threads. The JSON report
      keeps `"thread_id"`/`"message"` as `null` instead of dropping them.
    Evidence: `out/p38g-uuid.txt` (26 uuid shapes, 26/26) and
    `out/p38k-migrate.txt` (8 migrate-rollouts scenarios - empty home, dry run,
    `--verbose`, `--json`, `--apply`, `--apply --json`, both `--thread` forms -
    at 7 byte-identical, the remaining one being the `bytes_processed` counter
    listed under Still open). The 1,068-case inline sweep is now 1,031 identical
    with 0 new diffs (`out/round38-inline.txt`), and the 497 bad-value cases,
    78 round-34 fixes, 16 ordering cases and 31 round 29-34 probes stay at 0.
    `TestMigrateRolloutsProgressMatchesRust`,
    `TestMigrateRolloutsHumanReportMatchesRust`,
    `TestMigrateRolloutsHumanReportTruncatesExceptionsLikeRust`,
    `TestMigrationProgressPlainMatchesRust`,
    `TestMigrationProgressQuietForJSONMatchesRust`,
    `TestMigrationProgressTTYMatchesRust`,
    `TestFormatMigrationElapsedMatchesRust` and
    `TestFormatMigrationBytesMatchesRust` pin the captured behavior.

53. **`review` / `exec review`: clap's `conflicts_with_all`, the `--title`
    requirement and the multiple-times check** - the scope flags were rejected
    with Go's own wording as soon as they were seen (`--base conflicts with
    ...`, `--title requires --commit`), so every diagnostic clap derives after
    the scan (conflict ordering, the derived `--commit <SHA>` echo, the
    `multiple times` rejection) was missing.
    - `cli/cli.go` keeps the review scan but only records where each scope
      argument first appears (`positions`). `--uncommitted`, `--base`,
      `--commit`, `--title` and the `PROMPT` positional are no longer checked
      inline; a repeated SetTrue flag (`--uncommitted`, `--strict-config`, and
      under `exec review` also `--skip-git-repo-check`, `--ephemeral`,
      `--ignore-user-config`, `--ignore-rules`, `--worktree`, both dangerous
      bypasses and `--json`/`--experimental-json`) still fails during the scan,
      because clap validates those while parsing: `the argument '<flag>' cannot
      be used multiple times` beats a later undeclared flag.
    - `cli/usage_clap.go` adds `reviewClapValidation`, which runs after the
      loop like clap's post-parse validation: a second occurrence of a
      single-value option (everything but `--config`, `--enable`, `--disable`)
      reports the same `multiple times` line with the static usage page, then
      the scope arguments are walked in argv order and the first one with a
      conflict reports `the argument 'A' cannot be used with 'B'` (or, for
      several, `cannot be used with:
  B
  C`), and `--title` without a
      scope reports
      `the following required arguments were not provided:
  --commit <SHA>`.
      The usage block follows clap's smart form: the derived `--commit <SHA>`
      comes first, then the accepted options in argv order (conflicting ones
      dropped), then `[PROMPT]`.
    - Because a repeated single-value argument cannot be echoed, both the
      unknown-argument path and the subcommand decorator fall back to the plain
      usage page (`--title a --title b --bogus`, `--base x --base y --bogus`,
      `exec review --title a --title b --bogus`), while a did-you-mean tip that
      points at an option argv already carries no longer adds a second copy
      (`review --title=a --stripe`).
    Evidence: `out/p39t-rust.json` captures 38 diagnostics straight from 0.166
    (6 requirement shapes, 15 conflict shapes including multi-conflict argv
    ordering, 11 multiple-times shapes and 6 unknown-argument interactions);
    `TestReviewScopeDiagnosticsLikeRust` replays all 38. The 33-case conflict
    matrix `out/p39m-*` and the 4-case repeat/unknown matrix are byte-identical
    except `review --title=t --commit abc`, where Rust blocks on auth/network
    and the probe times out (Go reports the missing login immediately).

54. **clap's `multiple times` check across the whole command tree** - every
    command except `review` accepted a repeated flag (or answered it with Go's
    runtime wording), so `doctor --summary --summary`, `queue --thread t
    --thread u`, `exec --model a --model b` and 130 more spellings drifted from
    clap's diagnostics.
    - `cli/usage_clap.go` adds `clapRepeatedOptionError`, a left-to-right replay
      of clap's parse-time occurrence check: a SetTrue flag fails on its second
      occurrence (beating a later undeclared flag, losing to an earlier one),
      help/version stop the scan, and an undeclared option hands the argv back to
      the command parser. Value options are skipped here because clap only
      rejects them in its post-parse validator.
    - `clapRepeatedValueOptionError` replays that post-parse check. `Parse` runs
      it once the command parser returned nil, or - when the parser already
      failed - only if `clapPostParseValidationError` says the failure came from
      the validator or a later runtime gate. Parse-time diagnostics (unknown
      arguments, invalid or missing values, unrecognized subcommands and the
      `clap::Error::raw` value parsers) keep winning, which is why
      `app-server --listen a --listen b` still reports the URL parser first.
    - Repeatable actions stay accepted: the shared `--config`/`--enable`/
      `--disable`/`--add-dir`/`--image` renderings plus the per-command
      `mcp add --env`, `mcp login --scopes` and `plugin marketplace add --sparse`
      lists. `clapUsageTokensRepeat` now covers every command, so an unknown
      argument behind a repeated single-value option keeps clap's plain page
      (`queue --message a --message b --bogus`), and `clapDedupeUsageTokens`
      stops a repeatable option from being echoed twice in the required-argument
      block.
    - `login --api-key` is declared again (optional value, `[<API_KEY>]`), so
      `login --api-key a --api-key b` reports `cannot be used multiple times`
      instead of Go's deprecation notice, and `mcp add --env` rejects a value
      without `=` with clap's
      `invalid value 'e' for '--env <KEY=VALUE>': environment entries must be in
      KEY=VALUE form`.
    - `codex exec --thread-source` consumes its value (`i = next`) instead of
      recursing past it, which used to turn `exec --thread-source x y` into
      `unexpected argument 'y'`.
    - `codex agents` now declares only what 0.166's help page lists (config,
      enable/disable, remote, remote-auth-token-env, cd, no-alt-screen,
      no-daemon): `--model`, `--sandbox`, `--search`, `--image`,
      `--local-provider`, `--add-dir` and `--profile` report clap's
      `unexpected argument` instead of the removed runtime rejection.
    Evidence: `out/round40-bool.json` replays 149 boolean repeats (one per
    declared flag of all 71 command paths, followed by `--bogus`) at 149/149
    byte-identical, `out/round40-value-invalid.json` covers 129 single-value
    repeats with an invalid value at 129/129, and `out/round40-value.json` the
    same 129 with valid values at 128/129 (only `--startup-client-info`, which
    still lacks Go's serde schema, is listed under Still open).
    `TestRepeatedOptionDiagnosticsLikeRust` pins 27 of them,
    `TestRepeatableOptionsStayAcceptedLikeRust` the repeatable accepted set and
    `TestExecThreadSourceConsumesItsValueLikeRust` the arity fix.

### 55. Space-form value timing, positional usage blocks, agents --no-daemon and the ClientInfo schema

- **Lazy space-form validation.** clap validates a space-form option value only
  when the *next* token is one it recognises, so an undeclared flag directly
  after the value wins while any earlier value error preempts a later unknown
  flag. `Parse` now replays the argv prefix before the offending flag
  (`clapPromoteDeferredValueError`) and promotes the deferred value diagnostic
  unless the failing value is the token right before the flag. `exec --color
  bogus --json --bogus`, `exec --color bogus prompt --bogus` and `app-server
  --listen a --strict-config --bogus` report the invalid value, while
  `exec --color bogus --bogus` keeps `unexpected argument '--bogus' found`.
- **Positional offenders keep the plain usage block.** clap matches a stray
  positional with an empty "used" set, so `mcp list --json x`,
  `agents --config c x`, `agents --remote ws://x x`, `review --uncommitted x y`,
  `logout --config c x` and `update --config c x` all print `[OPTIONS]` instead
  of echoing the consumed option (`login --device-code x` is the one command
  that renders its required group member). `codex review` now raises the stray
  PROMPT as a clap diagnostic (`error: unexpected argument 'y' found` plus the
  table usage) instead of a bare `Error:` line.
- **`agents --no-daemon`.** Both spellings print
  `ERROR: --no-daemon cannot be used with codex agents. The agents overview
  requires a shared server. Use codex --no-daemon to work without it.` and exit
  1 before touching the session store; `runAgentsCommandWithIO` gained the
  stderr writer so the message is a raw `ERROR:` line, not `Error:`. The smart
  usage never echoes `--no-daemon` for the agents command
  (`agents --no-daemon --bogus` keeps `Usage: codex agents [OPTIONS]`), because
  the overview pulls the switch from the shared root parser.
- **`plugin marketplace add`.** The CLI unwraps `ErrInvalidPluginRequest`, so a
  bad source prints `invalid marketplace source format; expected owner/repo, a
  git URL, or a local marketplace path` without the `invalid plugin request: `
  prefix the app-server RPC payload carries.
- **ClientInfo serde schema.** `cli/startup_client_info.go` replaces the
  `encoding/json` shortcut with a scanner that reproduces serde_json's
  positions. `ClientInfo` is `{ name: String, version: String, title:
  Option<String> }`: `{}` reports ``missing field `name` at line 1 column 2``,
  `{"name":"x"}` ``missing field `version` at line 1 column 12``, `{"name":1}`
  ``invalid type: integer `1`, expected a string at line 1 column 9``, container
  values report `map`/`sequence` at the byte before the opening bracket, the
  seq form requires three strings (`["a","b",null]` is rejected, a fourth
  element reports `trailing characters`), duplicate fields report
  ``duplicate field `name```, and the syntax diagnostics match
  (`trailing comma`, `expected `:``, `expected `,` or `}``,
  `key must be a string`, `expected ident`, `EOF while parsing a value/list/
  object/string`, `trailing characters`). Type errors and syntax errors follow
  serde's streaming order, so `[1,]` reports the integer element before the
  trailing comma and `nulll` reports the type error before the trailing byte.
  Evidence: `out/round41-startup-compare2.txt` (29/33, the four misses are the
  new `--print-startup-config` requirement listed under Still open),
  `out/round41-startup-lex.txt` (all 16 lexer shapes) and
  `TestStartupClientInfoSerdeDiagnosticsLikeRust` (39 captures) plus
  `TestValueDiagnosticOrderLikeRust` (19 captures).

### 56. app-server startup gate: clap's conditional requires, conflict wording and `=value` diagnostics

- **Conditional `requires`.** `--startup-sign-out` and `--startup-client-info`
  declare `requires("print-startup-config")`, but clap drops a requirement when
  the argument it demands conflicts with an argument it already accepted
  (`validator.rs::is_missing_required_ok` consults `conflicts.gather_conflicts`).
  `--print-startup-config` conflicts with `--stdio`, `--listen <URL>` and the
  hidden `--remote-control`, so `app-server --startup-sign-out --stdio` starts
  the server instead of failing, while `app-server --startup-sign-out` alone
  prints `the following required arguments were not provided:
  --print-startup-config` with
  `Usage: codex app-server --print-startup-config --startup-sign-out`.
  `cli/appserver_startup_gate.go` carries clap's option table (20 entries in
  declaration order, `remote-control` hidden), the
  `<--startup-sign-out|--print-startup-config|--startup-client-info
  <STARTUP_CLIENT_INFO>>` group, the `--ws-*` group, the code-mode host group,
  the conflict map and the conditional requirements; `Parse` runs
  `validateAppServerStartupGate` after the subcommand parse (conflicts first,
  then requirements) and the three old `parseAppServer` strings are gone.
- **Conflict diagnostics.** `app-server --print-startup-config --stdio` reports
  `error: the argument '--print-startup-config' cannot be used with '--stdio'`
  with exit 2. The offender is the earliest accepted argument that has
  conflicts, and when it has several the message switches to
  `cannot be used with:` plus one `  <flag>` line per conflict in argv order
  (`--stdio --listen stdio:// --print-startup-config` names `--stdio` as the
  offender and lists `--listen <URL>` and `--print-startup-config`). The usage
  line echoes the requirement the gate adds plus the accepted tokens minus the
  conflicting one (`--stdio --print-startup-config=x` renders
  `Usage: codex app-server --stdio --print-startup-config`) and never the
  hidden `--remote-control`.
- **`=value` diagnostics.** `--print-startup-config=x`, `=true` and `=--stdio`
  now report `unexpected value 'x' for '--print-startup-config' found; no more
  were expected` (exit 2) instead of the unknown-argument suggestion;
  `appServerAttachedValueDiagnostic` rewinds one token so the pending-value
  spelling `--print-startup-config --stdio=x` reports `--stdio` and keeps the
  consumed `--print-startup-config` in the usage.
- **Runtime handoff.** `app-server --print-startup-config daemon` still reaches
  the daemon `arg_required_else_help` page (group help now skips the parent's
  options before the subcommand), `--print-startup-config <subcommand>` exits 1
  with `Error: --print-startup-config cannot run a subcommand`, and
  `--print-startup-config` skips the WebSocket auth validation because Rust
  prints the report before it opens a listener.
- Evidence: `TestAppServerStartupGateDiagnosticsLikeRust` (116 exact stderr
  captures), `...AcceptsRustCombinations` (28), `...GroupHelpLikeRust` (3) and
  `TestAppServerPrintStartupConfigSkipsTransportValidationLikeRust`, all
  generated from the 0.166 binary through `p42cmp.py`.

### 57. resume/fork TUI start gate: TERM, remote usage errors and override wording

- **Order of the checks.** Rust routes `codex resume` and `codex fork` through
  `run_interactive_tui`, so four checks answer before the session store is
  read: `--no-daemon` together with an actual `--remote` endpoint prints
  `ERROR: --no-daemon cannot be used with --remote.` (an auth-token env var
  alone does not count), TERM `dumb` without a terminal on both stdin and
  stderr prints `ERROR: TERM is set to "dumb". Refusing to start the
  interactive TUI because no terminal is available for a confirmation prompt
  (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM.`,
  `--remote-auth-token-env` without `--remote` prints
  ``ERROR: `--remote-auth-token-env` requires `--remote`.``, and `tui::init`
  then refuses a non-terminal stdin (`stdin is not a terminal`) and finally a
  non-terminal stdout (`stdout is not a terminal`) with the plain `Error:` head
  and exit 1. Go's `guardSessionTUIStart` and `requireSessionTUIStreams` replay
  that order, and `app.Run` now hands stdin/stderr to both commands.
- **Override wording.** resume/fork parse the overrides with the interactive
  CLI context, so an invalid value prints `Error parsing -c overrides: ...`
  without the runtime head, while the archive/delete/unarchive/queue family
  reports `Error: failed to parse -c overrides: ...`;
  `loadSessionRuntimeConfigWithPrefix` renders both.
- **Remote validation stays behind the gate.** `--remote ws://x resume --last`
  still fails with the `invalid remote address` diagnostic for `ws://x` after
  the TERM gate, exactly where Rust resolves the endpoint (before `tui::init`).
- **Headless picker no longer reachable.** Rust has no non-terminal resume
  path, so the Go session picker/summary JSON is now gated the same way; the
  tests that exercise it pass a terminal-simulating reader/writer
  (`newTerminalReader`, `terminalOut`) and pin `TERM` per case.
- Evidence: `TestSessionTUIStartGateLikeRust` (46 exact rc/stderr/error
  captures from the 0.166 binary), `out/round43-tui-gate.txt` (96 cases:
  29 argument shapes x TERM dumb/unset/xterm) and
  `out/round43-session-cli.txt` (36 archive/queue/override cases).

### 58. `codex mcp add`/`remove` write the document toml_edit writes

Rust edits the config document in place (`core/src/config/edit.rs`
`replace_mcp_servers` plus `document_helpers.rs`
`serialize_mcp_server_table`), so `codex mcp add` leaves the rest of the file
alone and writes each server table from its struct declaration order. Go used to
emit an empty `[mcp_servers]` header and alphabetize both the server keys and
every sibling section.

- **Implicit tables.** `config.OrderedTable` (config/api.go) carries an
  insertion order and an `Implicit` flag. An implicit table contributes no
  header but still nests, so the file starts with `[mcp_servers.alpha]` instead
  of `[mcp_servers]`. Plain `map[string]any` values keep the previous sorted
  rendering, and `MarshalJSON` delegates to the map so fingerprints and the
  JSON views do not change.
- **Server field order.** `mcpServerToConfigValue` sets stdio
  `command`/`args`/`env`/`env_vars`/`cwd` and streamable-HTTP
  `url`/`bearer_token_env_var`/`http_headers`/`env_http_headers`/
  `http_headers_helper`, then the shared `auth`/`enabled`/`environment_id`/
  `required`/`startup_readiness`/`supports_parallel_tool_calls`/
  `tool_input_schema_max_bytes`/`omit_tools_from`/`startup_timeout_sec`/
  `tool_timeout_sec`/`default_tools_approval_mode`/`enabled_tools`/
  `disabled_tools`/`scopes`/`oauth`/`oauth_resource`/`tools`. `oauth` keeps
  `client_id`/`client_secret`/`callback_url`/`authorization_server_issuer`, and
  `tools` is implicit with name-sorted entries. `env`, `env_vars` children and
  the header maps sort like Rust's `table_from_pairs`.
- **Document order.** `mcpServerSectionOrder` reads the existing
  `[mcp_servers.<name>]` headers and keeps that order, appending new names
  alphabetically, so adding `alpha` after `zeta` leaves `zeta` first and
  replacing a server rewrites it in place. `config` writes now wrap the top-level
  table with `orderedConfigValues`/`tomlDocumentTopLevelOrder`, so unrelated
  sections keep their position and `mcp_servers` is appended at the end. A table
  that only parents sub-tables no longer gets a header of its own
  (`[features.network_proxy]` without `[features]`), matching toml_edit.
- **Removal.** An empty server set is written as a deleted key, so removing the
  last server leaves the document empty instead of a stray `[mcp_servers]`
  header, and other sections survive untouched.
- Evidence: 40-case Rust 0.166/Go differential (`mcp add`/`mcp remove` over ten
  seed documents x four command sets); the 20 remaining diffs all belong to the
  two pre-existing writer gaps recorded under Still open (comments dropped,
  multi-line values reflowed), none to `mcp_servers` ordering or formatting.
  Regressions: `TestMCPAddWritesServerTableLikeTomlEdit`,
  `TestMCPAddKeepsServerInsertionOrderLikeTomlEdit`,
  `TestMCPAddAppendsTableAfterExistingSectionsLikeTomlEdit`,
  `TestMCPAddReplacesExistingServerTableInPlaceLikeTomlEdit`,
  `TestMCPRemoveLastServerEmptiesDocumentLikeTomlEdit`,
  `TestMCPRemoveLastServerKeepsOtherSectionsLikeTomlEdit` in `app`, and
  `TestRenderTOMLKeepsOrderedTableInsertionOrderLikeTomlEdit`,
  `TestRenderTOMLKeepsServerFieldOrderLikeTomlEdit`,
  `TestRenderTOMLSortsPlainMapSections`, `TestOrderedTableMarshalJSONMatchesMap`
  in `config`.

### 59. Empty values are present values: `mcp` NAME/`--url`, `queue --message` and session lookups

clap separates "the token is missing" from "the token is empty": an empty
string is a present `Option`/`String` value, while `NonEmptyStringValueParser`
argtypes reject it at parse time. Go tested trimmed emptiness, so
`codex archive ""`, `codex mcp get ""` and `codex queue --message ""` took the
clap diagnostic path where Rust reaches its runtime.

- **Session mutations and queue targets.** `parseSessionMutation` and the
  `queue` parser key off a `positionalSeen` flag instead of `Target != ""`, so
  `archive ""`, `delete ""`, `unarchive ""` and `queue --thread ""` reach
  `resolveSessionMutationTarget`/`resolveQueueSessionTarget`, which print Rust's
  `No <scope> session found matching '<target>'.` / `No active session found
  matching '<target>'.`. The name index (`sessionIDByNameWithArchiveFilter`,
  `sessionIDByUniqueActiveName`) treats an empty needle as a miss because Rust
  compares `Option<String>`, never `""`. `resolveRemoteSessionMutationTarget`
  keeps `SESSION is required` only for the nil-options case.
- **`mcp` NAME presence.** `MCPOptions` gained `URLSet`/`OAuthClientIDSet`/
  `OAuthClientSecretSet`, and `get`/`add`/`remove`/`login`/`logout` track
  `nameSeen`, so `codex mcp get ""` reaches `No MCP server named '' found.`
  (remove: `invalid server name '' ...`; login: `... found in configuration.`)
  and a second positional reports clap's `unexpected argument '<arg>' found`
  block against the static page instead of the Go-only
  `mcp remove accepts exactly one NAME`.
- **`mcp add` requires.** The required-argument report is one clap block:
  `--url <URL>` and `--oauth-client-id <CLIENT_ID>` when an OAuth flag was seen,
  then `<COMMAND|--url <URL>>`, then `<NAME>`; when `--url` is the only missing
  piece the message is the singular `the following required argument was not
  provided: url` with the root usage page. `--url=` satisfies the transport
  choice and is persisted as `url = ""`, and an empty
  `--oauth-client-secret` reports `must not be empty`.
- **`queue --message`.** An empty `--message`/`--message=` is clap's
  `a value is required for '--message <TEXT>' but none was supplied`, while
  `"  "` is a legal message (Rust does not trim), and the required check uses
  `MessageSet` rather than a trimmed comparison.
- **OAuth callback URLs.** `mcp add --oauth-client-id` resolves the callback URL
  before writing, so `OAuth callback URL: <url>` prints after
  `Added global MCP server` and `[mcp_servers.<name>.oauth] callback_url` is
  persisted. `MCPOAuthCallbackID` normalizes the parsed URL the way the `url`
  crate serializes it: lowercased scheme and host, http/ws:80 and https/wss:443
  dropped, an empty path spelled `/`, and the fragment cleared.
- Evidence: a 164-case sweep (ten command paths x empty positional, extra
  positional, and every `--flag=` / `--flag=bogus`) has no deterministic diff
  against Rust 0.166 (`out/r45-empty.txt`); the three Rust probe timeouts
  (`archive ""`, `delete ""`, `unarchive ""`) are SAME when rerun with a 120 s
  window, and the 14 session-command shapes are all SAME. Callback IDs match
  Rust for `https://a` -> `q5hkBE_CdBkI`, `https://A.Example` -> `vv3kmKRbbIII`,
  `https://a:443/mcp` -> `CwkrpmlhFpeS`, `http://a:80/mcp` -> `1oX_iem5GZNa`
  and `https://mcp.example.com/mcp?tenant=one` -> `XuuuHAzzHOni`. Regressions:
  `TestMCPEmptyNameIsPresentLikeClap`,
  `TestMCPExtraPositionalIsUnexpectedArgumentLikeClap`,
  `TestMCPAddEmptyURLSatisfiesTransportLikeClap`,
  `TestMCPAddOAuthRequiresLikeClap`,
  `TestQueueEmptyMessageIsClapEmptyValueLikeRust` and
  `TestMCPOAuthCallbackIDMatchesRustURLSerialization`.

### 60. `codex mcp add`/`remove` edit the config document in place

`replace_mcp_servers` (core/src/config/edit.rs) edits the parsed document, so
every byte outside `mcp_servers` survives: comments, multi-line strings,
multi-line arrays, blank lines and key order. Go re-serialized the whole file
from its parsed map, which dropped comments and reflowed multi-line values.

- **Document splice.** `config.spliceMCPTablesIntoDocument`
  (config/toml_document_splice.go) scans the document into top-level constructs
  (root key/values that may span lines, and table headers with their bodies) and
  rewrites only the `mcp_servers` region; everything else is copied verbatim.
- **Decor rules.** A removed or replaced table takes the blank and comment lines
  that decorate it (toml_edit attaches them as the item's prefix), the
  replacement region is rendered with toml_edit's default one-blank-line decor,
  a document whose first construct is that table gets no leading blank line
  (`DEFAULT_TABLE_DECOR` is skipped for the first table of a document), trailing
  blank and comment lines stay at the end of the file (`DocumentMut` prints them
  after every table), an explicit `[mcp_servers]` header is kept, a missing
  final newline is added, and CRLF is re-emitted as LF.
- **BatchWrite.** `editsReplaceOnlyMCPTables` routes a replace of exactly
  `mcp_servers` through the splice; every other write still renders the whole
  document.
- **Removal of a missing server.** `codex mcp remove <unknown>` no longer writes
  the file, because Rust only edits the document when the server was configured.
- Evidence: the 40-case order corpus is 33/40 byte-identical and the 7 remaining
  diffs are the `[[profiles]]` load rejection listed under Still open; a second
  33-case corpus (explicit header, CRLF, missing final newline, comments inside
  kept tables, multi-line values, nested tables) is 31/33, the two diffs being
  the `[features.x]` load rejection. Regressions:
  `TestSpliceMCPTablesKeepsDocumentTriviaLikeTomlEdit`,
  `TestSpliceMCPTablesRefusesInlineTablesLikeTomlEdit` in `config`, and
  `TestMCPAddKeepsDocumentTriviaLikeTomlEdit`,
  `TestMCPRemoveMissingServerLeavesConfigUntouchedLikeRust` in `app`.

### 61. Config load: serde's typed pass, position-carrying errors and three mcp wrappers

Rust deserializes `ConfigToml` (and every `RawMcpServerConfig`) before a
command runs, so a type or variant mismatch fails the load with a serde
message. Go parsed the same document into `map[string]any`, so every `mcp`
command accepted it, wrote files and exited 0.

- **Shape pass.** `config.ValidateConfigValues` replays `ConfigToml`'s 104
  top-level fields (`config/config_toml_schema.go`): scalar kinds, `features`
  as a boolean-only sub-table, the `mcp_servers` map and each entry as
  `RawMcpServerConfig`. Enum fields list their variants the way serde does
  (`expected `a` or `b``, `expected one of `a`, `b`, `c``); an enum value must
  be a string (`invalid type: unit variant, expected string only`) unless it is
  a table, whose single key is taken as the variant name.
  `forced_chatgpt_workspace_id` is an untagged `Repr` and rejects anything that
  is neither a string nor an array of strings.
- **stdio transport rules.** With `command` present, `url`,
  `bearer_token_env_var`, `http_headers`, `env_http_headers`,
  `http_headers_helper`, `auth` and `oauth_resource` report `<field> is not
  supported for stdio` at the server path; a server with neither `command` nor
  `url` reports `invalid transport`; a non-table entry reports `invalid type ...
  expected struct RawMcpServerConfig`.
- **Unknown fields stay allowed.** `foo = 1` and `[features] x = true` load in
  Rust, so the pass deliberately does not use `deny_unknown_fields`.
- **Position-carrying errors.** `projects` and `project_root_markers` produce
  errors that quote `<path>:<line>:<column>`. `config.ConfigDocumentLocator`
  recomputes that position from the document text: `projects` keeps the inner
  serde error as anyhow's second cause (the `in `projects`` line indents under
  it), `project_root_markers` prints only the located line, and `[[projects]]`
  reports the column of its `[`.
- **Three wrappers.** `codex mcp list/get/add/login/logout` wrap a load failure
  as `failed to load bootstrap configuration` (the typed pass) or `failed to
  load configuration` (Config::load semantics such as
  `experimental_thread_store_endpoint`, the legacy `profile` selector,
  `default_permissions` without a `[permissions]` table, and an unknown
  `model_provider`). `codex mcp remove` decodes only the server map, so
  unrelated top-level type errors and those semantic errors do not stop it,
  while a malformed server table reports `failed to load MCP servers from
  <home>` with `x.command` paths (no `mcp_servers.` prefix).
- Evidence: the 437-document corpus (`p47diff.py`, `out/r47-diff7.txt`) went
  from 57 diffs to 3. The leftovers are `model_instructions_file`,
  `model_catalog_json` and `experimental_compact_prompt_file` pointing at a
  missing file: Rust reads them during load and prints the Windows-localized
  `(os error 2)` text, which Go does not reproduce. Regressions:
  `TestValidateConfigValuesReportsSerdeShapeErrors`,
  `TestValidateConfigDocumentValuesReportsPositions`,
  `TestValidateMCPServersValuesUsesServerRelativePaths` and
  `TestValidateLoadedConfigSemantics` in `config`.

### 62. Nested config tables: profiles, model_providers, windows, history and shell policy

Entry 61 replays the top-level `ConfigToml` shapes; the nested tables still let
Go accept documents Rust rejects at bootstrap.

- **`profiles.<name>`** is a `ConfigProfile`: field types plus the enums
  `approval_policy`, `approvals_reviewer`, `sandbox_mode`,
  `model_reasoning_summary`, `model_verbosity`, `personality`, `web_search`,
  and `tui.session_picker_view` (`expected `comfortable` or `dense``), with
  `features` as a boolean-only sub-table and `tools`/`analytics`/`windows` as
  structs. `model_reasoning_effort` and `plan_mode_reasoning_effort` accept any
  string in Rust, so they stay unvalidated, and unknown keys stay allowed.
- **`model_providers.<id>`** is a `ModelProviderInfo`: string/bool/u64 fields,
  `http_headers`/`query_params`/`env_http_headers` as string maps, and
  `wire_api` as the single-variant `responses` enum that keeps serde's own
  `wire_api = "chat"` removal message. After serde reads every entry, Rust's
  `validate_model_providers` reports against the table itself:
  `model_providers.<id>: provider name must not be empty`, and
  `model_providers contains reserved built-in provider IDs: ...` for `openai`
  (unless it only sets `model_catalog_url`), `ollama` and `lmstudio`.
- **`windows`, `history`, `shell_environment_policy`** carry their own field
  sets: `windows.sandbox` (`elevated`/`unelevated`/`mxc`),
  `history.persistence` (`save-all`/`none`) with `max_bytes` as `usize` (a
  negative literal reports `invalid value: integer `-1`, expected usize`), and
  the shell policy's `inherit` enum, boolean flags and string-map `set`.
- **Table-header positions.** The shell policy's `exclude`, `include_only` and
  `filters` deserialize through a custom filter reader, so serde reports them at
  the `[shell_environment_policy]` header (`<path>:<line>:1: <message>`)
  instead of a dotted path;
  `config.ConfigDocumentLocator.shellEnvironmentPolicyPosition` reproduces
  that, including a header that does not sit on line 1.
- Evidence: three focused corpora against the 0.166 binary are byte-identical:
  33 profile documents (`p48prof3.py`), 21 provider documents (`p48mp3.py`) and
  27 windows/history/shell documents (`p48w3.py`); the 437-document top-level
  corpus stays at 3 diffs (`out/r48-diff1.txt`). Regressions:
  `TestValidateNestedConfigTables` in `config`.

### 63. The TUI table: flattened notification settings, presets and keymap

`[tui]` was the largest remaining untyped table. It mixes ordinary fields with
`#[serde(flatten)] notification_settings`, which changes where serde reports a
failure.

- **Named fields.** Booleans, `theme`/`pet` strings, `status_line` and
  `terminal_title` as string sequences, `mouse_scroll_speed` as `f64`,
  `terminal_resize_reflow_max_rows` as `usize`, and the enums `copy_on_select`
  (`auto`/`always`/`never`), `right_click_paste` (`auto`/`on`/`off`),
  `alternate_screen`, `pet_anchor` (`composer`/`screen-bottom`),
  `session_picker_view`, `agents_overview_grouping`
  (`project`/`status`/`model`) and `resume_cwd` (`current`/`session`), with
  `effects`/`rendering` as boolean structs and `keymap.global` as
  `TuiGlobalKeymap`.
- **Flattened notifications.** `notifications` is an untagged enum that accepts
  a boolean or a list of strings, and `notification_method`/
  `notification_condition` are externally tagged enums whose non-string form
  reads `invalid type: ..., expected string or map` (an empty table reads
  `invalid value: map, expected map with a single key`). All three report
  against `tui` itself, and serde reads them only after the named fields, so
  `[tui] notifications = 5` plus `[tui.effects] starfield = 5` reports the
  effect.
- **Presets.** `presets` is a map of `QuickDial`, whose `input` is mandatory:
  a table without it reports `missing field `input`` at `tui.presets.<key>`.
- **Untagged enum shape.** `forced_chatgpt_workspace_id` still accepts a bare
  string, while `Notifications` does not, so the untagged validator now carries
  which representations it may accept instead of assuming the `Repr` rules.
- Evidence: 47 `[tui]` documents covering every field, the flatten ordering and
  the presets cases are byte-identical to 0.166 (`p49tui5.py`,
  `out/r49-tui5.txt`); the 81-document nested corpus and the 437-document
  top-level corpus keep 0 and 3 diffs (`out/r49-diff1.txt`). Regressions:
  `TestValidateTuiConfigTable` in `config`.

### 64. The remaining struct config tables: flattened maps, integer widths and struct variants

Entry 61 typed the 104 top-level `ConfigToml` fields, entry 62 the nested
`profiles`/`model_providers`/`windows`/`history`/`shell_environment_policy`
tables and entry 63 `[tui]`. This round types every other subtable serde walks
at load, so a document 0.166 refuses is refused here with the same body and the
same path.

- **Tables.** `auto_review`, `memories`, `skills` (with its `Vec<SkillConfig>`
  entries), `agents`, `apps`, `analytics`, `audio`, `browser_use`, `cloud`,
  `computer_use` (including the macOS bundle ids and the Windows `exes`
  entries), `feedback`, `ghost_snapshot`, `goals`, `hooks` (the eleven
  `HookEventsToml` events, `MatcherGroup` and `HookStateToml`),
  `mcp_enterprise_managed_auth` (with its mandatory `idp`), `notice`,
  `orchestrator`, `otel`, `permissions`, `plugins`, `realtime`,
  `sandbox_workspace_write`, `tool_suggest` and `tools`.
- **Integer widths.** `u32`, `u64`, `i32`, `usize` and the two `NonZero`
  types reject an out-of-range literal with `invalid value: integer ...`,
  while a float or a string still reads `invalid type: ...`. `agents.max_depth`
  therefore fails on `1.5` (type) and on `2147483648` (value).
- **Flattened maps.** `agents`, `apps`, `plugins` and `permissions` are
  `#[serde(flatten)]` maps: every key their named fields do not claim is an
  entry, and serde reports the entry's failures against the *table*, not the
  entry. The collapse is inherited by nested structs, so
  `[apps.foo] tools = { x = 5 }` reports `expected struct AppToolConfig` at
  `apps`, and `[permissions.x.network] mode = "bogus"` reports at
  `permissions`. Named fields of the same table keep their own path:
  `[apps._default] enabled = 5` still reads `apps._default.enabled`, and
  `[plugins._default] enabled = 5` reads `plugins._default.enabled`.
- **Sequences of structs.** `skills.config`, `computer_use.windows.exes` and
  `tool_suggest.discoverables` keep the field path for both the item type and
  the item's own fields (`skills.config.path`), and a missing mandatory field
  is reported at the sequence path (`missing field `enabled`` at
  `skills.config`).
- **Struct variants.** `otel.exporter`/`trace_exporter`/`metrics_exporter` are
  externally tagged with two unit and two struct variants: a bare `otlp-http`
  reads `invalid type: unit variant, expected struct variant`, a non-table
  payload `expected struct variant OtelExporterKind::OtlpHttp`, and an empty
  payload `missing field `endpoint``.
- **Untagged shapes.** `audio.microphone_channel` accepts 1..=65535 or a
  sequence of them and otherwise reports the untagged message, and
  `tools.web_search` is bool-or-struct so every wrong field inside it collapses
  into the single `WebSearchToolConfigInput` message.
- **Aliases.** `agents.max_threads` reports the canonical
  `agents.max_concurrent_threads_per_session` path, as serde does.
- **`otel.log_sse_deltas`.** 0.166 has no such field in `OtelConfigToml`, so a
  non-boolean value is ignored there; the Go loader's earlier pre-emptive
  `otel.log_sse_deltas must be a boolean` rejection is gone and the value is
  still honoured when it *is* a boolean.
- Evidence: 172 documents covering every table above are byte-identical to
  0.166 (`p50diff.py`, `out/r50-diff2.txt`), the 81-document nested corpus and
  the 47-document `[tui]` corpus stay at 0 diffs while the 437-document
  top-level corpus keeps its 3 known diffs (`out/r50-nested.txt`,
  `out/r50-tui.txt`, `out/r50-top.txt`). Regressions:
  `TestValidateRemainingConfigTables` in `config`.

### 65. Config::load reads the three path settings

The typed pass accepted a document naming a missing file; Rust fails the same
document while it builds the config, after the semantic checks.

- **Resolution.** `model_instructions_file`,
  `experimental_compact_prompt_file` and `model_catalog_json` go through the
  serde `AbsolutePathBuf` guard, so a relative value is joined to the codex home
  (`model_instructions_file = "x"` looks at `<home>\x`), `~/...` expands to the
  user's home, and an empty value resolves to the codex home itself.
- **Read failures.** Rust's `io::Error` Display on Windows is the localized
  `FormatMessage` text plus ` (os error <code>)`, so a missing file reports
  `failed to read model instructions file <path>: <localized> (os error 2)`, a
  directory reports `(os error 5)`, and a missing parent reports `(os error 3)`.
  Go now renders the same text with `windows.FormatMessage`, and opens the
  target with read access only so a directory reports access denied instead of
  `ERROR_INVALID_FUNCTION`.
- **Order.** The instructions file is read first, then the compact prompt file,
  then the catalog; an earlier serde or semantic failure still wins, and the
  catalog's read error keeps Rust's bare wording (no `failed to read ...`
  wrapper, `stream did not contain valid UTF-8` for bad bytes).
- **Empty files.** A blank or whitespace-only instructions or prompt file
  reports `<subject> is empty: <path>`, and invalid UTF-8 inside them reports
  `failed to read <subject> <path>: invalid utf-8 sequence of N bytes from index
  I`.
- **Catalog contents.** A catalog without models reports
  `model_catalog_json path `<path>` must contain at least one model`.
- Evidence: 22 documents covering missing files, directories, empty files, bad
  UTF-8, ordering, `~`, absolute paths and catalog payloads are byte-identical
  to 0.166, including the catalog JSON-parse cases since entry 67 replaced the
  `encoding/json` diagnostics (`p51diff_p_p52.py`, `out/r51-diff1.txt`). The 437-document top-level corpus is now
  byte-identical (`out/r51-top.txt`), and the 172/81/47 corpora keep 0 diffs.
  Regressions: `TestValidateLoadedConfigFilesReadsPathSettings`,
  `TestValidateLoadedConfigFilesReadsModelCatalog`,
  `TestValidateLoadedConfigFilesKeepsSerdeOrder` and
  `TestResolveConfigFilePath` in `config`.

### 66. TOML syntax errors: toml_edit's wording, column and caret

A document that does not parse is rejected by the loader itself, so every
surface follows the `toml` crate rather than `go-toml`.

- **Messages.** `toml_edit` names the construct it wanted (`unclosed table,
  expected `]``, `string values must be quoted, expected literal string`,
  `invalid fraction, expected digits`, `duplicate key`, `unclosed array
  table`, ...) where go-toml reports its own parser state (`expected ']' to
  close table name`, `unexpected character U+000A at start of value`, ...).
  `translateTOMLSyntaxMessage` maps the messages measured against 0.166 and
  leaves anything unrecognised as it is.
- **Columns and carets.** go-toml points at the token that stopped the parser;
  toml_edit reports the span of the offending value. Failures that reject a
  value widen the position to the start of the bare run (`a = true false`
  reports 1:5 with ten carets, `a = 1.2.3` 1:8 with two), `]]`, multi-line
  strings and `\q`-style escapes shift by one, a bad surrogate escape by two,
  seconds are bounded by 60 not 59, an overflowing radix literal prints its
  decimal value, and duplicate keys point at the last segment of the key
  (`a.b = 1` twice reports 2:3; `b.c = 1` followed by `[a.b]` reports 3:4).
- **Surfaces.** `codex mcp list|get|add|login` wrap the failure in `failed to
  load bootstrap configuration` with two causes: the `path:line:column:
  message` line, then `TOML parse error at line L, column C` with the quoted
  source line, a caret and the message repeated. `codex mcp remove` uses
  `failed to load MCP servers from <home>` with the same two causes.
  `codex exec`/`codex review` print `Error loading config.toml:` and only the
  quoted line (no trailing message) and exit 1; `codex login status` prints
  the position line alone, which is `ConfigLoadError`'s own Display.
- Evidence: the 111-document syntax corpus x `mcp list`, `mcp remove`,
  `login status`, `exec` (444 runs) is byte-identical to 0.166, apart from
  three valid documents where `exec` needs a live model (`out/r52-diff3.txt`).
  The 437/172/81/47 corpora keep 0 diffs and the 22-document path corpus keeps
  its four catalog JSON cases. Regressions:
  `TestTOMLDiagnosticMatchesRustParseErrors`,
  `TestConfigLoadErrorUsesRustMessageWording`,
  `TestReadConfigDocumentFileReportsSyntaxDiagnostic` and
  `TestTOMLDiagnosticIgnoresOtherErrors` in `config`.

### 67. `model_catalog_json`: serde_json's structure and diagnostics

Rust parses the catalog with `serde_json::from_slice::<ModelsResponse>`, so every
syntax error, type mismatch, missing field and unknown variant names the serde
type and a byte position; Go answered with `encoding/json`'s wording and never
walked the model fields.

- **Reader.** `config/model_catalog_json.go` carries a small serde_json-shaped
  reader over the raw bytes: `expected ident`, `expected value`, `` `expected `:` ``,
  `key must be a string`, `trailing comma`, `invalid number`,
  `invalid escape`, `control character (\u0000-\u001F) found while parsing a
  string`, `EOF while parsing a value|an object|a list|a string` and
  `trailing characters`, positioned as `at line L column C` from the consumed
  byte offset (serde's `peek_position` is `min(len, idx + 1)`).
- **Tables.** `ModelsResponse`, `ModelInfoWithLegacyBaseInstructions`,
  `ModelInfo` and `ModelMessages` are walked field by field, so missing
  required members (`slug`, `display_name`, `shell_type`, ...), duplicate
  fields, ``invalid type: integer `5`, expected a string``,
  struct-from-sequence
  length errors and enum `unknown variant` all print serde's text. The tables
  with required members are `TruncationPolicyConfig`, `ReasoningEffortPreset`,
  `ModelAvailabilityNux`, `ModelServiceTier`, `ModelInfoUpgrade`,
  `ModelTokenBudgetConfig`, `ConfirmationPolicies` (both members optional) and
  `ModelAccessPrograms`; the remaining nested message tables are all-optional
  and accept any object.
- **Quirks kept.** A float where an integer is required reports `invalid type:
  map, expected i64` (serde peeks the buffered value), an unknown
  `available_access_programs.cyber` entry is dropped instead of rejected, and
  the `cyber` list itself is required.
- **Surfaces.** `ValidateLoadedConfigFiles` keeps both wrappers
  (``failed to parse model_catalog_json path `<path>` as JSON: <error>`` and
  `must contain at least one model`).
- Evidence: the 138-document syntax/type corpus, the 148-document mutation
  corpus and the 24 nested-table documents report 0 diffs against 0.166
  (`out/r53-diff1.txt`, `out/r53-diff-fuzz.txt`, `out/r53-table10.txt`), and
  the 437/172/81/47 corpora keep 0 diffs. Regressions:
  `TestValidateModelCatalogJSONReportsSerdeDiagnostics` (76 documents),
  `TestValidateModelCatalogJSONAcceptsCatalog` and
  `TestValidateModelCatalogJSONAcceptsLegacyBaseInstructions` in `config`.

### 68. Inline `mcp_servers` values: toml_edit's table conversion

`replace_mcp_servers` edits the document in place, and
`document_helpers::ensure_table_for_write` turns an inline `mcp_servers` value
into a regular table (`table_from_inline`) before inserting the replacement
servers. Go refused those documents and fell back to the whole-document
writer, so `mcp add` rewrote `mcp_servers = { alpha = { .. } }` as
`[mcp_servers.alpha]` blocks and `mcp remove` left the inline value untouched.

- **Header.** The converted table prints the key's decor: a comment above the
  key lands inside the brackets (`[# c\nmcp_servers ]`) and the space before
  `=` becomes the trailing blank in the header (`mcp_servers = { .. }` prints
  `[mcp_servers ]`, `mcp_servers={ .. }` prints `[mcp_servers]`). A trailing
  comment after the value is dropped with the value's decor, and an empty inline
  table prints no header at all.
- **Order.** toml_edit prints the root values before the tables, so the
  converted table moves below the other root keys; new servers follow it as
  `[mcp_servers.<name>]` tables one blank line below.
- **Pairs.** Surviving servers keep their inline spelling: `table_from_inline`
  keeps each value's prefix and clears its suffix, so `alpha ={command="a"}` and
  `alpha = { command = "a" }` both survive verbatim apart from the default key
  suffix (`alpha =`). Merging a changed server follows
  `document_helpers::merge_inline_table`: dropped fields disappear with their
  comma, kept fields keep their own decor, and a new field arrives as
  `, name = value` with the serializer's default ` ` before the value, which is
  why `mcp add alpha -- a x y` prints `{ command = "a" , args = ["x", "y"] }`.
- **Explicit section.** A `[mcp_servers]` header whose body spells servers
  inline keeps that header and those lines; only new names become sub-tables.
- Evidence: 25 seed documents (inline values with and without comments, empty
  tables, explicit sections, CRLF files, empty files) x `mcp add <name> -- cmd`,
  `mcp add <existing>`, `mcp add <name> -- cmd args`, `mcp add --env`,
  `mcp add --url` and three `mcp remove` shapes (200 runs) are byte-identical
  to 0.166 (`out/r54-inline.txt`). Regressions:
  `TestSpliceMCPTablesConvertsInlineValuesLikeTomlEdit` (ten documents) and
  `TestSpliceMCPTablesKeepsDocumentTriviaLikeTomlEdit` in `config`.

### 69. `mcp get`/`mcp list`: serde's key order, f64 seconds and the auth status enum

The JSON payloads are built with `serde_json::json!` in a crate that enables
`preserve_order`, so keys print in the order the macro spells them, timeouts go
through `Duration::as_secs_f64()` and the status comes from the core
`McpAuthStatus` enum. Go built the payloads from `map[string]any`
(`encoding/json` sorts keys), wrote integral timeouts as `3`, escaped
`<redacted>` as `\u003c...` and printed the app-server's camelCase status
names.

- **Transport order.** A stdio transport prints `type, command, args, env,
  env_vars, cwd` and a streamable HTTP one prints `type, url,
  bearer_token_env_var, http_headers, env_http_headers, http_headers_helper`.
  Absent maps stay `null`, an empty `args` list stays `[]`, and empty strings
  inside `args` survive.
- **`mcp get` fields.** The JSON branch carries `name, enabled, disabled_reason,
  transport, enabled_tools, disabled_tools, startup_timeout_sec,
  tool_timeout_sec` and no `omit_tools_from` (that key only exists in the config
  file). `mcp list` entries carry `name, enabled, disabled_reason, transport,
  startup_timeout_sec, tool_timeout_sec, auth_status`.
- **Redaction.** `http_headers_helper` is reported as the literal `<redacted>`
  (`map(|_| "<redacted>")`) in `mcp get --json`; the text branches print
  `http_headers_helper: <redacted>` and `-` when unset, and serde's string
  escaping is not HTML escaping.
- **Seconds.** `startup_timeout_sec`/`tool_timeout_sec` print as f64 (`3.0`,
  `1.5`) because `as_secs_f64` never drops the fractional part.
- **Auth status.** `compute_auth_status` answers `Unknown` when the probe
  errors and `Unsupported` for a disabled or stdio server; streamable HTTP
  follows `auth_status_before_discovery`: a configured `bearer_token_env_var` or
  `Authorization` header is `Bearer token`, stored credentials are `OAuth` or
  `Not logged in`, a `http_headers_helper` never runs during status inspection
  (so without stored credentials the state stays `Unknown`), and everything
  else falls through to a five-second OAuth metadata discovery where
  `Ok(Some)` is `Not logged in`, `Ok(None)` is `Unsupported` and an error is
  `Unknown`. The text column prints the enum's `Display` names, while the JSON
  payload uses the enum's `rename_all = "snake_case"` names (`unsupported`,
  `not_logged_in`, `bearer_token`, `o_auth`, `unknown`) - the CLI payload
  therefore differs from the app-server protocol's camelCase.
- Evidence: 8 configs x four `mcp list`/`mcp get` shapes (32 runs), six configs
  x `mcp get --json`/`mcp list --json` (18 runs) and a 12-config auth matrix x
  `mcp list`, `mcp list --json`, `mcp get --json` (36 runs) are byte-identical
  to 0.166 (`out/r55-mcp.txt`, `out/r55-mcpjson.txt`, `out/r56-auth.txt`).
  Regressions: `TestMCPGetJSONMatchesRustSerialization`,
  `TestMCPListJSONMatchesRustSerialization` and
  `TestMCPListJSONReportsStoredCredentialsLikeRust` in `app`.

### 70. `mcp add --url`: the login hint probe keeps Rust's five second cap

`run_add` asks `oauth_login_support(..., OAuthDiscoveryTimeout::LOCAL, ...)`, and
`LOCAL` caps every discovery request at `DISCOVERY_TIMEOUT` (five seconds). Go
capped the same probe at two seconds, gave up first and reported `Unknown`, so a
server that answered slowly printed "MCP server may or may not require login" in
Go and stayed silent in Rust.

- **Cap.** `mcpCLIAuthDiscoveryTimeout` is five seconds and is shared by the
  add-time hint probe and the `mcp list` status probe; starting the local
  callback server keeps its own `mcpCLIOAuthLoginStartTimeout`.
- Evidence: a first request delayed 2.6s and answered with 404
  (`out/r57-slow-probe.txt`), an immediate 404, an unreachable port and an
  unresolvable host all match 0.166 byte for byte (`out/r57-probe.txt`); before
  the fix the delayed case printed the hint in Go only. Regressions:
  `TestMCPAddSlowOAuthProbeKeepsWaitingLikeRust` and
  `TestMCPAddUnknownOAuthPrintsLoginHint` in `app`.

### 71. `apply_patch` failure text: anyhow's context hides the write cause

0.166 reports delete, write and move failures through anyhow contexts
(`Failed to delete file {}`, `Failed to write file {}`,
`Failed to create parent directories for {}`, `Failed to remove original {}`)
and the patch CLI prints only the context line, while an update read is an
`ApplyPatchError::IoError` whose Display keeps `: <io error>` inline. Go
appended `: <cause>` to the delete and write contexts, returned the raw
`mkdir` error for an add, and let `os.ReadFile` open a directory (Go reports
`(os error 1)`, Rust's `CreateFileW` reports `(os error 5)`).

- **Delete.** A directory target and a failed stat both print
  `Failed to delete file <path>` with no suffix.
- **Write.** `Failed to write file <path>` and
  `Failed to create parent directories for <path>` carry no cause text.
- **Update.** A directory target prints
  `Failed to read file to update <path>: <io error>` with Rust's io error:
  `ERROR_ACCESS_DENIED` (5) on Windows through the new
  `rustDirectoryReadError` helper, `EISDIR` (21) elsewhere.
- **Move.** A failed remove keeps the capitalised context
  `Failed to remove original <path>`.
- Evidence: ten `--codex-run-as-apply-patch` documents (delete a directory,
  delete a directory holding files, delete a missing path, delete a file,
  update a directory, update a missing path, add over a directory, add under a
  file, nested add, in-place update) are byte-identical to 0.166 in exit code,
  stdout, stderr and the resulting tree (`out/r58-patch.txt`,
  `out/r58-patch-extra.txt`). Regressions:
  `TestApplyDeleteDirectoryPrintsOnlyRustContextLine`,
  `TestApplyWriteFailuresPrintOnlyRustContextLine` and
  `TestApplyUpdateDirectoryReportsRustReadError` in `applypatch`.

### 72. `apply_patch` context errors name the file the way `compute_replacements` does

`compute_replacements` reports a missing `@@` anchor as
`Failed to find context '<ctx>' in <path>` and a hunk body that does not match
as `Failed to find expected lines in <path>:\n<lines>`. Go printed
`failed to find context '<ctx>'` and appended the path after the body
(`failed to find expected lines:\n<lines> in <path>`), because the file name
only reached the error through the update wrapper.

- **Path threading.** `applyUnifiedDiffToContent`, `applyUpdateDiffToContent`
  and `computeReplacementsPreserving` now take the resolved path, so both
  messages spell Rust's text and the wrapper no longer appends ` in <path>`.
- Evidence: the 14-document parse/validation corpus and four context cases
  (missing anchor, single- and multi-line bodies, EOF body) are byte-identical
  to 0.166 (`out/r59-parse.txt`, `out/r59-context.txt`), and the ten
  write/delete documents keep 0 diffs (`out/r58-patch.txt`). Regressions:
  `TestApplyContextMismatchKeepsRustWording` in `applypatch`, plus the
  `Failed to find expected lines in ` prefixes pinned by
  `TestApplyRejectsMissingContext`,
  `TestApplyPreserveRejectsOverlappingEndOfFileChunks` and
  `TestApplyPatchExecutorInvalidMatrixDoesNotMutateWorkspace`.

### 73. `completion [SHELL]` parses like clap and `features list` sizes its columns

clap validates `completion`'s optional positional at parse time, so
`codex completion bogus` exits 2 with `error: invalid value 'bogus' for
'[SHELL]'`, the alphabetical `[possible values: bash, elvish, fish,
powershell, zsh]` line, clap's `tip: a similar value exists` hint (`bsh` ->
`bash`) and no usage block. Go accepted any string and only failed inside
the generator with `Error: unsupported completion shell bogus` (exit 1).
`validateCompletionShell` now runs in `parseCompletion` and reuses
`invalidValueError`.

`features list` printed fixed `%-38s`/`%-18s` columns, while Rust sizes both
from the longest entry in `FEATURES` (`name_width` 39 and `stage_width` 17
for the current table), so every row carried one extra pad. `runFeatures`
now derives the widths from `features.Sorted()` the same way.

- Evidence: `out/r61-fix.txt` - `completion bogus`, `completion bsh`,
  `completion BASH` and `completion bash extra` are byte-identical to 0.166
  in rc, stdout and stderr, and the 194 `features list` rows share the same
  name/stage columns. The remaining row delta is the seven features added
  upstream after the 0.166 branch (`apps_early_resource_discovery`,
  `code_mode_host_output_truncation`, `gcp_setup_wizard`,
  `incremental_exec_tools`, `interrupt_on_cancel`,
  `suppress_model_change_announcements`, `tui_extensions_statusline`), all
  present in `codex-rs/features/src/lib.rs` at `b0f977c3a9`.
- Regressions: `TestCompletionShellDiagnosticsLikeRust` in `cli` and
  `TestFeaturesListUsesDynamicColumnWidthsLikeRust` in `app`.
### 74. `debug models` prints the bundled catalog and the offline fallback serves it

Rust's `debug models` serialises `bundled_models_response()` - the embedded
`models-manager/models.json` catalog - so the output is the release's own
catalog in serde order (745,260 bytes for 0.166). Go marshalled a hand-written
10-model literal through its internal `ModelInfo`, which dropped
`gpt-daybreak-blue-latest` and `gpt-daybreak-red-latest`, flattened
`supported_reasoning_levels` to strings, added Go-only fields
(`base_instructions`, `supports_parallel_tool_calls`) and left absent Rust
fields such as `shell_type` in the wrong place.

- **Snapshot.** `model/bundled_models_catalog.json` holds the 0.166 output
  byte for byte; `.gitattributes` marks it `-text` so checkout does not
  rewrite its single trailing LF.
- **Print path.** `model.BundledModelsCatalogJSON()` exposes the snapshot and
  `runDebug` writes it to stdout unchanged, which is exactly what Rust's
  `serde_json::to_writer` produces.
- **Runtime fallback.** `BundledModelsResponse()` now parses the embedded
  catalog before the hand-written literal, so a build without a
  `models-manager/models.json` still reports all 12 models, their reasoning
  level ordering, service tier definitions and base instructions.
- Evidence: `out/r64-models.txt` - byte-identical to 0.166 from three
  different working directories (the verification worktree, the shared
  checkout and a scratch directory). `go test ./model` passes; `./appserver`
  keeps only the two pre-existing MCP-dependency failures and `./app` the two
  known baselines.
- Regressions: `TestBundledModelsCatalogMatchesRustSnapshot` in `model` and
  `TestDebugModelsPrintsBundledCatalogLikeRust` in `app`;
  `TestRuntimeRouterSkillsIncludeInstructionsFalseHidesCatalogButAllowsExplicitExecutorSkillLikeRust`
  now keys off the catalog entry text and locator instead of the `## Skills`
  heading, which the bundled base instructions legitimately carry.
### 75. `debug clear-memories`, `execpolicy` read failures and empty `plugin list --json`

`debug clear-memories` targeted a hand-written set of legacy files instead of
the runtime's memories databases, `execpolicy check` reported a missing rules
file as a single `failed to read policy at <p>: <go err>` line, and
`plugin list --json` marshalled two nil slices into `null`.

- **clear-memories.** Rust's `run_debug_clear_memories_command` clears the rows
  of every memories database that already exists
  (`StateRuntime::clear_memory_data_in_sqlite_home` for `memories_1.sqlite` and
  `memories_v2_1.sqlite`), then empties the three memory roots
  (`memories`, `memories_v2`, `memories_extensions`) and prints
  `Cleared memory state from <memories_1.sqlite>. Cleared memory directories
  under <codex home>.` - or `No memories db found at <memories_1.sqlite>.`
  when no database was created. The database file itself is never removed. Go
  now resolves the SQLite home through the effective config (managed config
  included) and prints the same two sentences. `state.ClearMemoryDataInSqliteHome`
  reuses the extracted `clearMemoryDataInPool`, and
  `memories.ClearRootsContents` owns the directory cleanup.
- **execpolicy.** `LoadPolicies` now renders anyhow's context chain -
  `failed to read policy at <p>`, a blank line, `Caused by:` and the cause
  indented by four spaces - and rewrites `*os.PathError`/`syscall.Errno`
  causes the way `std::io::Error` displays them: on Windows the localized
  `FormatMessageW` text without its trailing CRLF plus ` (os error N)`, on
  other platforms the system message plus ` (os error N)`
  (`execpolicy/os_error_windows.go`, `execpolicy/os_error_other.go`).
- **plugin list --json.** Both lists start as empty slices, so an install-free
  home prints `"installed": []` and `"available": []` like Rust's `Vec::new()`.
- Evidence: `out/r66-verify.txt` - `debug clear-memories` on an empty home and
  on a home with a database plus memory roots, `execpolicy check` with a
  missing rules file and `plugin list --json` all match 0.166 in rc, stdout,
  stderr and the resulting home tree (4/4). `out/r66msg.txt` records the
  `Cleared memory state ...` line. `go test -vet=off ./execpolicy ./state
  ./app` keeps only the two known `./app` baselines.
- Regressions: `TestDebugClearMemories` in `app` (both sentences, the
  database file surviving, the roots emptied),
  `TestLoadPoliciesMissingFileMatchesRustContextChain` in `execpolicy` and
  `TestPluginListJSONEmptyHomeMatchesRustEmptyArrays` in `app`.
### 76. Hidden `debug` help pages, group subcommand resolution and marketplace listing/removal

Three surfaces still diverged: the hidden `debug` subcommands had no help
pages, a group command answered an unknown child followed by `--help` with its
own help page, and the plugin marketplace commands reported a marketplace that
does not exist.

- **Hidden help pages.** Rust ships `debug clear-memories` and `debug
  trace-reduce` behind `#[clap(hide = true)]`; both still render full clap
  pages for `--help` and `-h`. Go printed the `debug` group page for either
  spelling. `subcommandHelpPages`/`subcommandShortHelpPages` now carry both
  pages verbatim, together with their `Usage:` blocks and flag tables.
- **Group resolution before the help action.** clap resolves a group's child
  before running the help action, so `codex debug config --help`, `codex mcp
  nosuch --help`, `codex plugin nosuch --help`, `codex features nosuch
  --help` and `codex app-server nosuch --help` all answer
  `error: unrecognized subcommand '<name>'` with the group usage and rc 2. Go
  printed the group help with rc 0. `SubcommandHelpRequestFor` now stops at a
  bare word under any group listed in `subcommandCandidates`, letting the
  normal parse produce clap's diagnostic. Commands that take positionals
  (`exec`, `resume`, `mcp get`) keep printing their own help page.
- **`debug config` removed.** Go carried a hidden `debug config` subcommand
  that dumped the effective config with origins and layers; neither 0.166 nor
  current upstream has such a subcommand (their `DebugSubcommand` enum lists
  only models, app-server, prompt-input, trace-reduce and clear-memories), so
  it is gone and `codex debug config` is now clap's unrecognized-subcommand
  error. The `/debug-config` TUI renderer is unaffected.
- **Marketplace listing.** `plugin marketplace list` printed one row with an
  empty name and root, because the plugin service emits a synthetic unnamed
  entry for unattributed plugins. Rust lists only configured and installed
  marketplaces, so the command filters unnamed entries: an empty home now
  prints `No plugin marketplaces in scope.` and `{"marketplaces": []}`.
- **Marketplace removal.** `plugin marketplace remove nosuch` answered
  `Removed marketplace `nosuch`.` with rc 0. Rust removes the user config
  entry and the cloned `marketplace_install_root/<name>`, and fails with
  `marketplace `nosuch` is not configured or installed` when neither existed.
  `PluginService.RemoveMarketplace` now tracks both removals, deletes the
  clone for Git marketplaces (never a local root the user owns) and raises the
  same invalid-request error, which the CLI prints bare like Rust.
- Evidence: `out/r67-sweep.txt` - 24 command surfaces, 22 byte-identical to
  0.166 (rc and stdout/stderr); the only remaining diffs are the
  `completion powershell` and `completion fish` scripts. `go test -vet=off
  ./cli ./plugin` passes; `./app` and `./appserver` keep only their known
  baselines.
- Regressions: `TestHiddenDebugHelpPagesLikeRust` and
  `TestGroupUnknownChildWithHelpErrorsLikeRust` in `cli`,
  `TestPluginMarketplaceListSkipsUnnamedEntriesLikeRust` and
  `TestPluginMarketplaceRemoveUnknownErrorsLikeRust` in `app`, and
  `TestRemoveMarketplaceUnknownMatchesRust` /
  `TestRemoveMarketplaceDeletesGitCloneLikeRust` in `plugin`.

### 77. `codex apply <TASK_ID>` fetches a task diff (plus features wording, login-status logging and MCP OAuth classification)

`codex apply` took a patch body from stdin or a shell argument and ran the
Go-only patch applier. Rust's `apply` takes a TASK_ID, resolves the task from the
ChatGPT backend and applies the turn's `output_diff` with `git apply --3way`, so
the command now follows that path and the Go-only one is gone.

- **`apply` semantics.** The positional is `TASK_ID`; a second positional is
  clap's `error: unexpected argument '<arg>' found` with
  `Usage: codex apply [OPTIONS] <TASK_ID>` and rc 2. The command loads the
  effective config with managed layers, resolves ChatGPT auth
  (`Error: ChatGPT auth not available` and rc 1 when absent), requires the
  chatgpt backend mode and a `ChatGPT-Account-ID` header
  (`ChatGPT account ID not available, please re-run `codex login``), reads
  `chatgpt_base_url` or the production default, validates it, then `GET`s
  `/wham/tasks/<task-id>`. A missing diff turn prints `No diff turn found`, a
  turn without the PR output item prints `No PR output item found`, a failed
  apply prints Rust's `Git apply failed (applied=N, skipped=N, conflicts=N)`
  block with stdout/stderr, and success prints `Successfully applied diff`.
- **New `gitutils` package.** `ApplyPatch` writes the diff to a temporary file
  and runs `git apply --3way` (pre-staging reverted paths), `ResolveGitRoot`
  reports `not a git repository`, and `ParseGitApplyOutput` is now a faithful
  port of `codex-git-utils`: captured paths are unquoted and C-unescaped, 4xx
  style `error: patch failed` lines are *skipped* rather than conflicted, a
  three-way merge failure is attributed to the last `Checking patch` path, and
  the final precedence is conflicts > applied > skipped.
- **New `chatgptapi.GetTask`.** `GET /wham/tasks/<id>` reuses the cloud client
  and decodes `output_diff.diff` plus the turn status the apply path needs.
- **`features enable/disable`.** Both now print Rust's
  `Enabled feature `<name>` in config.toml.` / ``Disabled feature `<name>` in
  config.toml.`` on stdout *before* the under-development warning on stderr.
- **`login status` logging.** `codex login status` no longer installs the login
  file logger, so a status check leaves no `log/codex-login.log`; it still
  prints `Not logged in` on stderr with rc 1 when no credentials exist.
- **arg0 helper guard.** The production entry point now refuses to create PATH
  aliases when `CODEX_HOME` sits under the system temp directory, printing
  Rust's `WARNING: proceeding, even though we could not create PATH aliases:
  Refusing to create helper binaries under temporary dir "<temp>" (codex_home:
  AbsolutePathBuf("<home>"))` and creating nothing; previously Go left an empty
  `tmp/arg0-windows` directory behind.
- **`mcp add --url` OAuth classification.** The initial probe's status decides
  between `McpOAuthLoginSupport::Unsupported` and `::Unknown`: 4xx answers stay
  silent, while 5xx/408/425/429 become discovery errors and print
  `MCP server may or may not require login. Run `codex mcp login <name>` to
  login.` Go treated every non-challenge response as "unsupported".
- Evidence: `out/r68-sweep.txt` compares rc, stdout/stderr and the whole home
  tree for 8 surfaces against 0.166 (5 identical); `out/r68-probe.txt` shows both
  binaries agreeing on 6 probe statuses (200/401/404/405 silent, 500/503 hint).
  `go test -vet=off ./cli ./gitutils ./chatgptapi ./mcp ./plugin ./state`
  passes, and `./app` keeps only its two known baselines.
- Regressions: `TestApplyRequiresTaskIDLikeRust`,
  `TestParseApplyRejectsExtraArgumentsLikeRust`, `TestApplyWithoutAuthMatchesRust`,
  `TestFeaturesEnableDisablePrintRustMessages`, `TestLoginStatusLeavesNoLogFile`,
  `TestApplyPatchAppliesDiffInRepository` / `TestParseGitApplyOutputLikeRust` in
  `gitutils`, `TestGetTaskFetchesOutputDiff` in `chatgptapi`,
  `TestValidateArg0AliasHomeRefusesTempHomeLikeRust` in `cli`, and
  `TestDiscoverStreamableHTTPOAuthClassifiesProbeStatusLikeRust` in `mcp`.

### 78. `login --with-access-token` decodes and verifies the agent identity JWT

`codex login --with-access-token` stored whatever came from stdin: a
non-JWT string, an unsigned JWT and a JWT signed by a foreign key all became
`auth.json`. Rust classifies the token, decodes the payload, checks the
workspace allowlist and then verifies the signature against the agent identity
JWKS before saving, so the command now does the same.

- **Claim decoding.** `agent.DecodeAgentIdentityJWT` now enforces codex's
  `AgentIdentityJwtClaims`: `iss`, `aud`, `iat`, `exp`, `agent_runtime_id`,
  `agent_private_key`, `account_id`, `chatgpt_user_id`, `plan_type` and
  `chatgpt_account_is_fedramp` are all required, `iat`/`exp` must be
  non-negative numbers and only `email` is optional. A missing or mistyped field
  reports serde's `agent identity JWT payload is not valid JSON`, an undecodable
  payload reports `agent identity JWT payload is not valid base64url`, and a
  token without three non-empty segments reports `invalid agent identity JWT
  format`. The decoding cause stays available through `errors.Is`/`As` while
  `Error()` prints Rust's exact text.
- **Signature verification.** After the workspace check, the login path
  downloads the agent identity JWKS from `chatgpt_base_url` (or
  `CODEX_AGENT_IDENTITY_JWKS_BASE_URL`) with the config's proxy/policy client
  and re-decodes the token against it, mirroring
  `verified_record_from_jwt`; a token whose `kid` is unknown now fails with
  `agent identity JWT kid <kid> is not trusted` instead of being persisted.
  JWKS failures print Rust's `failed to request agent identity JWKS` /
  `agent identity JWKS endpoint returned an error` /
  `failed to decode agent identity JWKS` without the wrapped Go detail.
- **Retired `--api-key` flag.** Rust rejects the flag before any login flow
  starts, so the per-flow file logger never runs. Go installed
  `log/codex-login.log` first; the validation now precedes the install, which
  leaves an empty home behind exactly like Rust.
- Evidence: `out/r69-jwt.txt` compares four token shapes (full claims, missing
  `iss`, string `iat`, null `email`) plus three malformed tokens against 0.166 -
  rc, message and resulting home tree are identical; `out/r69-sweep.txt` covers
  18 command surfaces (15 identical, the other three being the version stamp,
  the upstream-only `auth.json.lock` sidecar and the completion scripts).
  `go test -vet=off ./agent ./auth ./app` passes except for the two known
  `./app` baselines.
- Regressions: `TestDecodeJWTPayloadRequiresEveryClaimLikeRust` in `agent`,
  `TestLoginWithAccessTokenRejectsMalformedJWTLikeRust` and
  `TestLoginWithAccessTokenVerifiesSignatureLikeRust` in `app`.

### 79. `completion <shell>` emits clap_complete's scripts byte-for-byte

Go generated all five completion scripts from a hand-written 76-node command
tree, so the output carried none of clap_complete's shape: 0.166 walks a
264-node model (including the hidden `execpolicy`, `responses-api-proxy`,
`stdio-to-uds`, `migrate-nfs-db` and `queue` subcommands) and prints
6968/4508/1040/2194/1914 lines of bash/zsh/fish/powershell/elvish against Go's
170/7/164/172/3. Porting clap_complete's five generators plus the complete clap
command model would be a port of clap, not a parity fix, so the scripts are now
shipped as data - the approach the repo already takes for
`model/bundled_models_catalog.json`.

- **Embedded assets.** `cli/completiondata/{bash,zsh,fish,powershell,elvish}.txt`
  hold the exact bytes 0.166 prints (242813/246017/198104/272564/167227 bytes,
  LF). `cli/completion_assets.go` serves them through `//go:embed` and
  `completionScript`, whose doc comment names the producing command and how to
  regenerate the files.
- **Fallback stays.** `GenerateCompletion` consults the assets first and only
  then reaches the hand-written generator, so `codex completion <shell>` is
  byte-identical while a renamed binary (`gcode`) still gets a script defining
  `_gcode` rather than `_codex`.
- **Hidden subcommands.** clap_complete walks the whole command model, so the
  scripts list `migrate-nfs-db`, `execpolicy`, `marketplace`, `daemon`,
  `bootstrap` and `remote-control`; the regression test used to assert they were
  absent and now asserts they are present.
- **Line endings.** `.gitattributes` pins `cli/completiondata/*.txt` to `-text`
  so a Windows checkout cannot rewrite the LF bytes.
- Evidence: `out/r70-completion.txt` compares against the 0.166 binary and all
  five scripts are identical; `out/r70-p67-sweep.txt` reports `cases 24 diffs 0`
  where it previously had two (`completion powershell`, `completion fish`).
  `go test -count=1 -vet=off ./cli ./plugin ./state` passes and `./app` keeps
  only the two known baselines.
- Regressions: `TestGenerateCompletionAllShellsIncludeNestedSurface` (hidden
  subcommands present, per-shell vocabulary) and
  `TestGenerateCompletionServesReferenceScript` (assets served, no CRLF, `tcsh`
  uncovered, `gcode` falls back).

### 80. `execpolicy check` renders starlark's diagnostics, not a Go one-liner

`execpolicy check` answered every bad rules file with one Go sentence
(`unsupported decision "maybe"`, `expected 2 arguments`, ...). 0.166 parses the
same file inside `starlark` and prints the runtime's own report: an anyhow
chain (`failed to parse policy at <p>` / `Caused by:` / `starlark error: ...`),
then, when the failure came from evaluation, a
`Traceback (most recent call last):` frame with the failing source line, then
ariadne's code frame (`--> p:l:c`, `N | <line>`, `^^^^` under the span, and the
multi-line `1 | / ...` / `| |_^` form for a call that spans lines). Go also
silently accepted files with trailing junk - `prefix_rule(...)` followed by a
stray `)` was treated as valid.

- **Front end.** `execpolicy/policy_diagnostics.go` adds `lexPolicySource`
  (identifier / string / number / punct / newline / EOF) and
  `parsePolicyProgram`, which splits statements, recognises `X = expr` and
  `name(...)` calls, balances brackets and rejects a leftover token with
  starlark's `Parse error: unexpected symbol 'X', expected new line`. Every
  call argument keeps its name, value text and source span so the renderer can
  point at the exact bytes Rust underlines.
- **Renderer.** `policyDiagnostic.Error()` emits the three shapes 0.166
  produces: analysis/parse errors get the single-line frame, evaluation errors
  get the traceback prefix plus the frame of the whole call span, and a span
  covering several lines switches to ariadne's multi-line gutter. An empty span
  (end of file) renders as `--> p:1:1` followed by two empty gutter lines.
- **Binder.** `execpolicy/policy_builtin_signatures.go` holds the
  `prefix_rule` / `network_rule` / `host_executable` signatures and the
  compile- and run-time checks starlark makes: `repeated named argument`,
  `positional argument after non positional`, `Missing parameter <name> for
  call to <fn>`, `Found N extra positional argument(s) for call to <fn>`,
  `Found <a> <b> extra named parameter(s) for call to <fn>`, `Argument <name>
  occurs more than once`, and the type mismatch `Type of parameter <name>
  doesn't match, expected <type>, actual <type (repr: value)>` (for
  `decision=1`: `expected str, actual int (repr: 1)`).
- **Scope check.** The same file resolves identifiers before evaluation:
  `Variable <name> not found, did you mean <suggestion>?` uses starlark's own
  spelling suggestion (distance 1 for names of two characters or less, 2
  otherwise, candidates = globals + locals + builtins, lowest distance wins
  after sorting), and a name assigned later in the file reports
  `Local variable <NAME> referenced before assignment` instead of evaluating
  to an empty string.
- **Wording.** `execpolicy/execpolicy.go` now matches the rule builder's
  messages: `invalid decision: <v>`, `invalid pattern element: ...`,
  `invalid example: ...`, `invalid rule: ...`, `host_executable paths must be
  strings (got <type>)`, and the non-type-matched field errors carry
  `policyTypeError` so the binder can name the starlark type.
- Evidence: `out/r73-execpolicy.txt` (33 cases, 33 identical to 0.166),
  `out/r73-suggest.txt` (36 suggestion shapes, 0 diffs), `out/r73-edge.txt`
  (11 edge cases, 0 diffs) and `out/r73-rust-expectations.json` (the captured
  stderr the regression table was generated from). `go test -count=1 -vet=off
  ./execpolicy` passes, including `TestPolicyDiagnosticsMatchRust166`.

### 81. `sandbox --sandbox-state-json` decodes like serde and before the overrides

`--sandbox-state-json` carries `codex_mcp::SandboxState`, which 0.166 decodes
with serde_json at the top of `run_command_under_sandbox` - before it parses
`-c` overrides, loads config or even looks at the command. Go parsed the value
much later, through `encoding/json`, and wrapped every failure as
`invalid sandbox run request: ...`; so `codex sandbox --config= --sandbox-state-json={}`
reported the override, `--sandbox-state-json=` reported `sandbox requires
COMMAND`, and a value 0.166 rejects with a caret-less serde message came back as
`json: cannot unmarshal ...`.

- **Ordering.** `app.validateSandboxStateJSONBeforeConfig` runs between
  `cli.Parse` and `validateCommandOverrides`, so a malformed state wins over a
  malformed override and over the missing-command check, exactly as upstream.
  `cli.SandboxOptions` gained `SandboxStateJSONProvided` because
  `--sandbox-state-json=` is an empty payload serde still decodes (it reports
  `EOF while parsing a value`), not an absent flag.
- **serde diagnostics.** `cli/sandbox_state_json.go` reuses the serde-compatible
  scanner behind `--startup-client-info` (whose scalar helper is now
  `clapJSONScalarDetail`) and reproduces the five shapes `SandboxState` can
  produce: syntax errors (`expected ident`, `EOF while parsing an object`,
  `trailing characters` with line/column), `invalid type: integer <n>, expected
  a string`, `duplicate field <name>`, `missing field <name>` (declaration
  order, reported at the closing brace) and the untagged-enum
  `data did not match any variant of untagged enum PermissionProfileDe`, whose
  position is the end of the buffered value or the bracket that follows it.
  Required fields are `permissionProfile` and `sandboxCwd`; the launcher path is
  an optional string, the two booleans default to false and unknown fields are
  ignored. A positional sequence is accepted when it carries the three required
  values, and a sixth element is `trailing characters`.
- **PathUri.** `sandboxCwd` is a `file:` URI upstream, not a native path, so
  `cli` now parses the scheme (`unsupported path URI scheme <name>` for a
  Windows path spelled as a URI, `invalid URI: relative URL without a base` for
  `/home/x`), normalises `file:tmp` to `file:///tmp`, decodes drive and UNC
  authorities and rewrites the payload with the native path before handing it to
  the runner.
  A URI describing another platform's convention reproduces anyhow's chain:
  `sandbox state cwd is not native to this host` / `Caused by:` /
  `'file:///home/x' is invalid on 'windows'`.
- **Effect.** `codex sandbox --sandbox-state-json=...` now accepts the payload
  0.166's MCP launcher actually forwards (`file:///C:/...`); Go previously
  rejected every one of them as not native.
- Evidence: 49 payloads compared against the 0.166 binary
  (`out/r74-state.txt`, `out/r74-syntax.txt`); 40 are byte-identical and the 9
  remaining ones share one cause (below). The regression table is
  `cli/sandbox_state_json_test.go`, generated from `out/r74-rust-expectations.json`.

### 82. `migrate-rollouts` replays legacy records into paginated history like Rust

Legacy migration is a record replay, not a session-item projection: 0.166's
`LegacyRolloutCanonicalizer` re-emits every surviving record with the next
stable ordinal, converts obsolete completion events into canonical turn items,
and only then publishes the staged file. Go instead rebuilt the rollout from
`sessionItemsFromRolloutLines`, which lost content and invented turn boundaries
for exactly the rollouts migration exists to fix: a legacy `response_item`
holding a `function_call`, `function_call_output` or `reasoning` payload was
projected into a turn item, failed that conversion and disappeared; every
legacy event without a `turn_id` produced a synthetic `turn-1`/`turn-2`
`task_started` stamped with `time.Now()` and `started_at: null`; `token_count`
was dropped entirely; `session_meta` printed `session_id: ""` plus Go's field
order; timestamps lost their milliseconds; and `bytes_processed` was pinned to
`0`.

- **Head metadata.** `marshalCanonicalSessionMeta` writes Rust's `SessionMeta`
  field order with its skip/null rules: `session_id` equals `id` (falling back
  to the record's id, never an empty string), `model_provider` and
  `base_instructions` always serialize (as `null` when unset),
  `history_mode` becomes `paginated`, and `history_base` /
  `subagent_history_start_ordinal` are dropped exactly as
  `write_head_session_meta` does.
- **Record replay.** `rollout/canonical_replay.go` re-reads the source records
  (plain or `.jsonl.zst`), emits them in Rust's `RolloutLine` order
  (`timestamp`, `ordinal`, `type`, `payload`), keeps the source timestamp
  verbatim (milliseconds included), and passes response items and unknown
  events through untouched. Go-written legacy rollouts that stored canonical
  turn items inside `response_item` records (`imageGeneration`,
  `commandExecution`, ...) are converted into `item_completed` events so resume
  keeps them.
- **Legacy events.** `user_message`, `agent_message`, `agent_reasoning`,
  `agent_reasoning_raw_content` and `exec_command_end` become canonical
  `TurnItem`s with Rust's field order and synthesized `item-N` ids (reasoning
  accumulates into one item that is re-emitted, `exec_command_end` normalizes
  the cwd to a `file:///C:/...` PathUri). `token_count` is normalized to
  `{"type":"token_count","info":...,"rate_limits":...}`, and `task_started` /
  `task_complete` / `turn_aborted` re-serialize with the protocol defaults
  (`model_context_window: null`, `collaboration_mode_kind: "default"`).
- **Turn boundaries.** Records without a turn id adopt Rust's implicit turn
  `rollout-{source line index}` with `started_at`/`completed_at` taken from the
  record timestamp, and the implicit turn is closed before the next turn starts
  or at end of file.
- **Rollback.** `thread_rolled_back` is now a plan operation: instruction
  boundaries follow user messages, the turn-started records that introduce a
  turn are adopted by that turn's first message, and `num_turns` drops whole
  boundaries before any ordinal is assigned - so a rolled-back instruction and
  its `task_started` disappear while the surviving turn keeps its
  `turn-1`-style id.
- **Byte accounting.** `CanonicalizeRolloutWithStats` returns what Rust's rate
  limiter counts: the source bytes, the header record read twice, the staged
  rollout written once and projected once, plus the extra unplanned pass,
  rollback-plan pass and aborted staged writes Rust performs when a rollback
  marker forces a second conversion.
- Evidence: `out/r75-migrate-compare.txt` compares 8 legacy shapes
  (response items, legacy events, explicit turn + command execution, tool-call
  items, reasoning accumulation, aborted turn, two turns, rollback) against
  0.166 at 8/8 byte-identical with identical `bytes_processed`
  (2,648 / 3,896 / 3,992 / 2,769 / 3,477 / 2,207 / 7,051 / 8,353);
  `out/r75-rollback-bytes.txt` covers three rollback variants (2,436 / 3,680 /
  5,352, all identical); `out/r75-compressed.txt` shows a `.jsonl.zst` source
  migrating to identical decompressed content; `out/r75-migrate-probe.txt`
  keeps the original 4-record probe. `TestCanonicalizeRolloutReplaysResponseItemsAndNormalizesTokenCountLikeRust`,
  `TestCanonicalizeRolloutSynthesizesImplicitTurnsLikeRust`,
  `TestCanonicalizeRolloutConvertsCommandExecutionLikeRust`,
  `TestCanonicalizeRolloutAccumulatesReasoningLikeRust` and
  `TestCanonicalizeRolloutCountsRollbackConversionPassesLikeRust` freeze those
  bytes and counters in `rollout/canonical_migration_rust_shape_test.go`;
  `go test -count=1 ./rollout` passes, `./app` keeps only its two known
  baselines and `./appserver` only the two pre-existing `TestSkillMCPDependency*`
  failures.

### 83. `migrate-rollouts` accounts compressed sources and rewrites legacy event shapes

Round 69 made the replay byte-identical for plain rollouts; three record-shape
gaps remained, and all three came from mirroring the parts of 0.166's pipeline
that run *before* the canonicalizer sees a record: `parse_legacy_rollout_line`
decodes every legacy line into the modern `RolloutLine` and silently drops the
line when that fails (`.unwrap_or(None)`), and `write_item` then applies
`persisted_rollout_item` with `ThreadHistoryMode::Paginated`, which keeps a
small allow-list of events and drops everything transient.

- **Compressed byte accounting.** A `.jsonl.zst` source is charged as the
  compressed length plus *two* decompressed passes (Rust inflates it once to
  reach the header and once to stream the canonical rollout) before the same
  `header + 2 * staged_bytes` tail. `readCanonicalReplayLines` now counts the
  bytes `ReadLine` consumed before trimming the delimiter, so the total is the
  decompressed size on disk even for a final line without a trailing newline,
  and `CanonicalizeRolloutWithStats` doubles it for compressed sources.
- **Dropped records.** `replayEvent` keeps only the events the paginated policy
  persists or that Go already converts, so `exec_command_begin`,
  `exec_command_output_delta`, `mcp_tool_call_begin`, `plan_update`,
  `item_started`, `error`, `warning`, `turn_diff` and every other transient or
  retired event disappear like they do in 0.166. An `item_completed` whose item
  is missing a field 0.166's `TurnItem` cannot decode without (`id`, plus the
  variant's required fields) is dropped instead of being carried over, so
  legacy item shapes written by older builds no longer survive migration.
- **`patch_apply_end`.** A record that decodes into the modern
  `PatchApplyEndEvent` becomes a `FileChange` completed item
  (`{"type":"FileChange","id":...,"changes":...,"status":...}` plus the
  non-empty `stdout`/`stderr`), with the line timestamp as `completed_at_ms`;
  a legacy record without `status` (or any other required field) is dropped,
  exactly like Rust's failed decode.
- Evidence: `out/r76-events.txt` compares five shapes (transient event mix,
  valid patch, legacy patch, legacy item, modern item) against 0.166 at 5/5
  byte-identical with identical `bytes_processed` (5,003 / 3,176 / 2,450 /
  2,485 / 3,513); `out/r76-compressed-cases.txt` covers six `.jsonl.zst`
  fixtures (three payload sizes at zstd levels 3 and 19, plus two with a
  rollback marker) at 6/6 identical bytes and counters
  (4,163 / 4,163 / 5,111 / 16,956 / 9,503 / 9,505);
  `out/r76-zst-model.txt` is the model that predicted them
  (`bp = C + 2D + 2O + header`), and `out/r76-migrate-compare.txt` keeps the
  eight plain shapes green. `TestCanonicalizeRolloutDropsTransientAndRetiredLegacyEventsLikeRust`
  and `TestCanonicalizeRolloutAccountsCompressedSourceBytesLikeRust` in
  `rollout/canonical_migration_events_rust_shape_test.go` freeze the new bytes,
  counters and the compressed accounting rule; `go test -count=1 ./rollout`
  passes, `./app` keeps only its two known baselines and `./appserver` only the
  two pre-existing `TestSkillMCPDependency*` failures (plus
  `TestRuntimeRouterConfigServiceKeepsManagedConfigFreshLikeRust` under the full
  parallel run, which passes when run alone).

### 84. `migrate-rollouts` re-serializes `turn_context` and `compacted` like Rust

0.166 does not carry those two record types through untouched: every legacy line
is decoded into the modern `RolloutLine` and written back through
`serde_json`, so `turn_context` loses whatever `TurnContextItem` does not model,
`compacted` gains the two nullable trailer fields `CompactedItemWire` always
serializes, and a record the modern types cannot decode drops out of the
migrated file entirely (the parser is `unwrap_or(None)`).

- **`turn_context`.** The payload is rebuilt in `TurnContextItem` field order
  (`turn_id`, `root_turn_id`, `disabled_plugin_ids`, `cwd`, `approval_policy`,
  `approvals_reviewer`, `sandbox_policy`, `permission_profile`,
  `active_permission_profile`, `file_system_sandbox_policy`, `model`,
  `comp_hash`, `collaboration_mode`, `multi_agent_version`, `realtime_active`,
  `cyber_access_program`, `effort`, `summary`), unset optionals disappear and
  unknown keys are dropped. `approval_policy` keeps `untrusted` / `on-request` /
  `never`, rewrites the legacy alias `on-failure` to `on-request`, and keeps a
  tagged granular object. `sandbox_policy` maps the legacy `mode` key onto
  `type` and then re-serializes each variant exactly as `SandboxPolicy` does:
  `read-only` emits `network_access` only when true, `external-sandbox` always
  emits the `restricted`/`enabled` enum (a bare boolean fails the decode),
  `workspace-write` always emits `network_access`, `exclude_tmpdir_env_var` and
  `exclude_slash_tmp` and keeps `writable_roots` only when non-empty, and
  `danger-full-access` is a bare tag. `collaboration_mode` follows the same
  treatment, including `line_parser`'s legacy migration of `model` /
  `reasoning_effort` / `developer_instructions` into `settings` (whose optional
  fields always serialize, as `null` when unset).
- **`compacted`.** The payload is rebuilt in `CompactedItemWire` order and
  `compaction_response_id` plus `latest_token_usage_record` are always written
  (`null` when the source omits them); the other wire fields only appear when
  present.
- **Dropped records.** A `turn_context` without `cwd` / `model` /
  `approval_policy` / a decodable `sandbox_policy`, or with an unknown approval
  or sandbox spelling, and a `compacted` without a string `message`, are dropped
  like Rust's failed parse.
- Evidence: `out/r77-turn-context-compacted.txt` compares 20 payloads
  (`turn_context`: legacy `workspace-write`, `read-only` network flag,
  `danger-full-access`, both `external-sandbox` spellings, writable roots plus a
  junk key, the `on-failure` alias, optional fields, three undecodable records,
  two collaboration modes; `compacted`: five shapes) against 0.166 at 20/20
  byte-identical with identical `bytes_processed`; `out/r76-events.txt`,
  `out/r76-migrate-compare.txt` and `out/r77-turn-context-rust*.txt` keep the
  earlier probes green. `TestCanonicalizeRolloutReserializesTurnContextAndCompactedLikeRust`
  (10 subtests) freezes the Rust bytes and counters in
  `rollout/canonical_migration_turn_context_rust_shape_test.go`, and
  `go test -count=1 ./rollout` passes.

### 85. `migrate-rollouts` converts legacy completion events into turn items like Rust

0.166 turns the remaining legacy completion events into completed `TurnItem`
records instead of carrying them through verbatim. Go's replay now mirrors
`legacy_event.rs::completed_item` for `mcp_tool_call_end`, `web_search_end`,
`context_compacted`, `entered_review_mode`, `exited_review_mode`,
`sub_agent_activity` and `dynamic_tool_call_response`:

- **Turn selection.** `canonicalizer.rs` writes an item whose explicit
  `turn_id` differs from the active turn into that turn, reuses a turn id it has
  already opened without emitting another `task_started`, and opens an implicit
  `task_started`/`task_complete` pair named after the id the first time it sees
  it; `legacyEventTurnID` now does the same for converted events and for
  `patch_apply_end` / `exec_command_end`, and every path drops buffered
  reasoning first.
- **`mcp_tool_call_end`.** Rebuilt as `McpToolCall` with serde's camelCase
  optionals (`connectorId`, `mcpAppResourceUri`, `mcpAppUi`, `linkId`,
  `appName`, `actionName`, `pluginId`, `readOnlyHint`), `arguments` kept as the
  opaque JSON value, `result` re-serialized in `CallToolResult` order
  (`content`, `structuredContent`, `isError`, `_meta`), `status` taken from
  `is_success()` (`failed` for `Err` *and* for `Ok` with `isError: true`), and
  `duration` decoded through serde's strict `{ "secs", "nanos" }` visitor.
- **`web_search_end`.** Rebuilt as `WebSearch`; `action` follows the snake_case
  Responses enum (`search` with `query`/`queries`, `open_page`, `find_in_page`
  with `url`/`pattern`, unknown tags decoding to `{"type":"other"}`), and
  `results` stays opaque JSON.
- **`context_compacted` / review mode / sub-agent / dynamic tool.** Each becomes
  its completed item: `ContextCompaction` with a synthesized `item-N` id,
  `EnteredReviewMode` (the target is rebuilt from `ReviewTarget`, a legacy
  `prompt` is promoted to a `custom` target by `line_parser`, and the hint
  defaults to `Review requested.`), `ExitedReviewMode` (`review_output: null`
  when absent, otherwise re-serialized in `ReviewOutputEvent` order with
  findings, code location and line range rebuilt and f32 confidence scores
  printed the way `ryu` prints them), `SubAgentActivity` (validated `kind` enum,
  UUID `agent_thread_id` and `/root`-shaped `agent_path`, with `model` and
  `reasoning_effort` always written), and `DynamicToolCall` with camelCase
  `content_items` (`inputText`/`inputImage`/`inputAudio`) and the strict
  duration shape.
- **Dropped records.** Any converted event the modern types cannot decode is
  dropped the way Rust's failed parse is: a `mcp_tool_call_end` without
  `duration`/`invocation`/an `Ok`-or-`Err` result or with an unknown
  `mcp_app_ui` shape, a `web_search_end` without an action, an
  `exited_review_mode` whose review output misses a required field, a
  `sub_agent_activity` with an unknown `kind`, malformed thread id or relative
  agent path, and a `dynamic_tool_call_response` with an unknown content item
  tag.
- Evidence: `out/r78-convert.txt` compares 44 payloads against 0.166 at 44/44
  byte-identical with identical `bytes_processed` (`out/r78-probe2.txt`,
  `out/r78-probe3.txt` and `out/r78-float.txt` keep the probes behind the
  `mcp_app_ui`, duration, enum and f32 formatting rules), the `out/r75*`,
  `out/r76*` and `out/r77*` regressions still report `FAILURES 0`, and
  `TestCanonicalizeRolloutConvertsLegacyEventItemsLikeRust` (24 subtests) freezes
  the Rust bytes and counters in
  `rollout/canonical_migration_legacy_event_items_rust_shape_test.go`.

### 86. `migrate-rollouts` converts legacy image-generation events like Rust

`image_generation_end` was the last legacy completion event still carried over
verbatim; it also flattens its image reference into the event payload, so the
record has to be rebuilt field by field. Go now mirrors
`legacy_event.rs::completed_item` for it:

- **Envelope.** The item is `TurnItem::Extension(ExtensionItem::ImageGeneration)`,
  which serde serializes as `{"type":"Extension","kind":"image_gen.generation",
  ...}` with the item fields flattened after the tag.
- **Fields.** `ImageGenerationItem` order and nullability are reproduced:
  `id`, `status`, `revisedPrompt` (always present, `null` when unset),
  the flattened `GeneratedImageRef` (`fileId` wins over `result`, matching the
  untagged enum's `File`-first order), `transparentBackground` and `failure`
  (always present, `null` when unset), `savedPath` and `inputImages` only when
  set (`inputImages` also skipped when empty), and `generationId` (always
  present, `null` when unset). `failure` follows the tagged
  `usageLimitExceeded` shape with `limitId` required and `resetsAt` always
  written; `inputImages` entries keep only `path` and `sha256`.
- **Paths.** `saved_path` and `input_images[].path` are `AbsolutePathBuf`
  values, so they are validated as absolute and re-serialized lexically cleaned
  the way `std::path::absolute` cleans them on this platform (`C:/tmp/./a.png`
  and `C:/tmp/b/../a.png` both become `C:\tmp\a.png`, a trailing separator is
  dropped), and a root-relative or drive-relative path fails the decode.
- **Turn selection.** `completed_item` returns no turn id for this event, so the
  item always lands in the implicit `rollout-{line}` turn even when the payload
  carries a `turn_id`.
- **Dropped records.** A record without `status`, without either `fileId` or
  `result`, with an unknown failure tag or a non-absolute path is dropped like
  Rust's failed parse.
- Evidence: `out/r79-imagegen.txt` compares 19 payloads against 0.166 at 19/19
  byte-identical with identical `bytes_processed` (`out/r79-imagegen-rust.txt`
  and `out/r79-paths.txt` keep the shape and path-normalization probes), the
  `out/r75*`-`out/r78*` regressions still report `FAILURES 0`, and
  `TestCanonicalizeRolloutConvertsImageGenerationLikeRust` (12 subtests) freezes
  the Rust bytes and counters in
  `rollout/canonical_migration_image_generation_rust_shape_test.go`.

### 87. `migrate-rollouts` re-serializes the nested permission policies like Rust

`turn_context` carried `permission_profile`, `active_permission_profile` and
`file_system_sandbox_policy` through verbatim, so a record with legacy aliases
or unknown keys came out different from 0.166, which decodes those fields into
their protocol types and writes them back. Go now rebuilds them:

- **`permission_profile`.** Tagged `PermissionProfile` (`managed` / `disabled` /
  `external`). `managed` writes `file_system` then `network`; `file_system`
  follows the custom-serialized managed-permissions enum (`restricted` requires
  `entries` and writes them before an optional non-zero `glob_scan_max_depth`,
  `unrestricted` is a bare tag); `network` only accepts `restricted` /
  `enabled`; `external` writes `network`.
- **`active_permission_profile`.** `{id, extends?}` with the optional trailer
  skipped when unset.
- **`file_system_sandbox_policy`.** `RawFileSystemSandboxPolicy` order
  (`kind`, optional `glob_scan_max_depth`, `entries` only when non-empty) with
  the kebab-case `restricted` / `unrestricted` / `external-sandbox` kinds.
- **Entries.** `{path, access, missing_path_behavior?}` where `path` is the
  tagged `RawFileSystemPath` (`path` keeps the raw legacy string, `glob_pattern`
  keeps the pattern, `special` holds a `FileSystemSpecialPath` whose `kind` is
  `root` / `minimal` / `project_roots` / `tmpdir` / `slash_tmp` / `unknown` and
  whose legacy `current_working_directory` alias is rewritten to
  `project_roots`), and `access` rewrites the legacy `none` alias to `deny`.
- **Dropped records.** Unknown enum tags, an unknown access mode, a
  `restricted` managed filesystem without `entries`, a zero managed
  `glob_scan_max_depth`, an `active_permission_profile` without `id`, and an
  unknown `kind` on the filesystem policy drop the whole `turn_context`, the way
  Rust's failed decode does.
- Evidence: `out/r80-perms.txt` compares 22 payloads against 0.166 at 22/22
  byte-identical with identical `bytes_processed` (`out/r80-perms-rust.txt`
  keeps the shape probe), the `out/r75*`-`out/r79*` regressions still report
  `FAILURES 0` (including the earlier `turn_context` matrix in
  `out/r77-turn-context-compacted.txt`), and
  `TestCanonicalizeRolloutReserializesPermissionPoliciesLikeRust` (16 subtests)
  freezes the Rust bytes and counters in
  `rollout/canonical_migration_permission_policy_rust_shape_test.go`.

### 88. `migrate-rollouts` decodes and re-serializes every `item_completed` item like Rust

Go only checked that the nested item carried its required keys and then copied
the payload through, so items whose values 0.166 rejects survived the migration
and decodable items kept Go's original spelling. The record is now decoded into
`ItemCompletedEvent`/`TurnItem` and written back:

- **Envelope.** `thread_id` must parse as a UUID and is rewritten to the
  rollout's own thread id, so a payload carrying another thread id still lands
  on this thread; `turn_id` is required, `started_at_ms` is written between the
  item and `completed_at_ms` only when present, `completed_at_ms` defaults to
  `0`, and unknown payload fields are dropped.
- **AgentMessage.** `AgentMessageContent` keeps the Rust variant tag, so
  `{"type":"Text"}` is the only accepted spelling (a legacy `"text"` tag drops
  the line). `phase` accepts `commentary` / `partial_answer` / `final_answer`,
  `delivery` accepts `async`, `memory_citation` passes through, and `questions`
  entries are limited to `{title, options}` (unknown fields drop the line) with
  `options` always written; the declaration order (`phase`, `memory_citation`,
  `delivery`, `questions`) is preserved.
- **UserMessage.** `client_id` keeps its snake_case name (the camelCase
  spelling is not a serde alias and disappears), `content` entries keep
  `UserInput`'s snake_case tags including the untagged `image` inline/file
  forms, a `text` entry always writes `text_elements`, and every `TextElement`
  writes `byte_range` followed by `placeholder` (`null` when unset).
- **CommandExecution.** The analytics-only `sandbox_type` and `model_context`
  never serialize even when the record carries them, `cwd` must be a `file:`
  URI and is rewritten in canonical URL form (a native path drops the line),
  `parsed_cmd` entries follow their tagged shapes (`read` / `list_files` /
  `search` / `unknown`, with the optional trailers always written), `source` and
  `status` are validated against their enums, and `duration` keeps serde's
  strict `{secs, nanos}` visitor.
- **Remaining variants.** `FunctionCallOutput` (untagged text-or-content body),
  `HookPrompt`, `Plan`, `Reasoning` (default `raw_content`), `DynamicToolCall`
  (camelCase `inputText` content items), `CollabAgentToolCall` (UUID receivers,
  `agents_states` values including the externally tagged `completed` /
  `errored` forms), `SubAgentActivity` (always-written `model` /
  `reasoning_effort`, validated `agent_path`), `WebSearch` (unknown action tags
  collapse to `other`), `ImageView` (PathUri), the three `Extension` kinds
  (`image_gen.generation`, `clock.sleep`, `web.search`), `ImageGeneration`,
  `EnteredReviewMode` (all four `ReviewTarget` tags), `ExitedReviewMode`
  (always-written `review_output`), `FileChange`, `McpToolCall` (camelCase, with
  the existing `CallToolResult` / `McpAppUi` replays) and `ContextCompaction`.
- **Serialization details.** Strings are re-encoded the way `serde_json` writes
  them, so `a<b>&c` stays literal instead of Go's `\u003c` escaping.
- **Dropped records.** An unknown `type`, a non-UUID `thread_id`, a missing
  `turn_id` / `item`, an undecodable nested item (unknown tag or enum value,
  wrong JSON type, `file:` violation) and a non-integer timestamp all drop the
  whole line, the way Rust's failed decode does.
- Evidence: `out/r85-itemcompleted.txt` compares 69 payloads against 0.166 at
  69/69 byte-identical with identical `bytes_processed`
  (`out/r83-itemcompleted-rust.txt` and `out/r84-itemcompleted-rust2.txt` keep
  the shape probes), the `out/r75*`-`out/r80*` regressions still report
  `FAILURES 0`, and `TestCanonicalizeRolloutReplaysItemCompletedLikeRust`
  (15 subtests) freezes the Rust bytes and counters in
  `rollout/canonical_migration_item_completed_rust_shape_test.go`.

### 89. `migrate-rollouts` keeps the thread WAL open, decodes legacy exec records strictly and dates `task_complete` from the last written record

- **WAL lifetime.** Rust opens the thread-history database once per
  `migrate-rollouts` run and keeps it open until the command ends, so SQLite
  never checkpoints the WAL and the `Disk used for thread storage` delta
  includes it. Go opened and closed a connection per migrated rollout, and the
  close checkpoints the WAL, so it reported 89.4 KB where Rust reported
  335.1 KB. `app/app.go` now opens the projection connection once
  (`openMigratedRolloutProjection`), projects every migrated rollout through it
  (`projectMigratedRolloutsIn`), measures storage while it is still open and
  only then closes it. A run with nothing to migrate never opens the
  connection at all.
- **Strict legacy `exec_command_end`.** The record must decode as 0.166's
  `ExecCommandEndEvent` before it can become a `CommandExecution` item:
  required `call_id`/`turn_id`/`command`/`cwd`/`parsed_cmd`/`exit_code`/
  `duration`/`status`, the `source` and `status` enums, serde's `{secs, nanos}`
  duration visitor and the string-typed `plugin_id`/`script_path`/`process_id`
  options. A record that fails any of them is dropped whole, and the decode now
  runs before `legacyEventTurnID`, so a dropped record starts no implicit turn.
- **Legacy `cwd` normalization.** `line_parser::normalize_legacy_command_cwd`
  rewrites a `cwd` that does not already start with `file:` through
  `LegacyAppPathString` into a `file:` URI, and leaves a `file:` value for the
  `PathUri` decoder. `canonicalCommandCwdURI` replays both branches;
  `utils.Parse` alone rejected `C:\work` as an unknown URI scheme and dropped
  records Rust converts.
- **`task_complete` timestamp.** Rust advances the fallback `last_timestamp`
  only for records it actually writes, so a line that fails to decode, or a
  record the paginated policy drops, does not date the synthesized
  `task_complete`. `writeCanonicalReplay` compares `writer.bytesWritten` around
  each `replay` call and only then records the entry's timestamp.
- Evidence: `out/r87-wal.txt` (both report `482 B -> 335.1 KB`),
  `out/r89-storage-slope.txt` (0 B delta for 1/1, 1/5, 1/20, 2/1 and 4/1
  sessions), `out/r88-storage-report.txt` (1/3/6 sessions, `FAILURES 0`),
  `out/r90-storage-fixture.txt` (one rollout, 5 identical lines),
  `out/r91b-task-complete-ts.txt` (a delta tail, an exec record without
  `parsed_cmd` and an unknown event type all date `task_complete` the same),
  `out/r92-cwd.txt` (`file:` URI, drive path, relative path and POSIX path,
  `FAILURES 0`) and `out/r92-p75diff.txt`-`out/r92-p85diff.txt` (`FAILURES 0`).

### 90. `exec-server forward` registers a remote environment and bridges Noise relay streams

- **Scope-local `requires`.** Rust's exec-server parser declares `--remote` and
  `--environment-id` as globals, but clap validates a subcommand's `requires`
  against that subcommand's own matches. `--remote X forward --connect Y`
  therefore reports both `--remote <URL>` and `--environment-id <ID>` missing,
  while `--environment-id E forward --remote X --connect Y` reports only
  `--environment-id <ID>`. The forward usage block is
  `--remote <URL> --environment-id <ID> --connect <URL>`, followed by
  `--aws-sigv4` when the AWS family or `--remote-transport direct` asks for it
  and then the options argv actually carried. `validateExecServerClapScopes`
  replays the duplicate, conflict and requirement passes per scope, deepest
  first, so forward diagnostics win over root ones and
  `--remote ... forward --remote ...` is not a duplicate.
- **Conflicts and duplicates are per scope.** `--listen` conflicts with
  `--remote` only inside the same scope (so `--listen stdio forward --remote
  ...` registers like Rust), `--use-agent-identity-auth` and
  `--executor-credential-stdin` conflict with `--aws-sigv4`, and a single-value
  option may appear once before and once after `forward`;
  `clapRepeatedValueOptionError` resets at the subcommand token. `--connect`
  belongs to `forward`, so the root command reports it as unexpected, and
  `--listen`/`--concurrent-requests` after `forward` are unexpected arguments.
- **Forwarding bridge.** `RemoteEnvironmentConfig.ForwardURL` carries the
  `--connect` destination into `RunRemoteEnvironment`, which rejects the Direct
  transport (`direct exec-server transport does not support forwarding`) and
  then bridges every accepted Noise relay stream through
  `serveForwardedStream`: one WebSocket dial per stream, relay JSON lines sent
  as binary frames, and every text/binary frame framed back to the relay
  unchanged, matching `codex-rs/exec-server/src/forward.rs`.
- **Removed Go-only rejections:** `exec-server forward cannot be combined with
  --remote`, `--listen conflicts with exec-server forward`, the bespoke
  `--environment-id is required when --remote is set`,
  `--remote-transport requires --remote` and `--aws-sigv4 requires --remote`
  messages, and the `--connect ws://` scheme check clap never performs.
- Evidence: `out/r96j-diff.txt` (`TOTAL 75 FAILURES 0`), `out/r96b-rust.txt`
  and `out/r96c-diff.txt` (probes recorded against 0.166 first),
  `TestExecServerForwardScopeDiagnosticsLikeRust` (31 pinned diagnostics) and
  `TestServeForwardedStreamBridgesPayloadsLikeRust`.

### 91. `--aws-sigv4` reports the Rust SDK's region and credential-provider errors

- **Error order and wording.** Rust's `load_sdk_config` only rejects an empty
  service; `AwsAuthContext::load` then resolves the region and only afterwards
  takes the credentials provider, so a machine with neither reports
  `AWS SDK config did not resolve a region`. `auth.ErrAWSAuthMissingRegion` and
  the new `ErrAWSAuthMissingCredentialsProvider` now carry Rust's exact text.
- **No eager fetch and no IMDS.** The pinned Rust aws-config build never enables
  the EC2 instance metadata provider, so a context without credentials loads and
  only signing fails with `failed to load AWS credentials: the credential provider
  was not enabled`; Go used to probe IMDS during the load and answer
  `AWS credentials are required: failed to refresh cached credentials, no EC2 IMDS
  role found ...`. `loadAWSSDKConfig` disables IMDS,
  `defaultCredentialsProvider` substitutes the aws-config identity when no
  environment, container, web-identity or profile source is configured, and
  `SignAt` wraps fetch failures with the new `ErrAWSAuthCredentials`.
- **`--aws-profile` is profile-only and tolerates unknown names.** Rust routes an
  explicit profile through `load_profile` -> `ProfileFileCredentialsProvider`: the
  profile supplies region and credentials, environment keys are ignored, and an
  unknown profile leaves the environment region in place and fails only at
  signing. `profileCredentialsProvider` reproduces that (static keys and
  `credential_process` resolve locally, role/SSO/web identity keep the SDK chain).
- **Build versus transient.** `app.execServerSigV4Signer` now wraps every
  non-retryable signing failure as `codexapi.ErrAuthBuild`, so the CLI prints
  `environment registry authentication error: failed to resolve environment
  registry authentication: request auth build error: failed to load AWS
  credentials: the credential provider was not enabled`; `auth.IsAWSAuthRetryable`
  keeps only provider timeouts transient.
- Evidence: `out/r97aws-probe-after.txt` (eight region/credential/IMDS/environment
  combinations, byte-identical to 0.166) and `out/r97aws-probe2-after.txt`
  (profile plus environment keys, unknown profile), `out/r97aws-sig.txt` and
  `out/r97aws-sig-region.txt` (real SigV4 requests against a loopback registry:
  the profile key beats environment keys and the `us-east-1`/`eu-west-1`/
  `ap-south-1` credential scopes match), `out/r97aws-diff.txt` (`TOTAL 9
  FAILURES 0`, exit code and output byte-identical for those cases),
  `TestAWSAuthSDKErrorMessageLikeRust` and
  `TestLoadAWSAuthContext*LikeRust`.

### 92. `doctor` reports the Rust Environment identity, chrome and group order

- **OS identity and language.** Rust's `system_check` renders `os_info::get()`
  plus `sys_locale::get_locale()`, so 0.166 prints
  `os: Windows 10.0.26200 (Windows 11 CoreCountrySpecific) [64-bit]`,
  `os version: 10.0.26200` and `os language: zh-CN`, while Go printed
  `os: windows`, `os version: unknown` and derived the language from
  `LC_ALL`/`LC_CTYPE`/`LANG` (so `LANG=C.UTF-8` became `C`).
  `doctorOSIdentity` reads `RtlGetVersion` and the CurrentVersion
  `EditionID`/`ProductName` value (with os_info's `Windows 11` prefix at build
  22000+, the workstation edition fallback and the pointer-width bitness), and
  `doctorPreferredLocale` asks `GetUserPreferredUILanguages(MUI_LANGUAGE_NAME)`
  before falling back to the POSIX variables.
- **Detail set and rows.** `systemCheckFromInputs` emits Rust's `os`, `os type`,
  `os version`, `os language`, `LC_ALL`/`LC_CTYPE`/`LANG`,
  `VISUAL`/`EDITOR` (always present, `not set` when empty) and
  `PAGER`/`GIT_PAGER`/`GH_PAGER`/`LESS` in that order; the Go-only `arch`, `go`,
  `TERM`, `SHELL`, `ComSpec` and `WT_SESSION` rows are gone. The human renderer
  consumes `os type`/`os version` without printing them and relabels
  `os language` as `OS language`, matching `output/detail.rs::system_details`.
- **Groups and chrome.** `humanGroups` mirrors Rust `GROUPS`: Environment starts
  `system, disk, security, runtime, install, ...`, the new Desktop App group owns
  `desktop`, rows follow the group's category order and categories missing from
  every group are hidden instead of landing in a Go-only `Other` group. Status
  chrome matches 0.166 as well: `✓ ↑ ⚠ ✗ ○` (ASCII `[ok] [up] [!!] [XX] [--]`),
  `▸` detail and `→` remedy markers, a 61-character `─` separator, `·` in the
  header, terminal title and summary line, a blank line before the footer hints,
  `terminal_summary` joining with `·` even under `--ascii`, and git rows falling
  back to `check.summary` because Rust's `display_summary` has no git arm.
- **Report order.** Rust pushes the Windows dev-drive check before the git
  executable check; `insertDoctorCheckBeforeCategory` puts it in that slot so the
  two `git` rows render in 0.166's order.
- Evidence: `out/r1296-doctor-diff.txt` (normalized Environment diff against the
  0.166 binary), `out/r99-doctor-rust.txt` and `out/r99-doctor-go.txt` (full
  reports), `out/r99-env-rust.txt`/`out/r99-env-go.txt`,
  `TestSystemCheckDetailsMatchRustLikeRust`,
  `TestSystemCheckOmitsUnsetLocaleAndPagerEntriesLikeRust`,
  `TestRenderHumanSystemRowsMatchRustLikeRust`,
  `TestRenderHumanGroupsFollowRustGroupOrderLikeRust` and
  `TestRenderHumanUnicodeMarkersMatchRustLikeRust`.

### 93. `doctor` groups install, git, runtime and terminal rows like 0.166

- **install_details.** Rust collapses the package-manager flags into a single
  `managed by npm: no · bun: no · pnpm: no · Vite+: no · package root —` row,
  labels the context row `context`, hides `current executable` (the runtime row
  owns it) and prints `PATH entries` right after. `writeInstallDetails` and
  `writeManagedByRow` reproduce that, including Rust's `is_falsy` em dash and
  `yes`/`no` wording.
- **git_details.** Rust orders `selected git`, `version`, `exec path`,
  `repo detected`, `repo root`, `.git entry`, `branch`, `core.fsmonitor` and
  only then the `PATH entries` block. `writeGitDetails` does the same, so a git
  check no longer prints its PATH list next to the PATH summary.
- **runtime and title labels.** `runtime_details` consumes `platform` (the
  header prints it) and displays `current executable` as `executable`;
  `title_details` displays `title source`, `title items`, `activity item`,
  `project source` and `project value`. Both now match 0.166.
- **Detail values keep their middle dots.** Database rows
  (`state DB ... · integrity ok`), rollout rows (`1,515 files · 2.52 GB
  (avg 1.70 MB)`) and the rollouts note (`active files · on disk`) now carry
  Rust's `·` in the value instead of Go's `|`.
- **Issue metadata.** Rust's `attach_issue_metadata` only attaches the expected
  value; Go replaced the row value with the issue measurement, so a `TERM: dumb`
  detail rendered as `TERM: TERM=dumb`. Issue rows now print
  `dumb (expected TERM=xterm-256color ...)` like 0.166.
- **Terminal detection.** `std::io::IsTerminal` asks `GetConsoleMode`, while Go
  used `os.ModeCharDevice`, so a NUL stdin counted as a terminal, escalated the
  `TERM=dumb` warning to a failure and printed the interactive wording. The size
  probe now opens `CONOUT$` the way `crossterm::terminal::size` does, so a
  redirected stdout still reports `terminal size: 120x30`, `stdin is terminal:
  false` and the `... in this non-interactive run` summary.
- Evidence: `out/r1297-doctor-diff.txt` (Environment diff against the 0.166
  binary: only the `GCODE_HOME` label, the package layout/version, the search
  provider, the default title items and the extra state databases remain),
  `out/r99-doctor-rust.txt`/`out/r99-doctor-go.txt`,
  `TestRenderHumanDetailsAttachIssueMetadataLikeRust` and
  `TestTerminalCheckDumbDetails`.

### 94. `doctor` probes every runtime database, the PATH helpers and the doctor title defaults

- **Runtime database rows.** Rust `runtime_db_paths` returns all seven `RUNTIME_DBS`
  entries, so `codex doctor` also lists `memories v2 DB`, `queue DB` and `thread
  history DB`. `runtimeDBPathsForDoctor` probes them from the `state` filename
  constants and `state.RuntimeDBPaths()` reports the same seven in declaration
  order.
- **State row order.** Rust `state_details` re-emits `CODEX_HOME`, `log dir` and
  `sqlite home`, folds integrity into the four managed database rows, prints the
  rollout rows next and only then drains the remaining details. `writeStateDetails`
  reproduces that order, so the lazily-created databases and their
  `... integrity skipped (missing)` rows render after `active rollouts`/
  `archived rollouts` instead of before them.
- **Logs database location.** Rust moves `logs_2.sqlite` into an explicitly
  configured `log_dir` unless a managed `sqlite_home` requirement wins (Rust
  #55978), matching `Config::sqlite_config`. The state check probes that path, so
  both binaries report `log DB <log_dir>/logs_2.sqlite`.
- **PATH helpers.** Rust's in-process `which::which_all` builds Windows
  candidates from `PATHEXT` only, lists every matching extension in PATHEXT order
  and canonicalizes the match to its on-disk casing. Go also tried the
  extensionless name and reported the PATHEXT spelling, so an npm prefix printed
  `PATH entries (2)` with `codex` and `codex.CMD`; it now prints
  `PATH entries (1) ...\codex.cmd` and keeps the extensionless script
  unreported, like 0.166.
- **Doctor title defaults.** Rust doctor's `title.rs`
  `DEFAULT_TERMINAL_TITLE_ITEMS` is `activity, project-name` even though the TUI
  defaults to three items; the doctor title check reports the same two.
- Evidence: `out/r1301-doctor-diff.txt` (Environment diff after the changes),
  `out/r1302-pathext.txt` (six PATHEXT layouts, both binaries),
  `out/r1303-logdir.txt`/`out/r1303-doctor-rust.txt`/`out/r1303-doctor-go.txt`
  (configured `log_dir`), `TestCodexPathCandidatesFollowWhichAllLikeRust`,
  `TestStateCheckRoutesLogsDatabaseToConfiguredLogDirLikeRust`,
  `TestStateCheckReportsRustPathDBAndRolloutDetails` and
  `TestTerminalTitleReportsDefaultItemsAndGitProjectName`.

### 95. `doctor` renders the Configuration rows like `config_details`

- **Row order and labels.** Rust `config_details` prints `model` with its
  provider joined by `·`, then `cwd`, `config.toml`, `config.toml parse`,
  `config.toml read`, `MCP servers` and the feature flags, and finally drains
  every remaining detail in the order the config check recorded it.
  `writeConfigDetails` reproduces that, so `configuration scope`,
  `active thread overrides`, `configured TUI mode` and `configuration load ms`
  follow the flags instead of leading the block.
- **Hidden path rows.** Rust parses `CODEX_HOME`, `log dir` and `sqlite home` for
  the Configuration check and never prints them; Go printed all three (plus a
  separate `model provider` row and a lowercase `mcp servers` label). All are
  now consumed by the renderer, matching 0.166.
- **Feature flag separator.** Rust joins the counters with `·`
  (`3 enabled · 1 overridden (full list with --all)`) even under `--ascii`, so
  the Go row no longer uses `|`.
- Evidence: `out/r1304-doctor-full-diff.txt` (normalized diff of the whole
  report against the 0.166 binary: the Configuration block is identical apart
  from the feature-flag count and the load timing),
  `TestRenderHumanConfigDetailsMatchRustLikeRust` and
  `TestRenderHumanFeatureFlagsRespectAllLikeRust`.

### 96. `doctor` prints Rust's sandbox kind, summary separators and note dashes

- **Sandbox kind values.** Rust's `filesystem sandbox` row renders
  `FileSystemSandboxKind`, so `read-only` and `workspace-write` both print
  `restricted`, full access prints `unrestricted` and an executor-managed
  profile prints `external-sandbox`. `filesystemSandboxKindForDoctor` maps Go's
  sandbox policy onto those strings, which also fixes the row summary
  (`restricted fs + restricted network · approval OnRequest`).
- **Summary separators.** The sandbox, mcp and websocket row summaries join
  their halves with `·` like Rust's `row_description`, in the Unicode report
  and under `--ascii` (Rust keeps `·` there too).
- **Note dashes.** A non-OK row with a remediation joins the summary and the
  remedy with an em dash (` — `) in the Unicode report and ` - ` under
  `--ascii`, matching `actionable_note_summary`.
- **Auth label.** `doctorDisplayLabel` prints `auth.json path (file
  backend/fallback)` for the `auth file` detail row (`detail.rs`).
- Evidence: `out/r1306-sandbox-modes.txt` (read-only, workspace-write and
  danger-full-access: the summary and `filesystem sandbox` rows agree),
  `out/r1307-summaries.txt` (`title`, `mcp` and `sandbox` rows are byte-identical
  and the websocket note uses the em dash), `out/r1304-doctor-full-diff.txt`
  (the whole-report diff no longer carries the sandbox kind, the mcp/sandbox
  separator or the note dash), `TestFilesystemSandboxKindForDoctorMatchesRust`,
  `TestRowSummaryUsesEmDashForRemediationLikeRust` and
  `TestDoctorDisplayLabelAuthFileMatchesRust`.

### 97. `doctor` renders the sandbox rows in Rust's order and names the backend

- **Row order and set.** Rust's `sandbox_check` pushes the approval policy, the
  filesystem sandbox, `denied-read rules`, `denied-read glob rules`,
  `glob scan max depth`, `managed filesystem source`, `network sandbox` and the
  Linux helper, then the Windows-only `sandbox backend` and `denied-read
  restrictions`. Go now builds that list in one place, so the denied-read
  counters are always present (Rust prints `0`, never omits them) and the
  Go-only `execve wrapper helper` and `windows sandbox level` rows are gone.
- **Backend naming.** `windowsSandboxBackendForDoctor` resolves the effective
  Windows sandbox mode through `config.ResolveWindowsSandboxMode`, so the nested
  `[windows] sandbox` table and the legacy feature flags are honoured, and
  prints Rust's names: `mxc`, `elevated`, `restricted-token`, `disabled`.
- **Denied-read restrictions.** The row prints Rust's boolean
  (`!has_full_disk_read_access()`), `false` for a profile without deny entries,
  instead of Go's `active`/`inactive` wording.
- Evidence: `out/r1304-doctor-full-diff.txt` (the whole-report diff no longer
  contains a sandbox row), `out/r1308-backend.txt` and
  `out/r1309-backend-keys.txt` (nested/feature configs print the same
  `sandbox backend`), `TestSandboxCheckDetailOrderMatchesRust`,
  `TestSandboxCheckOmitsExecveWrapperRowLikeRust`,
  `TestWindowsSandboxBackendRowMatchesRust` and
  `TestSandboxCheckAppendsWindowsRowsAfterHelperLikeRust`.

## Verification

- Round 85 renders the `doctor` sandbox rows in Rust's order (entry 97): the
  denied-read counters and glob depth precede `managed filesystem source`,
  `network sandbox` follows it, `execve wrapper helper` and
  `windows sandbox level` are gone, `sandbox backend` resolves the nested
  `[windows] sandbox` table and the feature flags, and `denied-read
  restrictions` prints Rust's boolean. `out/r1304-doctor-full-diff.txt` no
  longer carries a sandbox row, `out/r1309-backend-keys.txt` matches the backend
  names, `go test -count=1 ./doctor ./state ./cli` and `go vet ./doctor` pass,
  and `./doctor` cross-compiles for `GOOS=linux` and `GOOS=darwin`.

- Round 84 prints Rust's sandbox kind and separators in `doctor` (entry 96):
  `filesystem sandbox` reports `restricted`/`unrestricted`/`external-sandbox`,
  the sandbox, mcp and websocket summaries join with `·`, a note's remedy is
  joined with an em dash in the Unicode report, and the auth detail row is
  labelled `auth.json path (file backend/fallback)`.
  `out/r1306-sandbox-modes.txt` and `out/r1307-summaries.txt` show the rows
  matching 0.166, `go test -count=1 ./doctor ./state ./cli` and
  `go vet ./doctor` pass, and `./doctor` still cross-compiles for `GOOS=linux`
  and `GOOS=darwin`.

- Round 83 aligns the `doctor` Configuration block (entry 95): `model` joins its
  provider, the `config.toml`/`MCP servers` rows precede the feature flags, the
  `CODEX_HOME`/`log dir`/`sqlite home` rows Rust only parses stay hidden and the
  feature-flag counters use `·`. `out/r1304-doctor-full-diff.txt` shows the
  Configuration block matching 0.166 apart from the feature-flag count and load
  timing, and `go test -count=1 ./doctor ./cli` passes.

- Round 82 removes the state-database and PATH-helper rows from the `doctor`
  Environment diff (entry 94): `out/r1301-doctor-diff.txt` now differs only on the
  `GCODE_HOME` label, the source-build packaging/`0.0.0` version, the
  bundled-vs-system `rg` and the configuration check's `log directory`;
  `out/r1302-pathext.txt` shows identical `PATH entries` for six PATHEXT layouts
  (including a lowercase `codex.cmd` next to an extensionless `codex`),
  `out/r1303-logdir.txt` reports `log DB` in the configured `log_dir` for both
  binaries, `go test -count=1 ./doctor ./state ./cli` passes, and `./doctor` still
  cross-compiles for `GOOS=linux` and `GOOS=darwin`.

- Round 81 finishes the `doctor` Environment rows (entry 93): the install
  flags collapse into one `managed by` row, the git PATH block moves after the
  repository rows, runtime/title labels follow `detail.rs`, database and rollout
  values keep their `·`, issue rows stop replacing the measured value, and the
  terminal check reports `stdin is terminal: false`, `terminal size: 120x30` and
  the non-interactive `TERM=dumb` warning. `out/r1297-doctor-diff.txt` shrinks to
  the `GCODE_HOME` label, packaging/version, search provider, default title items
  and the missing state databases, and `go test -count=1 ./doctor ./cli` passes.

- Round 80 renders the Rust 0.166 `doctor` Environment (entry 92): the
  normalized Environment diff in `out/r1296-doctor-diff.txt` is reduced to the
  `GCODE_HOME` label, the runtime/install detail formatting, the search provider
  and the extra state database rows, `out/r99-doctor-rust.txt` and
  `out/r99-doctor-go.txt` agree on the `os`/`OS language`/locale/editor/pager
  rows, the `✓ ⚠ ✗ ○` markers, the `─` separator, the `·` summary line and the
  git row order, and `go test -count=1 ./doctor ./cli` passes.

- Round 79 aligns the `--aws-sigv4` region and credential errors with the pinned
  0.166 SDK (entry 91): `out/r97aws-probe-after.txt` and
  `out/r97aws-probe2-after.txt` are byte-identical across the probed
  combinations (`out/r97aws-diff.txt` reports `TOTAL 9 FAILURES 0`),
  `out/r97aws-sig.txt`/`out/r97aws-sig-region.txt` compare the
  signed credential and region scope from real loopback registry requests, and
  `go test -count=1 ./auth ./model ./cli ./execserver` passes.
- Round 77 implements `exec-server forward --remote` with clap's scope-local
  requirement checks and the Noise-relay bridge (entry 90): `out/r96j-diff.txt`
  covers 75 argument combinations at `FAILURES 0`, `go test -count=1 ./cli
  ./execserver` passes, and `./app` still fails only the two pre-existing
  interactive-TUI tests (`TestInteractiveSlashCommandsUpdateTUIState`,
  `TestInteractiveSlashCommandRejectsGoOnlyApproval`).

- Round 76 keeps the thread-history WAL open, decodes legacy `exec_command_end`
  strictly and dates `task_complete` from the last written record (entry 89):
  `out/r87-wal.txt` reports `482 B -> 335.1 KB` for both implementations,
  `out/r89-storage-slope.txt` reports a 0 B delta at every session count,
  `out/r90-storage-fixture.txt` is line-identical for a single rollout,
  `out/r91b-task-complete-ts.txt` and `out/r92-cwd.txt` match in every probe,
  and `out/r92-p75diff.txt`-`out/r92-p85diff.txt` plus
  `go test -count=1 ./rollout` report no failures.

- Round 75 decodes every `item_completed` item into 0.166's
  `TurnItem` and writes it back (entry 88): `out/r85-itemcompleted.txt` reports
  69/69 payloads byte-identical with identical `bytes_processed`, covering the
  previously-surviving bad shapes (an unknown `phase`, the legacy `text`
  `AgentMessageContent` tag, a native `cwd`) and the re-serialized ones
  (`text_elements` defaults, camelCase `McpToolCall`, `completed_at_ms`
  default, rewritten `thread_id`); `out/r75*`-`out/r80*` still report
  `FAILURES 0`, and the new
  `TestCanonicalizeRolloutReplaysItemCompletedLikeRust` subtests freeze them
  (`go test -count=1 ./rollout` passes).

- Round 74 re-serializes the nested `turn_context` permission policies like
  0.166 (entry 87): `out/r80-perms.txt` reports 22/22 payloads byte-identical
  with identical `bytes_processed` for `permission_profile`,
  `active_permission_profile` and `file_system_sandbox_policy`, `out/r75*` to
  `out/r79*` still report `FAILURES 0`, and the new
  `TestCanonicalizeRolloutReserializesPermissionPoliciesLikeRust` subtests freeze
  them (`go test -count=1 ./rollout` passes).
- Round 73 converts the last legacy `migrate-rollouts` completion event,
  `image_generation_end`, into the `Extension(image_gen.generation)` item like
  0.166 (entry 86): `out/r79-imagegen.txt` reports 19/19 payloads
  byte-identical with identical `bytes_processed`, including the
  `AbsolutePathBuf` path cleaning, and `out/r75*`-`out/r78*` still report
  `FAILURES 0`; the new
  `TestCanonicalizeRolloutConvertsImageGenerationLikeRust` subtests freeze them
  (`go test -count=1 ./rollout` passes).
- Round 72 converts the remaining legacy `migrate-rollouts` completion events
  (`mcp_tool_call_end`, `web_search_end`, `context_compacted`, both review-mode
  events, `sub_agent_activity`, `dynamic_tool_call_response`) into completed
  `TurnItem` records like 0.166 (entry 85): `out/r78-convert.txt` reports 44/44
  payloads byte-identical with identical `bytes_processed`, the `out/r75*`,
  `out/r76*` and `out/r77*` probes still report `FAILURES 0`, and the new
  `TestCanonicalizeRolloutConvertsLegacyEventItemsLikeRust` subtests freeze them
  (`go test -count=1 ./rollout` passes).
- Round 71 re-serializes `migrate-rollouts` `turn_context` and `compacted`
  records like 0.166 (entry 84): `out/r77-turn-context-compacted.txt` reports
  20/20 payloads byte-identical with identical `bytes_processed`, the earlier
  `out/r76-*` probes still match, and the new
  `TestCanonicalizeRolloutReserializesTurnContextAndCompactedLikeRust` subtests
  freeze them (`go test -count=1 ./rollout` passes).
- Round 70 closes the `migrate-rollouts` record-shape and compressed-source
  gaps (entry 83): `out/r76-events.txt` reports 5/5 shapes byte-identical to
  0.166 with identical `bytes_processed`, `out/r76-compressed-cases.txt` 6/6
  `.jsonl.zst` fixtures byte-identical with identical counters
  (`bp = C + 2D + 2O + header`, from `out/r76-zst-model.txt`), and
  `out/r76-migrate-compare.txt` keeps the eight plain shapes green; new
  regression tests in `rollout/canonical_migration_events_rust_shape_test.go`,
  `go test -count=1 ./rollout` passes.
- Round 69 makes `migrate-rollouts --apply` replay legacy records like Rust
  (entry 82): `out/r75-migrate-compare.txt` reports 8/8 legacy shapes
  byte-identical to 0.166 with identical `bytes_processed`,
  `out/r75-rollback-bytes.txt` 3/3 rollback variants and
  `out/r75-compressed.txt` identical content for a `.jsonl.zst` source;
  `go test -count=1 ./rollout` passes with `./app` keeping only its two known
  baselines and `./appserver` only the two pre-existing `TestSkillMCPDependency*`
  failures.
- Round 68 makes `sandbox --sandbox-state-json` decode like serde before the
  `-c` overrides and the command check (entry 81): `out/r74-state.txt` and
  `out/r74-syntax.txt` compare 49 payloads against 0.166 (40 identical, the
  other 9 all one Windows-backend cause now listed under Still open), and
  `go test -count=1 -vet=off ./cli ./sandbox ./config` passes with `./app`
  keeping only its two known baselines.
- Round 67 gives `execpolicy check` starlark's diagnostic model (entry 80):
  `out/r73-execpolicy.txt` reports 33/33 identical against 0.166,
  `out/r73-suggest.txt` covers 36 did-you-mean shapes with 0 diffs and
  `out/r73-edge.txt` 11 edge cases with 0 diffs; `go test -count=1 -vet=off
  ./execpolicy ./config ./cli ./plugin` passes, and `./appserver` keeps only
  the two pre-existing `TestSkillMCPDependency*` failures that also fail at
  `0ea60afa`.
- Round 66 makes `completion <shell>` return clap_complete's own five scripts
  (entry 79): `out/r70-completion.txt` shows byte-identical bash/zsh/fish/
  powershell/elvish against 0.166, and `out/r70-p67-sweep.txt` drops the sweep
  from two differences to `cases 24 diffs 0`; `go test -count=1 -vet=off ./cli
  ./plugin ./state` passes with `./app` keeping only the two known baselines.
- Round 65 makes `login --with-access-token` decode and verify the agent
  identity JWT before storing it and stops the retired `--api-key` flag from
  creating `codex-login.log` (entry 78): `out/r69-jwt.txt` shows identical rc,
  message and home tree for seven token shapes, `out/r69-sweep.txt` covers 18
  more command surfaces with 15 identical, and
  `go test -vet=off ./agent ./auth ./app` keeps only the two known `./app`
  baselines.
- Round 64 replaces the Go-only stdin `apply` with Rust's task-diff flow and
  aligns the `features enable/disable` wording, `login status` logging, the arg0
  temp-home guard and the `mcp add --url` OAuth classification (entry 77):
  `out/r68-sweep.txt` compares rc, stdout/stderr and the home tree for 8
  surfaces (5 byte-identical), `out/r68-probe.txt` shows both binaries
  classifying six probe statuses identically, and
  `go test -vet=off ./cli ./gitutils ./chatgptapi ./mcp ./plugin ./state` passes
  while `./app` keeps only its two known baselines.
- Round 63 aligns the hidden `debug` help pages, clap's group-first subcommand
  resolution and the marketplace listing/removal paths (entry 76): 22 of 24
  swept command surfaces match 0.166 byte for byte (`out/r67-sweep.txt`), with
  only the two completion scripts open; `go test -vet=off ./cli ./plugin`
  passes and `./app`/`./appserver` keep only their known baselines.
- Round 62 aligns `debug clear-memories`, `execpolicy` read failures and
  `plugin list --json` (entry 75): four probes match 0.166 in rc, stdout,
  stderr and the home tree (`out/r66-verify.txt`, 4/4), and
  `go test -vet=off ./execpolicy ./state ./app` keeps only the two known
  `./app` baselines.
- Round 61 makes `debug models` print the bundled catalog (entry 74): the
  745,260-byte snapshot is byte-identical to 0.166 from three working
  directories (`out/r64-models.txt`), the offline fallback serves the same 12
  models, `go test ./model` passes, and `./appserver`/`./app` keep only their
  pre-existing failures.
- Round 60 sweeps 20 uncovered command surfaces (`completion --help`,
  `features --help|list --help|enable --help|disable|enable <unknown>`,
  `debug models --help`, `execpolicy` and `execpolicy check` help,
  `responses-api-proxy`, `stdio-to-uds`, `tcp-tunnel`, `plugin list --help`,
  `plugin remove`, `mcp list --help`, `login`, `logout`, `update` and `cloud`
  help): 19 match 0.166 byte for byte, the only diff is `debug
  prompt-input` (`out/r62-sweep.txt`).
- Round 59 aligns the `completion` value diagnostics and the `features list`
  column widths (entry 73): four completion probes match 0.166 byte for byte
  and the shared `features list` rows align (`out/r61-fix.txt`); the extra
  rows are post-0.166 upstream features. `go test -count=1 -vet=off ./cli`
  passes, as do `TestFeaturesList`/`TestFeaturesEnable` in `./app`.
- Round 58 aligns the `apply_patch` context errors (entry 72): the 14-document
  parse corpus and four context cases match 0.166 byte for byte
  (`out/r59-parse.txt`, `out/r59-context.txt`), and
  `go test -count=1 -vet=off ./applypatch ./tool ./app` passes.
- Round 57 aligns the `apply_patch` failure text (entry 71): ten
  `--codex-run-as-apply-patch` documents match 0.166 byte for byte
  (`out/r58-patch.txt`, `out/r58-patch-extra.txt`), and
  `go test -count=1 -vet=off ./applypatch ./tool ./app` passes.
- Round 56 aligns the `mcp add --url` login hint probe (entry 70): the delayed,
  immediate-404, unreachable and unresolvable probes are byte-identical to
  0.166 (`out/r57-slow-probe.txt`, `out/r57-probe.txt`).
- Round 55 aligns the `mcp get`/`mcp list` payloads (entry 69): the 32 text
  runs, the 18 JSON runs and the 36-run auth matrix are byte-identical to 0.166
  (`out/r55-mcp.txt`, `out/r55-mcpjson.txt`, `out/r56-auth.txt`), and
  `go test -count=1 -vet=off ./config ./cli ./mcp` passes in a clean worktree
  while `./app` keeps only the two known baselines
  (`TestInteractiveSlashCommandsUpdateTUIState`,
  `TestInteractiveSlashCommandRejectsGoOnlyApproval`).
- Round 54 adds the inline `mcp_servers` writer (entry 68): the 25 seed
  documents x eight `mcp add`/`mcp remove` shapes (200 runs) are byte-identical
  to 0.166 (`out/r54-inline.txt`), and `go test -count=1 -vet=off ./config ./cli ./mcp`
  passes in a clean worktree while `./app` keeps only the two known baselines.
- Round 53 adds the catalog JSON diagnostics (entry 67): 138 syntax/type
  documents, 148 mutated documents and 24 nested-table documents are
  byte-identical to 0.166 (`out/r53-diff1.txt`, `out/r53-diff-fuzz.txt`,
  `out/r53-table10.txt`), the 22-document path corpus loses the last four
  `model_catalog_json` diffs, and the 437/172/81/47 corpora keep 0 diffs.
  `go test -count=1 -vet=off -run TestValidateModelCatalogJSON ./config`
  passes in a clean worktree.
- Round 52 adds the TOML syntax diagnostics (entry 66): the 111-document
  syntax corpus is byte-identical to 0.166 for `mcp list`, `mcp remove`,
  `login status` and `exec` (0 diffs; the three `exec` timeouts are valid
  documents that need a live model), the 437/172/81/47 corpora still report 0
  diffs, and the 22-document path corpus keeps its four catalog JSON cases
  (`out/r52-diff3.txt`).
- Round 51 adds the `Config::load` file reads (entry 65): 22 path-setting
  documents match 0.166 except four `model_catalog_json` JSON-parse cases
  (`out/r51-diff1.txt`), the 437-document top-level corpus drops from 3 diffs to
  0 (`out/r51-top.txt`), and the 172/81/47 corpora keep 0 diffs
  (`out/r51-tables.txt`, `out/r51-nested.txt`, `out/r51-tui.txt`).
  `go test -count=1 -vet=off ./config ./cli ./mcp` passes in a clean worktree
  and `./app` keeps only the two known baselines.
- Round 50 types the remaining struct tables (entry 64): 172 documents across
  `auto_review`, `memories`, `skills`, `agents`, `apps`, `audio`, `browser_use`,
  `computer_use`, `hooks`, `notice`, `otel`, `permissions`, `plugins`,
  `realtime`, `tool_suggest`, `tools` and the rest are byte-identical to 0.166
  (`out/r50-diff2.txt`), with the 81-document nested corpus, the 47-document
  `[tui]` corpus and the 437-document top-level corpus keeping 0, 0 and 3
  diffs (`out/r50-nested.txt`, `out/r50-tui.txt`, `out/r50-top.txt`).
  `go test -count=1 -vet=off ./config ./cli ./mcp` passes in a clean worktree
  and `./app` keeps only the two known baselines.
- Round 49 types the `[tui]` table (entry 63): 47 documents covering every
  field, the `#[serde(flatten)]` notification ordering and the `presets`
  `missing field` case are byte-identical to 0.166 (`out/r49-tui5.txt`); the
  81-document nested corpus and 437-document top-level corpus keep 0 and 3
  diffs (`out/r49-diff1.txt`). `go test -count=1 -vet=off ./config ./cli
  ./mcp` passes in a clean worktree and `./app` keeps only the two known
  baselines.
- Round 48 extends the load pass into the nested tables (entry 62). Three
  focused corpora are byte-identical to 0.166: 33 `[profiles.*]` documents
  (`p48prof3.py`), 21 `[model_providers.*]` documents (`p48mp3.py`) and 27
  `windows`/`history`/`shell_environment_policy` documents (`p48w3.py`); the
  437-document top-level corpus keeps its 3 known diffs (`out/r48-diff1.txt`).
  `go test -count=1 -vet=off ./config ./cli ./mcp` passes in a clean worktree
  and `./app` keeps only the two known baselines.
- Round 47 adds serde's typed pass to the `mcp` commands (entry 61). A 437
  document corpus x `mcp list|get|add|remove|login|logout` is 434/437
  byte-identical (`out/r47-diff7.txt`); the three leftovers are the missing
  `model_instructions_file` / `model_catalog_json` /
  `experimental_compact_prompt_file` reads, where Rust reports the localized
  Windows io text. A focused 56-case semantic sweep (`p47sem6.py`,
  `out/r47-sem6.txt`) is 54/56, the two leftovers being TOML syntax errors that
  Rust reports with a position. `go test -count=1 -vet=off ./config ./cli
  ./mcp` passes in a clean worktree and `./app` keeps only the two known
  baselines.

- Round 46 makes the `mcp add`/`mcp remove` writer edit the document in place
  (entry 60). Ten seed documents x four command sets are 33/40 byte-identical
  (`p46order.py`); the 7 leftovers are all the `[[profiles]]` load rejection, and
  a second 33-case corpus covering comments, multi-line strings and arrays, CRLF,
  missing final newlines, explicit `[mcp_servers]` headers and nested tables is
  31/33 with the two leftovers being the unknown `[features.x]` rejection.
  `go test -count=1 -vet=off ./config ./cli` passes and `./app` keeps only the
  two known baselines.
- Round 45 covers the empty-value and session-lookup family. The 164-case
  empty/extra-positional sweep (`out/r45-empty.txt`) has no deterministic diff
  once the three Rust probe timeouts are rerun (`p45slow.py`), the 14 session
  command shapes are all SAME (`p45sess_wt.py`, `p45sess2_wt.py`), and
  `mcp add`'s callback URL, `--url=` persistence and merged `requires` block
  match 0.166 byte for byte (`p45mcp4.py`, `p45cb4.py`).
  `go test -count=1 -vet=off ./cli ./mcp ./config` passes in the clean
  worktree and `./app` keeps only the two known baselines.
- The resume/fork start gate matches Rust 0.166 on all 96 argument-shape/TERM
  combinations (`out/round43-tui-gate.txt`) and the 36 archive/queue/override
  cases (`out/round43-session-cli.txt`): exit code, stderr and stdout are
  identical, including the `ERROR:`/`Error:` heads and the
  `failed to parse -c overrides:` wording. The only harness timeouts are
  `queue ... -c x=` on the Rust side, which starts a daemon and never returns
  within the probe window. `go test -count=1 -vet=off ./cli ./config ./plugin`
  passes in a clean worktree of the commit and `./app` passes in the shared
  tree; a clean worktree still shows the two pre-existing
  `TestInteractiveSlashCommandsUpdateTUIState` /
  `TestInteractiveSlashCommandRejectsGoOnlyApproval` failures noted under
  entry 56, whose fixes are uncommitted in the shared tree.
- The app-server startup gate matches Rust 0.166 on every case of the 159-entry
  matrix (`out/round42-compare.txt` via `p42cmp.py`, exit code plus full stderr
  and stdout) and the 28 accepted combinations start the server without a
  diagnostic. Re-run alone: the nine `app-server` cases that serve until EOF
  time out for Rust under parallel load, so the matrix must not share the
  machine with a package test (`out/round42-compare-quiet.txt` is the clean
  pass). The subcommand matrix is 8/10 with the two diffs being the pre-existing
  socket runtime text, boolean repeats stay 149/149
  (`out/round41-bool*.json`), single-value repeats 129/129, the 1,068-case
  inline sweep keeps its 35 baseline diffs with 0 new and 0 fixed
  (`out/r35-inline.txt` vs `out/round40-inline.txt`), bad values 497/497
  (`out/round41-r34-flag-surface.txt`), and `go test -count=1 -vet=off ./cli
  ./config ./plugin` passes in a clean worktree of the commit. Three failures
  reproduce unchanged in that worktree and are outside this change:
  `TestInteractiveSlashCommandsUpdateTUIState` and
  `TestInteractiveSlashCommandRejectsGoOnlyApproval` also fail at the parent
  commit `ef41744a`, and
  `TestPrecomputedExportArtifactsMatchTargetRustCommit` (schema artifact hash)
  fails there too; `TestServerRequestBrokerRejectsOnlyOriginatingConnectionLikeRust`
  passes at the parent but is red with another agent's in-flight
  `appserver/turn_runtime.go` edit in the shared tree.
- clap's value diagnostics, usage blocks and the ClientInfo schema match Rust
  0.166: `out/round41-order-rust.txt` and `out/round41-positional-echo.txt`
  pin the ordering/usage rules, `out/round41-startup-compare2.txt` the 33
  ClientInfo shapes, `out/round41-startup-lex.txt` the 16 lexer shapes,
  `out/round41-positional-compare.txt` the 14 positional usage blocks and
  `out/round41-nodaemon.txt` the agents rejection. Regressions stay clean:
  149/149 boolean repeats (`out/round41-bool*.json` via `p40j.py`), 129/129
  single-value repeats (the former `--startup-client-info` miss is gone), the
  1,068-case inline sweep at 35 diffs with 0 new and 0 fixed
  (`out/round42-inline.txt` vs `out/round40-inline.txt`), 497/497 bad-value
  cases (`out/round41-r34-flag-surface.txt`) and the 12 round 29-34 probe
  groups at 0 diffs (`probe_r34x_ok.py` reports 48/48 in
  `out/round41-r34x.txt`). `go test -count=1 -vet=off ./cli ./app ./plugin
  ./config` and `./appserver` pass.

- clap's occurrence diagnostics match Rust 0.166 across the command tree:
  `out/round40-bool.json` (149/149), `out/round40-value-invalid.json`
  (129/129) and `out/round40-value.json` (128/129), with the one miss recorded
  under Still open. The 1,068-case inline sweep stays at 35 diffs with 0 new and
  0 fixed (`out/round40-inline.txt`); the 497 bad-value cases
  (`out/round40-r34-flag-surface.txt`), 78 round-34 fixes
  (`out/round40-r34-fixed.log`), 16 ordering cases
  (`out/round40-r35-order.log`) and 31 round 29-34 probes
  (`out/round40-regress.txt`, `out/round40-regress2.txt`) stay at 0 diffs.
  `go test -count=1 -vet=off ./cli ./sandbox ./applypatch ./prompt ./review
  ./cmd/... ./tcptunnel ./config ./rollout ./app` passes.
- `review`/`exec review` scope diagnostics match Rust 0.166: the 1,068-case
  inline sweep ends at 1,033 identical with 0 new diffs and two fixes
  (`codex review --title=`, `codex exec review --title=`,
  `out/round39-inline.txt`), and the 497 bad-value cases
  (`out/round39-r34-flag-surface.txt`), 78 round-34 fixes
  (`out/round39-r34-fixed.log`), 16 ordering cases
  (`out/round39-r35-order.log`) and 31 round 29-34 probes
  (`out/round39-regress.txt`, `out/round39-regress2.txt`) stay at 0 diffs
  (the two `<ACCEPTED>`/`--color` rows that occasionally appear there are the
  6-second probe timeout catching Rust's auth/network stall; `probe_r34x`
  alone reports 0 diffs twice in a row in `out/round39-r34x.log`).
  `go test -count=1 -vet=off ./cli ./sandbox ./applypatch ./prompt ./review
  ./cmd/... ./tcptunnel ./config ./rollout` and `go test -vet=off ./app` pass.
- `migrate-rollouts` matches Rust 0.166 on the uuid `--thread` grammar and the
  human report: `out/p38g-uuid.txt` covers 26 accepted/rejected uuid shapes
  (26/26) and `out/p38k-migrate.txt` compares 8 scenarios at 7 byte-identical,
  leaving only the `bytes_processed` counter. The 1,068-case inline sweep ends
  at 1,031 identical with 0 new diffs (`out/round38-inline.txt`), and the 497
  bad-value cases (`out/round38-r34-flag-surface.txt`), 78 round-34 fixes
  (`out/round38-r34-fixed.log`), 16 ordering cases
  (`out/round38-r35-order.log`) and 31 round 29-34 probes
  (`out/round38-regress.txt`, `out/round38-regress2.txt`) stay at 0 diffs.
  `go test -count=1 -vet=off ./cli ./sandbox ./applypatch ./prompt ./review
  ./cmd/... ./tcptunnel ./config ./rollout` passes; `go test -vet=off ./app`
  keeps the two known baselines plus the known flaky auth-concurrency test.
- Empty `-c`/`--config=` overrides match Rust 0.166: `out/round37-inline.txt`
  repeats the 1,068-case inline sweep and ends at 38 diffs, none of them a
  `--config=` case beyond environment noise (22 fixed). The bad-value sweep
  `out/round37-r34-flag-surface.txt` stays 497/497, the 78 round-34 fixes
  (`out/round37-r34-fixed.log`) and the 16 ordering cases
  (`out/round37-r35-order.log`) stay at 0, and the 31 round 29-34 probes in
  `out/round37-regress.txt` / `out/round37-regress2.txt` stay at 0 diffs.
- Inline `--flag=` and empty-value diagnostics match Rust 0.166:
  `out/round36-inline.txt` walks 68 command paths x every value flag in three
  shapes (1,068 probes) and ends at 60 diffs, all of them TERM TUI, daemon
  version or signed-out runtime chatter; the bad-value sweep
  `out/round36-r34-flag-surface.txt` stays 497/497 and the 31 round 29-34
  probes in `out/round36-regress.txt` / `out/round36-regress2.txt` stay at 0.
- The exec family echoes the global `--enable`/`--disable` in argv order:
  `out/round35-enable-order.txt` covers 16 orderings (both directions, the
  `--image` merge, `--disable`, `exec review`/`exec fork`, `--las` with a
  suggestion and `exec-server forward`'s required options) against 0.166.
- The global option surface matches Rust 0.166: `out/r34-flag-surface.txt`
  walks 68 command paths and probes every declared value flag with a bad value,
  a bare flag and a trailing `--bogus` (497 cases, 497 identical), and
  `out/round34-fixed.txt` replays the 78 cases this round fixed. Both compare
  stdout, stderr and exit status against 0.166.

- The remaining command groups match Rust 0.166 on clap's flag diagnostics:
  127 live cases cover the did-you-mean tip, the `--` workaround tip, clap's
  smart usage ordering, the required-argument blocks and the daemon /
  marketplace / uninstall leaves (`out/round32-*.txt`), and the 118 round 29-31
  regression cases stay at 0 diffs (`out/round32-all-probes.txt`).
  `go test ./cli ./sandbox ./applypatch ./prompt ./review ./cmd/...
  ./tcptunnel` passes, `TestSubcommandGroupFlagDiagnosticsLikeRust` pins 120 of
  those cases, and `go test -vet=off ./app` keeps only the
  `TestExecJSONEndToEnd` baseline.
- Short-flag diagnostics, pending-value resolution and the global
  `-c`/`--config` match Rust 0.166: 20 short-flag cases
  (`out/round33-short-flags.txt`), 8 per-command usage-spelling cases
  (`out/round33-usage-spelling.txt`), the pending-value and `--`/`-c` probes
  (6/6, 9/9, 5/5, 8/8) and the 307-case round 29-32 regression
  (`out/round33-regress.txt`) are byte-identical. Four tests pin the captured
  cases: `TestShortFlagDiagnosticsLikeRust`,
  `TestPendingValueResolutionLikeRust`, `TestShortFlagUsageSpellingLikeRust` and
  `TestSubcommandConfigOverrideAcceptanceLikeRust`.
- Session subcommand parsing matches Rust 0.166: the 14 subcommand-diagnostic
  cases (`out/round29-session-diag.txt`), the 10 archive/delete/unarchive cases
  (`out/round29-mutation.txt`) and the 30 queue cases
  (`out/round29-queue.txt`) are byte-identical, and `go test ./cli ./sandbox
  ./applypatch ./prompt ./review ./cmd/... ./tcptunnel` passes. `go test
  -vet=off ./app` still fails only the two known baselines plus the known flaky
  auth-concurrency test.
- The missing-required surface matches Rust 0.166: `probe_missing_r25` and
  `probe_r25i` compare 34 tcp-tunnel / execpolicy / stdio-to-uds invocations
  (`out/round25-hidden-diagnostics.txt`); the only difference is the
  `stdio-to-uds <path>` connect attempt on Windows. The 434-case help probe is
  now at 0 diffs, and `go test ./cli ./tcptunnel ./applypatch ./prompt ./review
  ./cmd/...` passes. `go test -vet=off ./app` in the shared working tree also
  fails `TestExecServerRegistryAuthConcurrentRefreshesShareOneRefreshLikeRust`;
  that test passes on a clean `f0ee41de` checkout with these cli/tcptunnel
  changes applied, so the failure comes from another agent's in-flight edits
  rather than this round.
- The full help surface is byte-identical to Rust 0.166: `probe_subhelp_r24`
  compares 434 invocations (80 paths x `--help`/`-h`/`--help=x`/`-hx`, 79
  `help <path>`, 12 short-flag and 23 root cases) on stdout, stderr and exit
  status. The only difference is `execpolicy check -hx`, which fails on the
  missing-required diagnostic still listed under Still open. The 160-page probe
  and the 80 unknown-argument / 56 missing-argument regressions stay at 0 diffs
  (`out/round24-subcommand-help.txt`). `go test ./cli` passes, and
  `go test ./cli ./applypatch ./prompt ./review ./cmd/...` is green while
  `go test -vet=off ./exec ./app` keeps its 9 and 2 pre-existing failures.
- `go test ./config/`, `go test ./tui/onboarding/`, `go test ./tui/...` pass;
  `go test ./app/` keeps its two pre-existing unrelated failures
  (`TestExecJSONEndToEnd`, `TestLocalSessionForksRustRolloutWithoutGoSnapshot`),
  reproduced unchanged on a clean `HEAD` worktree, so they are not caused by
  these changes.
- Slash popup after the rebuild is byte-identical to Rust 0.166 at 120x40 apart
  from the version line (`out/slash-menu-01-rust166.txt` vs
  `out/slash-menu-01-go.txt`); the eight Go-only commands no longer appear.
- `go test ./tui/` and `go test ./tui/...` pass after the command removal;
  the reworked expectations are
  `TestModelSlashEditorIsUnrecognizedLikeRust`,
  `TestModelClearAttachmentsCommandIsUnrecognizedLikeRust`,
  `TestModelSlashInlineArgumentDispatchMatchesRust`,
  `TestComposerSlashPromptKeepsTextElementOffsetsLikeRust`,
  `TestComposerHistoryRecallRestoresTextElementsLikeRust` and
  `TestInteractiveSlashCommandRejectsGoOnlyApproval`.
- `$` popup after the rebuild renders `codex-go-update   [Skill] ...` like Rust.
- Onboarding screen is text-identical to Rust (`out/onboarding-rust.txt` vs
  `out/onboarding-go.txt`).
- `codex exec` in the trusted repo now reports
  `sandbox: workspace-write [workdir, /tmp, $TMPDIR]`; `/status` reports
  `Workspace (Ask for approval)`, matching Rust.
- A real prompt turn ("Say exactly: ALIGN-OK") renders the same user line,
  `• ALIGN-OK`, and duration row on both sides (`out/prompt-*.txt`).
- `go test ./tui/tea` and the targeted `./exec`/`./appserver` regressions pass;
  `go test ./app` keeps only the two pre-existing failures listed above.
- `/status` and `/model` are text-identical to Rust 0.166 at 120x40
  (`out/status2-rust166.txt` vs `out/status2-go.txt`,
  `out/model2-rust166.txt` vs `out/model2-go.txt`), apart from the version
  string and the known `← for agents` footer item.
- Both suggestion popups now match Rust 0.166 at 120x40 apart from the version
  line: `/` first page and four scrolled pages
  (`out/slash-menu-01-*`, `out/slashscroll-{0..4}-*`), filtered `/qu`, `/a`,
  `/s` (`out/slashfilter-*-rust166.txt` vs `-go.txt`), and `$`
  (`out/dollar-menu-01-*`, `out/skillscroll-{1..3}-*`).
- `go test ./tui/tea ./tui ./tui/bottom_pane` pass, including the new
  `TestSlashPopupNameColumnStaysPutWhileScrollingLikeRust` and
  `TestSkillPopupRendersPickerFrameLikeRust`.
- Mid-scroll states of both popups (at the end, then two `↑`) match Rust:
  `out/skillmid-{0,1,2}-*` and `out/slashmid-{0,1,2}-*`.
- `/skills` (menu and the `$` picker it opens) and `/permissions` are
  byte-identical to Rust 0.166 at 120x40 apart from the version line:
  `out/skills1-{rust166,go}.txt`, `out/skills2-{rust166,go}.txt`,
  `out/perm-rust166.txt` vs `out/perm-go.txt`.
- `go build ./...`, `go test ./tui/...` pass; `go test ./app/` keeps only the
  two pre-existing failures listed above. New regressions:
  `TestSkillsPickerRowsMatchRustSelectionView`,
  `TestSelectionViewWrapsDescriptionUnderItsColumnLikeRust`,
  `TestPermissionsMenuReusesDiscoveredCatalog`;
  `TestSkillsMenuViewUsesRustCopyAndShortcut` and
  `TestModelSkillsListMenuOpensSkillPopup` now assert the `$` shortcut.
- The full-access confirmation (`out/permfull-{rust166,go}.txt`) matches Rust
  0.166 apart from the version line and the known `← for agents` footer item.
- `/daemon` and its restart confirmation match Rust 0.166 at 120x40 apart from
  the version line: `out/daemon4-go.txt` vs `out/daemon-rust166.txt` are
  byte-identical, and `TestDaemonRestartConfirmationMatchesRustSnapshot` pins
  the centered dialog to Rust's
  `codex_tui__app__daemon_menu__tests__daemon_restart_confirmation.snap`.
  Pressing `enter` on a source without a managed daemon leaves the menu open on
  both sides (`out/dc-{rust166,go}-*.txt`).
- `/mcp` prints Rust's `empty_mcp_output` copy (`out/mcp5-go.txt`) and renders
  the populated list byte-identically for the three servers Rust reported
  (`out/mcp166-rust166.txt`); the `MCP`/`Loading MCP inventory...` footer
  notices and the empty-state blossom are gone, covered by
  `historycell.IsStartupCell` and `TestWelcomeBlossomEligibilityLikeRust`.
- `/diff` opens Rust's static pager: `out/diff3-go.txt` reproduces the
  `/ D I F F / ...` header, the `~` filler, the ` N% ` rule and the
  `q close` hint (`out/diff166-rust166.txt` shows the same chrome while Rust's
  sandbox refuses to compute a diff in this workspace).
- `/debug-config` now ends with Rust's `[agents]` block
  (`out/dbg3-go.txt` vs `out/dbg166-rust166.txt`).
- `/config` matches Rust 0.166 at 120x40 on all five tabs, the Effects child
  picker, the empty `/` search, the `v`/`x`/`vim` searches, Esc-from-search,
  `j j k`, wrap-around `k k` and `l` tab switching (`out/cfgtab*`,
  `out/cfgeffects-*`, `out/cfgsearch0-*`, `out/cfgs-*`, `out/cfgesc-*`,
  `out/cfgjjk-*`, `out/cfgwrap-*`, `out/cfgl-*`). With `CODEX_HOME=~/.codex`
  the Appearance tab is a 0-diff match (`out/cfgsharedhome-go.txt`), proving the
  `github`/`Automatic` label difference is config data. Regressions:
  `TestConfigCommandOpensSearchablePanelLikeRust`,
  `TestConfigPanelSearchSpansGroupsLikeRust`,
  `TestConfigPanelNarrowKeepsValueAndDescribesBelowLikeRust`,
  `TestEffectsRapidTogglesIgnoreStaleSaveResultLikeRust`,
  `TestEffectsComposerStarsPendingValueSurvivesDisabledRowLikeRust`.

- `/config` child pickers match Rust 0.166 at 120x40: `out/cfgnotify-*`,
  `out/cfgsession-*`, `out/cfgresume-*`, `out/cfgcopy-*`,
  `out/cfgrightclick-*` are 0-diff apart from the version line, and
  `out/cfgpick-pick{1,2}-*` show a pick being applied to the parent row and
  reverted. Regression tests: `TestConfigPanelChildPickerKeysLikeRust`,
  `TestConfigPanelResumeDirectoryPickerLikeRust`,
  `TestConfigPanelResumeDirectoryRemoteRenderLikeRust`,
  `TestConfigPanelResumeDirectoryRemoteAvailabilityLikeRust`.

- `go build ./...` passes with an isolated `GOCACHE`; `go test ./tui/...`
  passes; `go test ./app/` keeps only the two pre-existing failures.
- `/keymap` round: `go build ./...` succeeds, `go test ./tui/...` passes
  (tea 45s), `go vet ./tui/... ./keymapschema/ ./config/` is clean, and
  `go test ./app/ -run Keymap` passes. `go test ./tui/chatwidget/` now pins
  `pager.find` to Rust's `f3, /` order.
- `codex exec --json` id round: `go build ./exec` and
  `go test ./exec -run TestExecJSONItemIDsMatchRustCounterLikeRust` pass; the
  full `go test ./exec` set matches HEAD (9 pre-existing failures, reproduced
  in a clean worktree). Real runs: `out/execjson-go2.txt` emits
  `item_0`/`item_1` warnings then `item_2` for the reply, and
  `out/execjson-tool-go2.txt` shares `item_2` between the started/completed
  command and gives the reply `item_3`, matching
  `out/execjson-rust166.txt` / `out/execjson-tool-rust166.txt` modulo the
  upstream duplicate warning.
- `codex exec --json` command-shape round: `go build ./tui/... ./cmd/...
  ./exec` and `go vet ./exec` are clean; the two new tests pass; the full
  `go test ./exec` failure set (9 tests) is byte-identical to a clean worktree at
  the same HEAD (`codex_go_wt/r141execbase`). Real runs with qax
  `deepseek-v4-flash`: `out/execjson3-tool-{rust166,go}.txt` (success) and
  `out/execjson4-fail-{rust166,go}.txt` (exit 3) match on `command`,
  `aggregated_output`, `exit_code` and `status`.
- `/keymap` Debug round: `go build -o bin\codex.exe .\cmd\codex` succeeds,
  `gofmt -l` is clean for the touched files, and
  `go test ./tui/tea -run TestKeymapInspector` plus
  `go test ./tui -run TestKeymapActionsForKey` pass. Real-TUI captures with a
  live qax model config differ only on the version banner, apart from the
  Insert / Ctrl+Space input-layer gap recorded under Still open.

- `exec` help round: `go test ./cli` passes and `go test ./app` keeps only
  its two pre-existing failures (`TestExecJSONEndToEnd`,
  `TestLocalSessionForksRustRolloutWithoutGoSnapshot`, reproduced unchanged in a
  clean worktree at `08d888bc`). The 34-case live comparison of
  `bin\codex.exe` against the 0.166 build matches stdout, stderr and exit code
  for every exec help page, `exec help <topic>`, every `--bogus`/invalid-value
  diagnostic and both `--approve-for-me` conflicts. Real-model smoke test with
  qax `deepseek-v4-flash` (`--skip-git-repo-check -C .tmp-usealign/out/afm1`):
  `codex exec --approve-for-me -m deepseek-v4-flash "Reply with exactly: AFM-OK"`
  exits 0 and answers `AFM-OK` on both CLIs, so the auto-review overrides are
  accepted by Go's config layer.

- stdin/error-surface round: an 11-case live matrix (empty/blank/forced stdin
  for `exec`, `resume --last`, `resume --last -`, `review`, `review -`,
  `fork abc`, `login status`, `logout`, and a missing `--output-schema`) matches
  Rust 0.166 byte for byte on stdout, stderr and exit status in 10 cases; the one
  remaining difference is the OS-level `strerror` text inside
  `Failed to read output schema file ...` (Rust reports the localized Windows
  message, Go reports `syscall`'s English text). `go test ./prompt ./review
  ./cli ./cmd/... ` pass, `go test ./app` keeps only its two pre-existing
  failures and `go test ./exec` only its nine pre-existing ones. Real-model check
  with qax `deepseek-v4-flash`: `echo "extra context" | codex exec ... "Reply
  with exactly: CTX-OK"` prints the `<stdin>` block, the append notice and
  `CTX-OK` on both CLIs. Regressions: `prompt/stdin_behavior_like_rust_test.go`,
  `app/exec_stdin_like_rust_test.go`, `cmd/internal/codexmain/error_prefix_like_rust_test.go`
  plus the updated login stream assertions in `app/app_test.go`.

- exec final-output round: after `go build -o bin\codex.exe .\cmd\codex`,
  `go test ./prompt ./review ./cli ./cmd/...` pass, `go test ./exec` keeps only
  its nine pre-existing failures and `go test ./app` only its two, and `gofmt -l`
  is clean for the touched files. Three live qax `deepseek-v4-flash` runs (bad
  `--output-last-message` path, plain `exec`, `--json`) match Rust 0.166 on
  stdout, stderr ordering and exit status; with stdin on `NUL` both CLIs now print
  `Reading additional input from stdin...` first. The failure case differs only in
  the localized `io::Error` text recorded under Still open.

- exec resume/fork round: `go test ./cli ./prompt ./review ./cmd/...` pass and
  `go test ./exec ./app` keep exactly their pre-existing failures (9 and 2).
  Live qax `deepseek-v4-flash` runs of `resume` with an unknown name and of
  `resume --last` in a history-free directory start a new thread and answer on
  both CLIs, while the two missing-UUID cases now print Rust's exact
  `thread/resume` / `thread/fork` messages with exit status 1.

- exec OS-error / argv round: `go test ./cli ./prompt ./review ./cmd/...` pass
  and `go test ./exec ./app` keep exactly their pre-existing failures (9 and 2).
  Live comparisons against 0.166 are byte-identical for the localized
  `(os error N)` text, the variadic `--image` parse, the missing-image-value
  diagnostic and the single-PROMPT positional diagnostics; real qax
  `deepseek-v4-flash` runs answer exactly when a prompt precedes `-i`.

- exec subcommand argv round: `go test ./cli` passes, `go test ./exec ./app
  ./cmd/... ./prompt ./review` keeps exactly the pre-existing exec/app failures
  (9 and 2; `TestExecServerRegistryAuthConcurrentRefreshesShareOneRefreshLikeRust`
  passes standalone and remains flaky under the full app run), and the live
  diagnostics for resume/fork/review positionals are byte-identical to 0.166.

- apply_patch error-text round: `go test ./applypatch ./cli ./prompt ./review
  ./cmd/...` pass, `go test ./exec ./app` keeps exactly the pre-existing
  failures (9 and 2), `go vet ./applypatch` is clean, and 19 live
  `--codex-run-as-apply-patch` probes against 0.166 are byte-identical.

- root/doctor clap round: `go test ./cli ./applypatch ./prompt ./review
  ./cmd/...` pass, `go test -vet=off ./exec ./app` keeps exactly the
  pre-existing failures (9 and the two known app tests plus the flaky
  auth-concurrency case, which passes standalone), and 34 live diagnostics
  match 0.166 byte for byte.

- subcommand clap round: `go test ./cli ./applypatch ./prompt ./review ./cmd/...`
  pass (after the concurrent `appserver/router.go` / `compact` edits settle; those
  broke `go build ./cmd/codex` mid-round and are not part of this change), and 158
  live comparisons against Rust 0.166 are byte-identical.

- subcommand shared-flag round: `go build -o bin\codex.exe .\cmd\codex` succeeds,
  `go test ./cli ./applypatch ./prompt ./review ./cmd/...` pass and
  `go test -vet=off ./exec ./app` keeps exactly the pre-existing failures (9 and
  2). 144 live comparisons against 0.166 (accepted values, missing values and
  `=` forms across 36 subcommand surfaces) plus 18 targeted cases agree on
  stdout, stderr and exit status apart from `execpolicy`, `stdio-to-uds` and
  `tcp-tunnel`, whose Go parsers still answer with their own missing-argument
  wording. The previous rounds' probe sets stay clean: 80 unknown-flag, 56
  missing/misplaced argument, 17 missing-value and 8 `sandbox setup` comparisons
  report 0 diffs.

- group help round: `go build -o bin\codex.exe .\cmd\codex` succeeds,
  `go test ./cli ./applypatch ./prompt ./review ./cmd/...` pass and
  `go test -vet=off ./exec ./app` keeps exactly the pre-existing failures (9 and
  2). 56 live group-page comparisons and 32 group-level `-c` comparisons against
  0.166 are byte-identical; the earlier probe sets stay clean (80 unknown-flag,
  56 missing/misplaced argument, 17 missing-value, 8 `sandbox setup`, 144 shared
  flag comparisons down to the three parsers listed under Still open).

- `mcp` OAuth registration round: `go build -o bin\codex.exe .\cmd\codex`
  succeeds, `gofmt -l` is clean for the touched files,
  `go test ./cli ./applypatch ./prompt ./review ./cmd/... ./tcptunnel` pass and
  `go test -vet=off ./app` keeps exactly the two pre-existing failures
  (`TestExecJSONEndToEnd`, `TestLocalSessionForksRustRolloutWithoutGoSnapshot`).
  23 live `mcp add`/`mcp login` comparisons against 0.166
  (`out/round26-mcp-oauth.txt`) are byte-identical on stdout, stderr and exit
  status: the four former Go-worded `requires` errors now print clap's
  singular root-usage and plural `mcp add`-usage blocks, and
  `--oauth-client-registration` accepts `auto|cimd|dcr` with clap's invalid and
  missing-value diagnostics.

- `sandbox setup` presence round: `go build -o bin\codex.exe .\cmd\codex`
  succeeds, `gofmt -l` is clean for the touched files,
  `go test ./cli ./sandbox ./applypatch ./prompt ./review ./cmd/... ./tcptunnel`
  pass and `go test -vet=off ./app` keeps the two known failures plus the flaky
  auth-concurrency case, which passes standalone. 26 live comparisons against
  0.166 (`out/round27-sandbox-setup.txt`) are byte-identical: `--user=` honours
  the required group and codex-home dependency, empty `--codex-home` values
  report clap's missing-value block, and the usage blocks echo consumed options
  in argv order.

- `mcp add` transport group round: `go build -o bin\codex.exe .\cmd\codex`
  succeeds, `gofmt -l` is clean for the touched files,
  `go test ./cli ./sandbox ./applypatch ./prompt ./review ./cmd/... ./tcptunnel`
  pass and `go test -vet=off ./app` keeps the two known failures plus the flaky
  auth-concurrency case, which passes standalone. 11 live comparisons against
  0.166 (`out/round28-mcp-add-groups.txt`) match on stdout, stderr and exit
  status; the only difference is the config.toml serialization shape listed
  under Still open.

- The worktree / strict-config round matches Rust 0.166: the 15 worktree
  rejections (`out/round30-worktree.txt`), the 5 ordering cases
  (`out/round30-worktree-order.txt`), the 4 `exec review --worktree` cases
  (`out/round30-exec-review.txt`), the 12 strict-config combinations
  (`out/round30-strict-config.txt`), the 9 remote-control names
  (`out/round30-remote-control.txt`) and the 10 round-29 mutation cases are
  byte-identical on a clean worktree at `f3bb3db9`, which also passes
  `go test ./cli ./sandbox ./applypatch ./prompt ./review ./cmd/...
  ./tcptunnel`; `go test -vet=off ./app` keeps only the two known baselines.

- Long-flag diagnostics match Rust 0.166 for the root command, the `exec`
  family, the session commands and `codex review`: 20 did-you-mean cases
  (`out/round31-flag-suggestions.txt`), 13 consumed-option smart usage cases
  (`out/round31-smart-usage.txt`) and 26 boundary cases
  (`out/round31-flag-boundaries.txt` - unknown shorts, `=value` spellings,
  hidden aliases, bound positionals and the root subcommand fallback) are
  byte-identical. `go test ./cli ./sandbox ./applypatch ./prompt ./review
  ./cmd/... ./tcptunnel` passes.
## Still open

- A source build reports `codex-cli 0.0.0` while 0.166 prints its release
  version; the version string is injected by the release pipeline
  (`-ldflags`), so a plain `go build` binary cannot match it.
- `mcp add --url https://example.com/mcp` still differs in this sandbox: Rust's
  TLS stack rejects the intercepted certificate, so its discovery fails and the
  login hint prints, while Go's system store trusts the chain, reaches the CDN's
  404 and stays silent. The classification itself now matches
  (`out/r68-probe.txt`), so this is a certificate/transport difference rather
  than command behaviour.
- `login status` leaves Go's home tree empty while 0.166 writes its own
  `tmp/arg0-windows/codex-arg0*/apply_patch.bat` helpers. Those files belong to
  the Rust binary's dispatch layer and are created before the CLI parses
  arguments, so Go does not mirror them; Go's equivalents are only produced by
  its production entry point and refuse a temp-dir `CODEX_HOME` like Rust.
- `logout` leaves an `auth.json.lock` sidecar that 0.166 never creates. Upstream
  main serialises credential commits through `<auth file>.json.lock`
  (`login/src/auth/storage_commit.rs`) and Go follows that; the pinned 0.166
  binary predates the sidecar and only uses `auth.lock`, so the extra file is a
  version delta rather than a missing cleanup.
- `go test ./parity` fails six registry/surface comparisons because the local
  Rust checkout (`b0f977c3a9`) predates upstream changes the Go tree already
  carries: `suppress_model_change_announcements` (#58405) is implemented in Go
  while `features/src/lib.rs` at that revision does not list it, and
  `deferred_tool_world_state` became stable and enabled by default (#58410)
  after the checkout. These are snapshot staleness, not Go defects.
- Override diagnostics the 0.166 binary still answers from its own runtime:
  `remote-control --config=` reports the daemon version mismatch instead of
  Rust's `foreground app-server exited before remote control became ready`
  chain, `update --config=` prints Go's install-detection wording, and
  `fork`/`resume`/`agents` report the override before Rust's TERM gate.
- `sandbox --sandbox-state-json` still diverges once the payload decodes: the
  legacy empty profile (`"permissionProfile":{}`) is `Managed { Restricted }`
  upstream, so 0.166 reaches its backend and prints
  `windows sandbox failed: Restricted read-only access requires the elevated
  Windows sandbox backend`, while Go classifies it as no profile and answers
  `only managed permission profiles can be enforced by the Windows sandbox`;
  the same nine payloads in `out/r74-state.txt` show it. Mapping the empty
  legacy profile onto Go's managed path would enter Go's real Windows sandbox,
  which already hangs for `--permission-profile :read-only` on this host, so
  the backend lane has to be fixed first. `sandbox` with a valid state and no
  command is also Go's `sandbox requires COMMAND` where 0.166 runs the empty
  command and either reports `sandbox command must not be empty` (disabled or
  external profile) or the backend error.
- `migrate-rollouts --apply` now matches 0.166 for plain and `.jsonl.zst`
  sources, including the transient/retired records Rust drops and the
  `patch_apply_end` -> `FileChange` conversion (entries 82 and 83,
  `out/r76-events.txt` 5/5, `out/r76-compressed-cases.txt` 6/6) and
  `turn_context` / `compacted` are re-serialized like 0.166 (entry 84,
  `out/r77-turn-context-compacted.txt` 20/20), every legacy completion event is
  converted into its `TurnItem` the way Rust does (entries 85 and 86,
  `out/r78-convert.txt` 44/44 and `out/r79-imagegen.txt` 19/19). What is left on
  this surface is narrower: the nested `turn_context` permission policies are
  now re-serialized like Rust (entry 87, `out/r80-perms.txt` 22/22), and the
  `item_completed` surface now decodes and re-serializes every nested
  `TurnItem` like Rust (entry 88, `out/r85-itemcompleted.txt` 69/69), so a bad
  enum value or a wrong JSON type drops the line instead of surviving.
- `app-server --print-startup-config` writes
  `"appServerVersion":"0.0.0"` where Rust reports its own `0.166.0`. This is
  build-time injection, not a hard-coded value: `doctor.Version()` reads
  `CODEX_GO_VERSION` and `go build -ldflags "-X
  codex_go/doctor.buildVersion=0.166.0"` prints `"0.166.0"`
  (`out/round42-version-*.txt`), so a release build that stamps the version
  matches Rust without a code change.

- Long-flag did-you-mean and the smart usage block now cover every command path
  the 0.166 help tree exposes with options (entries 46 and 47; the audit compares
  the tables against the help pages). What the audit reports next to the tables
  is only Rust's hidden aliases (`--yolo`, `--not-so-yolo`,
  `--experimental-json`), which the tables keep because clap still scores them.
- OS-level error text is now Rust-shaped on the exec surfaces; other commands
  that print `%v` of a `*os.PathError` (for example some `apply_patch` paths)
  still show Go's `open <path>: <English text>` wording.
- clap diagnostics are aligned for the root command, `doctor`, the `exec`
  family and the subcommand surface (unexpected arguments, missing required
  arguments, unrecognized subcommands with did-you-mean, missing values, and the
  nested `sandbox setup` parser). What is still Go-shaped:
  - clap's flag did-you-mean now covers the whole command tree (entries 46 and
    47); see the dedicated Still open bullet for the two remaining details.
  - shared options inside a subcommand (entry 37): `--enable`/`--disable` are
    accepted everywhere and now echoed in argv order, including the exec family
    (entry 49, `reorderUsageOptionRun`). The
    pending-value order is aligned since entry 48 (`codex mcp get -c -x1`
    reports `unexpected argument '-x' found`), and `codex review --config a=b`
    keeps working through the subcommand parser's own `-c` handling, which
    mirrors Rust's merged override order.
  - the missing-required blocks are aligned (entry 40): `execpolicy check`,
    `stdio-to-uds` and `tcp-tunnel` print clap's block with exit 2, including
    the consumed-flag echo in the usage. What remains Go-shaped on those
    surfaces:
    - `codex stdio-to-uds <path>` rejects a non-pipe path on Windows
      (`Error: stdio-to-uds on Windows requires a named pipe path, got
      <path>`), where Rust attempts an AF_UNIX connect and reports
      `failed to connect to socket at <absolute path>` with `Caused by:` and the
      OS error. Aligning needs a Windows AF_UNIX transport in the Go runtime.
    - the `tcp-tunnel` value parsers cover the URL cases a proxy URL can hit
      (relative URL, empty host, invalid port, invalid IPv6); exotic `url`
      crate variants (IDNA, domain characters) fall back to the relative-URL
      text.
  - the help surface is aligned (entry 39): the root command and all 79
    subcommand paths print clap's `-h`/`--help`/`help <topic>` pages plus the
    `--help=<value>` diagnostic, and `codex execpolicy check -hx` now reaches
    clap's missing-required block like Rust.
- The load pass now covers the 104 top-level field types, enums, `features`,
  the `mcp_servers` entries (entry 61), every struct table serde walks
  (entries 62-64), the file reads `Config::load` performs on the three
  path-valued settings (entry 65) and the `model_catalog_json` parse itself
  (entry 67), so a missing instructions or prompt file, a catalog with no
  models and a malformed model entry fail the way 0.166 does.
- `apply_patch` on 2026-10-31: Rust 0.166 (`sandbox = "unelevated"`) refused to
  write under `%TEMP%` (`Access ... is denied`) while Go wrote the file; that
  sandbox-policy difference is still unaligned.
- `/keymap` Debug: Insert and Ctrl+Space reach the Go TUI as the same
  `KeyRunes{0}` event because bubbletea v1.3.10's Windows console reader never
  maps `VK_INSERT` (v1.3.10 is the newest v1 tag; v2 restructures the API). Go
  inspects that event as `ctrl+space`, mirroring Rust's 0x00 -> Ctrl+Space
  normalization, so it cannot also report `insert` / `vim_normal.enter_insert`;
  Rust reads the console key record and prints `insert` (and `ctrl+shift+2`
  for Ctrl+Space). Aligning needs a bubbletea input fork or the v2 input API.

- Rust shows the npm update banner (`0.165.0 -> 0.166.0`); the Go fork has no
  published update channel, so it stays silent.
- `$` ordering differs only because `~/.codex` has `mention_usage.json`
  frequency data and `~/.gcode` does not.
- `codex exec --json` ids are aligned: Go numbers every item from one
  `item_N` counter the way `event_processor_with_jsonl_output.rs::next_item_id`
  does (started/updated reuse their id, a tool item's completion reuses the
  started id, agent messages and warnings always take a fresh id). The
  `rawToExecItemID` map lives on `execEventSink` and only rewrites what the
  JSON encoder prints, so the embedded TUI keeps the raw session ids.
  The command payload is aligned too (entry 26): `command` is the resolved
  shell argv, `metadata` never reaches the JSON stream and `exit_code` is
  always present. The only remaining exec-JSON gap is that Rust repeats the
  startup config warning (upstream `TODO(anp) resolve duplicate startup
  warnings` in `exec/src/lib.rs::should_process_notification`); Go keeps one.
- `← for agents` hint: re-checked against the 0.166 build. Rust 0.166 only
  renders and reacts to it in daemon mode (`out/agents-daemon-rust166.txt`);
  with `--no-daemon` it is absent and inert. Go's `commandCenterAvailable()`
  already carries the newer upstream rule (`#58238`, 2026-10-30, before the
  0.166 branch point), so Go's embedded-mode hint is forward parity, not a
  defect. The local Rust checkout is at `b0f977c3a9` (2026-10-30).

- `completion` shell scripts are still Go's hand-written tables (11.7k, 2.3k,
  6.2k, 12.1k and 2.3k characters) instead of clap_complete's full tree
  emission (243k, 246k, 198k, 273k and 167k for bash, zsh, fish, powershell
  and elvish). Byte parity needs a clap_complete port for all five shells
  plus the whole clap command tree with help texts and value hints; the
  functional coverage (every subcommand and flag) is already present, only
  the generated layout differs.
- `debug models` accepts `--bundled` but ignores it: the flag defaults to
  false, where Rust refreshes the catalog online (or from its cache) and only
  falls back to the bundled copy, while the Go CLI has no online
  models-manager client wired into `runDebug`. The bundled path is now
  byte-identical to 0.166 and the offline fallback serves the same catalog.
- `debug prompt-input` still prints Go's `SessionPrompt` message list (5.9k
  characters: skills instructions, environment context and user text as
  `content` blocks) instead of Rust's `build_prompt_input` output (65k
  characters of `ResponseItem`s: `additional_tools` naming the `functions`
  namespace with `exec` and its JSON schema, then the developer/user items).
  Aligning means serialising the tool definitions and the prompt-assembly
  order the core session builder produces.
- `doctor` still differs from 0.166 outside the Environment work in entries
  92-93: the disk and state rows keep Go's `GCODE_HOME` label, a source build
  reports `other` instead of Rust's package layout and `v0.0.0` for the version,
  `search` prefers the system ripgrep over the packaged one, the Desktop App
  group still reports Go's own installation shape instead of 0.166's
  `version`/`running`/`log directory` rows plus its security and update
  summaries (see `out/r1304-doctor-full-diff.txt`).
