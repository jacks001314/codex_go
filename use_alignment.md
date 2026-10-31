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

## Verification

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

- Override diagnostics the 0.166 binary still answers from its own runtime:
  `remote-control --config=` reports the daemon version mismatch instead of
  Rust's `foreground app-server exited before remote control became ready`
  chain, `update --config=` prints Go's install-detection wording, and
  `fork`/`resume`/`agents` report the override before Rust's TERM gate.
  `sandbox --config= --sandbox-state-json=` also reports the override where
  Rust parses the state JSON first, because Go's `--sandbox-state-json` text is
  still `invalid sandbox run request: ...` rather than serde's
  `invalid --sandbox-state-json value: ...` wording.
- `migrate-rollouts --apply` still reports `bytes_processed: 0` where Rust
  counts every byte its canonicalizer read and wrote through the rate limiter
  (2,827 for a 282-byte legacy rollout), and the paginated rollout Go publishes
  is not byte-identical: Rust writes the synthetic `task_started`/`task_complete`
  events with the record timestamp and fills `session_id`/`base_instructions` in
  `session_meta`, while Go stamps the synthetic events with the current time and
  omits those optional fields (879 vs 1,077 bytes for the same input). The
  `Disk used for thread storage` line follows Rust's rule but not its numbers,
  because Rust leaves the thread-history WAL uncheckpointed while Go checkpoints
  it (290.2 KB vs 88.6 KB in the same probe).
- `exec-server forward` still rejects `--remote` outright (pinned by
  `TestExecServerRejectsMissingOrConflictingRemoteOptionsLikeRust`), where Rust
  allows the direct-transport combination; the Go forward runtime has no
  remote-registration path.
- `queue --thread "" --message hi` parses in both CLIs, but Rust fails later with
  `No active session found matching ''` while Go reports `Error: SESSION is
  required`.

- `codex resume`/`codex fork` without a TTY: Rust refuses to start the TUI
  (`ERROR: TERM is set to "dumb". Refusing to start the interactive TUI ...`)
  before it resolves the session, while Go runs headless and reports
  `No sessions found to resume.`, `Session not found: <id>` or
  `Error: thread not found`. The argument surface itself now matches, so the
  remaining gap is the TUI start gate in the non-interactive path.
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
- `codex mcp add` writes the same values but a different config.toml shape:
  Rust inserts `mcp_servers` as an implicit table and writes each server's
  fields in declaration order (`command`, `args`, `env`), while Go emits an
  empty `[mcp_servers]` header and sorts keys alphabetically. Both parse back
  identically; aligning needs implicit-table support and stable field order in
  the config writer.
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
