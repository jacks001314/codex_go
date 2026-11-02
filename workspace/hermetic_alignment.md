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

## Commits

1. `f9746643` align rust #46897: port supports-color env semantics for colour detection
2. `cb7e6fcf` tests: pin the clipboard environment for viewed-image hyperlink rows
3. `ec10a6e3` tests: stop the primary-selection fixtures from hiding coreutils
4. `1acb884c` scripts: add a hostile-environment test runner
