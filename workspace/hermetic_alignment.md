# Test-environment isolation (`align/hermetic`)

Baseline `e68103f1`. Worktree `codex_go_lane_hermetic`, branch `align/hermetic`.

Three rounds in a row were lost to the same class of bug: a test that reads the
ambient environment and asserts as if the host were configured one particular
way. A Linux-only `PATH` fixture, a developer's `SSH_CONNECTION`, and
`NO_COLOR`/`WT_SESSION` on the build box all produced failures that could not be
reproduced elsewhere. This lane fixes the known instances, replaces the
guesswork with a semantic port of the Rust decision function, and adds a
re-runnable hostile-environment gate.

## 1. Colour detection

### Ruling: `NO_COLOR=""` counts as *present*

Rust does not implement the NO_COLOR convention directly. `stdout_color_level()`
(`codex-rs/tui/src/terminal_palette.rs:14-21`) delegates to the `supports-color`
crate, pinned at 3.0.2 in `codex-rs/Cargo.toml:521`, and
`effective_stdout_color_level()` (`terminal_palette.rs:43-55`) post-processes that
answer with terminal promotion (`terminal_palette.rs:57-74`).

`supports-color-3.0.2/src/lib.rs:54-59`:

```rust
fn env_no_color() -> bool {
    match as_str(&env::var("NO_COLOR")) {
        Ok("0") | Err(_) => false,
        Ok(_) => true,
    }
}
```

- absent → not disabled,
- `"0"` → not disabled,
- **everything else, including the empty string → disabled**.

So `NO_COLOR=""` is *set* and disables colour. Go read it with `os.Getenv` and
tested `!= ""`, which made an empty value look unset - the exact opposite. The
subtest called `empty_NO_COLOR_is_unset` therefore asserted a false premise; it
passed only on hosts without `WT_SESSION` and went red wherever the variable was
set, because the truecolor promotion runs *after* the supports-color verdict.
Name and expectation were both wrong; both are gone.

### Other divergences found while porting

`FORCE_COLOR` (`supports-color/src/lib.rs:36-50`): `"true"` and `""` mean level 1,
`"false"` means 0, anything else is `min(parse.unwrap_or(1), 3)`, and
`CLICOLOR_FORCE` (`!= "0"` → level 1) is the fallback. Levels are capped at 3.
Force is evaluated *before* NO_COLOR, so force wins.

`supports_color()` (`lib.rs:90`) then applies: force > 0, else
(`env_no_color() || TERM=="dumb" || !(isatty || IGNORE_IS_TERMINAL != "0")`) → 0,
else the 16m / 256 / ANSI heuristic ladder over `COLORTERM`, `TERM`,
`TERM_PROGRAM`, `CLICOLOR`.

| situation | Rust (supports-color 3.0.2) | Go before | Go now |
| --- | --- | --- | --- |
| `NO_COLOR` unset | detect normally | detect normally | detect normally |
| `NO_COLOR=""` | disabled | **not disabled** | disabled |
| `NO_COLOR="0"` | not disabled | **disabled** | not disabled |
| `NO_COLOR` any other value | disabled | disabled | disabled |
| `FORCE_COLOR=""` | ANSI-16 | **ignored** | ANSI-16 |
| `FORCE_COLOR="true"` | ANSI-16 | **truecolor** | ANSI-16 |
| `FORCE_COLOR="false"` / `"0"` | 0, then detect normally | disabled | detect normally |
| `FORCE_COLOR="9"` | capped to truecolor | ignored | truecolor |
| `FORCE_COLOR="garbage"` | ANSI-16 | ignored | ANSI-16 |
| `CLICOLOR_FORCE` non-`"0"` | ANSI-16 | **ignored** | ANSI-16 |
| `TERM="dumb"` | disabled | disabled | disabled |
| stdout not a tty | disabled | termenv-dependent | disabled |
| `IGNORE_IS_TERMINAL != "0"` | overrides the tty gate | **ignored** | honoured |
| `WT_SESSION` present | truecolor unless `FORCE_COLOR` present | keyed off the *value* | keyed off presence |
| `TERM_PROGRAM=Windows_Terminal` + ANSI-16 | truecolor unless `FORCE_COLOR` present | keyed off the value | keyed off presence |

Two further notes:

- The `WT_SESSION` promotion is **not** Windows-gated in Rust
  (`terminal_palette.rs:63` runs for every platform). Go had
  `runtime.GOOS == "windows"` in front of it; removed.
- Rust quirk, deliberately mirrored: `NO_COLOR=1` **plus** `WT_SESSION` yields
  truecolor, because promotion runs after the verdict. Any test that asserts a
  NO_COLOR outcome must leave `WT_SESSION` absent - that is precisely why the
  old subtest flipped between machines.

### Implementation

`tui/terminal_palette.go` now carries a field-by-field port of
`supports_color()` with the environment lookup (`envLookup`, `os.LookupEnv` based)
and the tty probe injected through `detectStdoutColorLevelFor(lookup, isTTY)`.
`DetectStdoutColorLevel()` keeps its process-wide cache and passes the real
sources. `termenv.ColorProfile()` no longer participates in the decision, so
detection no longer depends on whether `go test` happens to run with a pipe.

Known delta: supports-color also consults the `is-ci` crate in its last branch.
Go has no equivalent dependency; the branch is unreachable without a tty and is
documented in the source rather than guessed.

### User-visible behaviour changes (intentional, Rust-aligned)

- `NO_COLOR=""` now disables colour.
- `NO_COLOR="0"` no longer disables colour.
- `FORCE_COLOR=0` no longer disables colour (it only cancels `CLICOLOR_FORCE`).
- `FORCE_COLOR=""` now forces ANSI-16.

Anyone who relied on `FORCE_COLOR=0` as a kill switch should use `NO_COLOR=1`,
which behaves identically in Rust and Go.

### Tests

| test | covers |
| --- | --- |
| `TestSupportsColorLevelLikeRust` | 36 cases through the seam: presence vs absence, force parsing, tty gate, heuristic ladder, WT/Windows Terminal promotion |
| `TestDetectStdoutColorLevelRespectsProcessEnv` | the real entry point with `isolateColorEnvForTest` + `t.Setenv` (tty simulated with `IGNORE_IS_TERMINAL=1`) |
| `TestDetectStdoutColorLevelPromotesWindowsTerminalLikeRust` | replaces the old test, which asserted truecolor for `FORCE_COLOR=""` - itself wrong vs Rust - and no longer needs a Windows-only skip |
| `TestAnsiColorForTermMatchesPlatformLikeRust` | `check_ansi_color()`, whose "TERM unset" answer differs per platform |

`unsetEnvForTest` exists because `t.Setenv(key, "")` cannot express "absent", and
absent vs empty is the whole point here.

## 2. Repo-wide sweep

### Method

1. Static: every `os.Getenv` / `os.LookupEnv` in `*_test.go`, cross-checked
   against a `t.Setenv("<same var>")` in the same file.
   Result: **244 reads**, 19 matched that (weak) isolation signal, **119 did
   not**.
2. Empirical: the hostile-environment gate below, run over `./tui/...`,
   `./app/...`, `./appserver/...`. Static analysis cannot tell "reads env in
   order to skip" from "reads env as part of an assertion", so only this run
   decides what is actually broken.

### Inventory, grouped by package

Verdicts: **fixed** = changed in this lane; **sentinel** = child-process
handshake variable that only the re-exec'd helper reads; **opt-in** = the test
skips or configures itself when unset; **os-path** = used to locate OS
executables/directories; **self-consistent** = reads the parent value and
forwards/compares that same value; **toolchain** = needs a real shell/Chrome and
skips when absent.

| package | reads | variables | verdict |
| --- | --- | --- | --- |
| tui | 6 | `CODEX_RUST_ROOT`, `CODEX_CHROME_PATH`, `CODEX_TERM_PROGRAM_HELPER_EXIT`, `LOCALAPPDATA`, `ProgramFiles`, `ProgramFiles(x86)` | opt-in / os-path / sentinel |
| tui (colour) | 2 | `NO_COLOR`, `WT_SESSION`, `FORCE_COLOR`, `TERM`, `COLORTERM` | **fixed** (section 1) |
| tea | 29 | `APPDATA`, `CODEX_GO_EXE`, `CODEX_RUST_EXE`, `CODEX_SYSTEM_EXE`, `CODEX_TUI_ARTIFACT_DIR`, `CODEX_TUI_SIDE_IMPL`, `CODEX_TUI_WEATHER_ARTIFACT_DIR`, `CODEX_STABILITY_*`, `ComSpec` | os-path / opt-in |
| tea (viewed image) | 1 | `SSH_CONNECTION` (via the model's clipboard probe) | **fixed** |
| mcp | 17 | `GO_WANT_MCP_HELPER`, `MCP_*_HELPER`, `MCP_*_FILE`, `MCP_STDIO_AUTH_CHANGE_OPT_IN`, `PATH` | sentinel / opt-in / self-consistent |
| windowssandbox | 14 | `CODEX_WINDOWS_SANDBOX_SMOKE*`, `CODEX_WINDOWS_SANDBOX_TOKEN_MATRIX`, `CODEX_HOME`, `ComSpec`, `PATH`, `SystemRoot`, `TEMP` | opt-in / os-path |
| appserver | 8 | `APPSERVER_MCP_*_FILE`, `CODEX_GO_UNIFIED_EXEC_BACKGROUND_HELPER`, `CODEX_RUST_ROOT`, `CODEX_RUST_APP_SERVER_*` | sentinel / opt-in |
| state | 6 | `CODEX_GO_SQLITE_WAL_HELPER`, `CODEX_GO_SQLITE_WAL_PATH`, `CODEX_EXPECT_GOOS`, `CODEX_EXPECT_GOARCH`, `CODEX_STABILITY_*` | sentinel / opt-in |
| tool | 5 | `PATH`, `PIPE_HELPER_EXTRA_FD`, `CODEX_THREAD_ID`, `CODEX_TOOL_CALL_ID` | toolchain / sentinel |
| execserver | 5 | `CODEX_*_HELPER`, `PATH`, `ComSpec` | sentinel / toolchain |
| parity | 4 | `CODEX_RUST_ROOT`, `CODEX_RUST_APPLY_PATCH_BIN`, `APPLY_PATCH_DIFF_*` | opt-in |
| scenarios / turn / memories | 10 | `CODEX_STABILITY_*` | opt-in |
| voicehost | 3 | `CODEX_VOICE_*` | opt-in |
| windowssandboxservice | 2 | `LOCALAPPDATA` | os-path |
| app | 2 | `CODEX_HOME`, `INTERACTIVE_MCP_STARTUP_BLOCK_HELPER` | os-path / sentinel |
| linuxsandbox | 2 | `PATH`, `HTTP_PROXY` | toolchain / skips when unset |
| cli / protocol / sandbox / recordreplay / execpolicy / win | 6 | `CODEX_RUST_ROOT`, `RECORDREPLAY_UPDATE_GOLDEN`, `SystemDrive`, `USERNAME` | opt-in / os-path |

### Fixes

| item | class | change |
| --- | --- | --- |
| `TestDetectStdoutColorLevel*`, `TestSupportsColorLevelLikeRust` | seam + isolation | injected `envLookup` and tty probe; `unsetEnvForTest`/`isolateColorEnvForTest` for the one test that goes through the real process environment |
| `TestViewedImageRowLinksLocalFileLikeRust` | seam | the helper pins `Options.ClipboardEnvironment`; the SSH case passes `SSHSession` instead of mutating `SSH_CONNECTION` |
| `tui/primary_selection_linux_test.go` | isolation | fixture directory prepended to `PATH` instead of replacing it, so the fixture scripts keep `cat`; the "unavailable" test keeps an isolated `PATH` |

Distribution: **2 seam, 1 isolation, 0 skip.** No assertion was relaxed.

### Hostile-environment gate

`scripts/test_hostile_env.ps1` (+ `.sh`) exports `NO_COLOR=1`, `WT_SESSION=x`,
`SSH_CONNECTION=...`, `SSH_TTY=/dev/pts/9`, `TERM=dumb`, `FORCE_COLOR=1`,
`CLICOLOR_FORCE=1`, `CLICOLOR=1`, runs the key packages, prints a PASS/FAIL line
per package with the failing test names, restores the caller's environment and
exits non-zero on any failure.

Result at `e68103f1`+fixes:

```
==> go test ./tui/...      FAIL   (--- FAIL: TestModelTerminalSnapshots/main_view)
==> go test ./app/...      PASS
==> go test ./appserver/... PASS
```

The single failure is the known worktree-path snapshot: the golden expects
`D:\qax\reagent\dev\codex_go\tui\tea` and the render emits
`D:\qax\reagent\dev\codex_go_lane_hermetic\tui\tea`. It fails identically
without the hostile variables (`TestModelTerminalSnapshots/main_view` and
`TestModelVT100TerminalSnapshotMainView` both), so it is an environment fixture,
not a regression. **New failures under the hostile environment: 0.**

## 3. Verification

| command | result |
| --- | --- |
| `go build ./...` | exit 0 |
| `GOOS=windows go build ./...` | exit 0 |
| `go vet ./tui/ ./tui/tea/` | exit 0 |
| `GOOS=linux go vet ./tui/` | exit 0 (compiles the Linux-only primary-selection test) |
| `go test ./tui/... -count=1` | all packages ok except the two worktree-path snapshots in `./tui/tea` |
| `go test ./tui/ -count=1` with `NO_COLOR=1` / unset / `WT_SESSION=x` / `TERM=dumb` / `FORCE_COLOR=0..3` | 8/8 green |
| `scripts/test_hostile_env.ps1` | `./app/...` and `./appserver/...` green, `./tui/...` only the known snapshot |
| `go test ./tui/tea/ -count=1` x5, 2 of them under concurrent load | clipboard test green in all 5; only the two known snapshots fail |
| `go test ./tui/tea/ -count=1 -skip <the two snapshots>` x5, 2 under load | 5/5 exit 0 |

## 4. Remaining risk

- The Linux-only primary-selection tests could not be executed here (no WSL
  distro on this box). The change is a superset of the old `PATH` (fixtures
  first), and `GOOS=linux go vet ./tui/` compiles it, but an actual Linux run is
  still required before release.
- Colour behaviour changed for `NO_COLOR=""`, `NO_COLOR="0"`, `FORCE_COLOR=""`
  and `FORCE_COLOR=0`. This is Rust parity, but it is user-visible; release notes
  should mention `NO_COLOR=1` as the way to force plain output.
- The `is-ci` branch of supports-color is not ported (documented in
  `tui/terminal_palette.go`). Only observable on a CI runner with a tty.
- The static inventory covers `*_test.go` reads. Production env reads exercised
  only indirectly are covered by the hostile gate for `tui`/`app`/`appserver`,
  not for `mcp`, `tool`, `execserver` or the sandbox packages - those suites are
  slow and partly require elevated privileges, so they stay out of the default
  gate; pass `-Packages` to include them.
- `tui/tea` PTY tests read `APPDATA`, `ComSpec` and `CODEX_*_EXE` to locate real
  executables. They are os-path lookups rather than assertions, and they passed
  the hostile run, but they remain machine-shaped.
- Timing fragility is a separate class from environment leakage. The bucket-C
  settle waits and the 3 tight upper bounds in section 5 are recorded, not
  fixed; they will flake under heavy load the same way the clipboard reader test
  did until each one is converted to a marker wait.

## 5. Timing fragility (the third class)

Not every non-deterministic failure is an environment dependency or a real
regression. `TestClipboardCommandReaderReusesProcessLikeRust` failed once in a
full-package run on `main=1b100261` (7.09s), passed alone (0.05s), and passed on
a verbose full re-run. That is timing, not environment.

### Case study

7.09s is the fingerprint of the bug: the first framed read burns its whole 5s
deadline in `waitClipboardRead`, then `defer reader.Close()` spends its 2s grace
waiting for `done`.

Two races were hidden behind `time.Sleep(50 * time.Millisecond)`:

1. `Warm()` is a **non-blocking** send into a **one-slot** channel. If the run
   loop has not dequeued it yet, the following `readUntil` finds the channel full
   and returns `errClipboardReaderBusy` immediately - a second, also
   load-dependent failure mode.
2. The helper is a re-exec of the test binary (`os.Args[0]` with
   `-test.run=^TestClipboardReaderHelperProcess$`), and process start happens
   inside the run loop *after* the caller has already fixed its absolute
   deadline (`readUntil(time.Now().Add(...))`). On a loaded machine the 5s budget
   is spent spawning instead of reading.

Rust evidence that the product timeout is right and must **not** be widened:

- `codex-rs/tui/src/clipboard_copy/worker.rs:22`:
  `const SETUP_TIMEOUT: Duration = Duration::from_secs(/*secs*/ 5);`
- `read_text()` computes `let deadline = Instant::now() + SETUP_TIMEOUT;` at
  *request* time, so process setup is deliberately inside the budget, and
  `take_text_result()` reports `"clipboard read timed out"` when
  `Instant::now() >= deadline`.
- Go already matches: `RightClickPasteReadTimeout = 5 * time.Second`
  (`tui/app/right_click_paste.go:215`).

So the fix belongs in the test. `newTestClipboardCommandReader` now returns a
`started` channel that the start function closes when `startClipboardProcess`
returns; the test waits on that event instead of sleeping, which removes both
races at once. The per-read budget became `clipboardReaderTestReadDeadline`
(30s) with a comment stating it is a test budget and not the product deadline.
`TestClipboardCommandReaderTimesOutLikeRust` keeps pinning the 5s behaviour, and
now also waits for the start so the 50ms deadline measures the sleeping read
rather than a slow spawn.

### Sweep

Repo-wide there are **334** `time.Sleep` calls in `*_test.go` across **176 files
and 30 packages**, plus **66** `time.Since` timing assertions (22 upper bounds,
7 `>=` timeout-not-honoured checks, 9 lower bounds). The distribution is skewed:
`appserver` 139, `tui/tea` 47, `execserver` 27, `mcp` 19, `tool` 14.

Buckets: **A 保持（已是事件轮询）**, **B 保持（夹具延迟/清理宽限）**,
**C 可改成事件等待**, **D 需要放宽（紧上界断言）**.

`tui/tea` + `tui` (the lane's own area, every site inspected):

| site | wait | purpose | bucket |
| --- | --- | --- | --- |
| `tui/tea/analytics_dashboard_like_rust_test.go:310` | 2ms | deadline poll loop | A |
| `tui/tea/clipboard_worker_like_rust_test.go:39` | 5ms | deadline poll loop | A |
| `tui/tea/composer_selection_pty_windows_test.go:98` | 50ms | deadline poll loop | A |
| `tui/tea/program_mouse_wheel_like_rust_test.go:47` | 10ms | `waitForProgramOutput` poll | A |
| `tui/tea/side_navigation_pty_windows_test.go:368` | 100ms | deadline poll loop | A |
| `tui/tea/terminal_restore_pty_windows_test.go:453` | 25ms | exit poll helper | A |
| `tui/tea/*_pty_windows_test.go:39/40/89` | 500ms | `t.Cleanup` grace before TempDir removal | B |
| `tui/tea/clipboard_reader_like_rust_test.go:41` | 2s | helper latency under test | B |
| `tui/tea/collaboration_modes_discovery_like_rust_test.go:100` | 250ms | fake slow provider | B |
| `tui/tea/analytics_*_like_rust_test.go:29/138` | 1-2ms | fake async loader | B |
| `tui/get_git_diff_test.go:345`, `tui/transcript_reflow_test.go:17` | skew/1ms | fake loader ordering | B |
| `tui/tea/clipboard_reader_like_rust_test.go:78` | 50ms | warmup guess | **C - fixed** |
| `tui/tea/composer_selection_pty_windows_test.go:104/110/112/114/126` | 300/50/50/500/500ms | settle between injected mouse/typing events | C |
| `tui/tea/rendered_selection_pty_windows_test.go:95/97/99` | 50/50/400ms | settle between mouse events | C |
| `tui/tea/tui_mode_picker_pty_windows_test.go:146/156/165/174` | 100ms | settle after closing a picker | C |
| `tui/tea/program_mouse_wheel_like_rust_test.go:129/134` | 100/200ms | "let the first frame lay out", then hit-test | C |
| `tui/tea/pet_runtime_test.go:126/128/298/301` | 20/400/20/500ms | let the program loop process before Quit | C |
| `tui/tea/composer_sparkle_pty_windows_test.go:97` | 1200ms | settle | C |
| `tui/tea/welcome_blossom_pty_windows_test.go:97` | 700ms | settle | C |
| `tui/tea/multi_agent_parity_pty_windows_test.go:151`, `weather_parity_pty_windows_test.go:143` | 1s | settle | C |
| `tui/tea/side_shortcut_pty_windows_test.go:118/123/191` | 1s/3s/500ms | settle | C |
| `tui/tea/slash_parity_pty_windows_test.go:141/146/148` | 3s/750ms/2s | settle between slash commands | C |
| `tui/tea/side_navigation_pty_windows_test.go:262` | 750ms | settle after Ctrl+/ | C |
| `tui/tea/terminal_restore_pty_unix_test.go:54` | 150ms | settle before asserting restore | C |
| `tui/tea/tui_mode_picker_pty_windows_test.go:201` | 50ms | shutdown helper | C |
| `tui/terminal_exit_drain_test.go:78/94` | >1s | drain bound | D |

Conversion recipe for bucket C: these files already accumulate PTY output in a
buffer and several already have a `wait(...)` / `waitForProgramOutput(...)` /
`waitSince(...)` helper. Replace `time.Sleep(d); snapshot()` with
`waitForX(output, marker, deadline)` for the *next* marker the app emits after
the input, which removes the guess without weakening the assertion. Where no
marker exists (`program_mouse_wheel_like_rust_test.go:129` wants "first frame
laid out"), the render-complete signal has to be added to the program harness
first.

### Tight upper bounds (bucket D)

| site | bound | what it means | recipe |
| --- | --- | --- | --- |
| `mcp/api_test.go:314` | 25ms | "second call did not restart the 50ms optional-startup grace" | raise `OptionalStartupGrace` to a few seconds and assert `< 1s`; a regression would then take the full grace and still be distinguishable |
| `tool/unified_exec_output_drain_test.go:56` | 100ms | "closed reader does not consume the 1s drain grace" | assert `< 500ms` against the existing 1s grace |
| `app/app_test.go:2156` | 500ms | "no startup probe before the TUI" | the blocked helper sleeps far longer than 1s; widen to `< 2s` |
| other 19 `elapsed > N` sites | 1-5s | already generous upper bounds | keep |

Lower bounds (`elapsed < X` / `elapsed >= X` verifying a timeout was honoured)
are load-safe - load only makes an interval longer - so they stay.

None of the bucket C/D items were rewritten this round: each lives in a package
whose full suite would have to be re-run, and none of them is currently
reproducing. They are recorded here so the next flake has a map instead of a
guess.

### Acceptance

```
5x go test ./tui/tea/ -count=1            (runs 2 and 4 under concurrent load)
run 1: exit=1 52.6s  fails: TestModelTerminalSnapshots/main_view, TestModelVT100TerminalSnapshotMainView
run 2: exit=1 61.0s  fails: same (load: go build ./...)           load exit=0
run 3: exit=1 50.2s  fails: same
run 4: exit=1 64.9s  fails: same (load: go test ./tool/)          load exit=0
run 5: exit=1 52.4s  fails: same
```

The two failures are the known worktree-path golden snapshots (section 2); the
clipboard test passed in all five. Same loop with those two fixtures skipped
(`-skip "TestModelTerminalSnapshots|TestModelVT100TerminalSnapshotMainView"`):

```
run 1: exit=0 48.7s  run 2: exit=0 50.4s (load: go build ./...)  run 3: exit=0 51.5s
run 4: exit=0 49.3s (load: go test ./appserver/ -run Schema)     run 5: exit=0 56.3s
```

5/5 green, 2/5 under concurrent load.

## Commits

1. `f9746643` align rust #46897: port supports-color env semantics for colour detection
2. `cb7e6fcf` tests: pin the clipboard environment for viewed-image hyperlink rows
3. `ec10a6e3` tests: stop the primary-selection fixtures from hiding coreutils
4. `1acb884c` scripts: add a hostile-environment test runner
5. `08c9bd41` tests: wait for the clipboard helper to start instead of sleeping 50ms
