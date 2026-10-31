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

## Verification

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

## Still open

- `codex resume -i a.png b.png` (TUI picker path) also uses Rust's variadic
  `--image`; Go's session parser was left single-value pending a pty capture.
- OS-level error text is now Rust-shaped on the exec surfaces; other commands
  that print `%v` of a `*os.PathError` (for example some `apply_patch` paths)
  still show Go's `open <path>: <English text>` wording.
- clap diagnostics are aligned for the root command, `doctor`, the `exec`
  family and the subcommand surface (unexpected arguments, missing required
  arguments, unrecognized subcommands with did-you-mean, missing values, and the
  nested `sandbox setup` parser). What is still Go-shaped:
  - clap's flag did-you-mean: `codex delete --name x --force` prints
    `tip: a similar argument exists: '--enable'` and echoes that flag in the usage
    (`Usage: codex delete --enable <FEATURE> <SESSION>`); Go prints the plain
    `--` tip. Aligning needs the per-command long-flag table and clap's used-flag
    usage rendering.
  - shared options inside a subcommand (entry 37): `--enable`/`--disable` are
    accepted everywhere and rendered in the usage block, but the hand-written
    `exec` family usage lines (`exec resume --last --enable f --bogus`,
    `exec fork --enable f`) do not echo them yet - Rust prints
    `--last --enable <FEATURE>` / `--enable <FEATURE> <SESSION_ID>`. A pending
    value followed by an *unknown* flag (`codex mcp get -c -x1`) also still
    reports the pending value, where clap reports `unexpected argument '-x'
    found` first, because Go has no per-command flag table to test the token
    against. `codex review --config a=b` keeps working through the subcommand
    parser's own `-c` handling, which mirrors Rust's merged override order.
  - three subcommands still answer missing-required arguments in Go
    wording instead of clap's block, and exit 1 rather than 2: `execpolicy`
    (`Error: execpolicy requires a subcommand`, Rust prints clap's
    `requires a subcommand but one was not provided` with `[subcommands: check,
    help]`), `execpolicy check` (`requires --rules PATH` vs clap's
    `--rules <PATH>` + `<COMMAND>...`), `stdio-to-uds` (`requires exactly one
    SOCKET_PATH`, and on Windows it validates the path before clap would reject
    an unknown flag) and `tcp-tunnel` (`requires a value`-style errors plus a
    single missing option instead of `--proxy-url`/`--proxy-origins-file`/
    `--target`). Those parsers live outside `cli.Parse`'s table-driven surface,
    so they need their own usage snapshots before they can print clap blocks.
  - the eight group pages are aligned (entry 38); every *other* command still
    rejects `-h`/`--help` with `unexpected argument '--help' found`. That covers
    the leaf commands (`codex mcp get --help`, `codex features list --help`,
    `codex cloud exec --help`, ...), the non-group top-level commands
    (`codex cloud`, `codex app-server`, `codex sandbox`, `codex agents`,
    `codex resume`, `codex update`, ...) and `codex <group> help <topic>`
    (`codex mcp help get`), which Rust answers with that leaf's long page.
    `cli/group_help.go` shows the capture-and-embed pattern; the remaining work
    is generating the same short/long pairs for those commands and routing them
    in their parsers.
  - `codex mcp add --oauth-client-registration <AUTO|CIMD|DCR>` is a Rust flag Go
    does not parse yet, and clap treats `--user=` as a satisfied (empty) value for
    the nested `setup` parser where Go reports the missing group.
- `apply_patch` on 2026-10-31: Rust 0.166 (`sandbox = "unelevated"`) refused to
  write under `%TEMP%` (`Access ... is denied`) while Go wrote the file; that
  sandbox-policy difference is still unaligned. Go also still appends
  `: not a regular file` to `Failed to delete file <path>` for non-regular
  targets, where Rust prints only the context line.

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
