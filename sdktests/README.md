# Codex SDK Parity Tests

This project drives Rust Codex and codex_go through the same local TypeScript SDK.
It records raw SDK event streams, normalizes volatile fields, and compares the
observable contract rather than exact model prose.

Build the Go CLI with its adjacent Code Mode host before live tool or lifecycle
scenarios. The release build script emits both binaries into the same directory:

```powershell
.\scripts\build.ps1 -Output sdktests\.tmp\bin\codex-go.exe -CGO off
```

Passing a standalone `codex-go.exe` without `codex-code-mode-host.exe` beside it
causes Code Mode to fail closed and invalidates tool/resume results.

Run a minimal live smoke:

```powershell
npm --prefix sdktests run test:smoke -- --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

Platform-specific shell commands live in `src/platform/linux.ts` and
`src/platform/windows.ts`. Common prompts, schemas, and observable contracts
remain in `src/scenarios.ts`. The live runner records the selected platform
suite and scenario variant in `run-manifest.json`; an explicit mismatched
`--platform linux|windows` is rejected.

Linux example:

```sh
npm --prefix sdktests run test:parity -- --platform linux --scenario workspace-structured-read --rust /usr/local/bin/codex --go /path/to/codex-go --sdk /path/to/codex/sdk/typescript
```

Replay the latest saved artifact without another model call:

```powershell
npm --prefix sdktests run report
```

Run the model-free raw app-server paginated fork audit. This is a protocol
audit rather than an SDK parity scenario because the TypeScript SDK does not
expose `thread/fork`:

```powershell
npm --prefix sdktests run test:raw:fork -- --rust <rust-codex> --go <go-codex.exe>
```

Run the model-free raw app-server `thread/goal/*` protocol audit (goal set/get/
clear lifecycle, validation errors, ephemeral forks, and the goals feature
gate). This is a protocol audit because the TypeScript SDK does not expose the
app-server goal methods:

```powershell
npm --prefix sdktests run test:raw:goal -- --rust <rust-codex> --go <go-codex.exe>
```

Run the full rolled-fork/ephemeral-side lifecycle audit and the bidirectional
Rust/Go persisted-session compatibility audit:

```powershell
npm --prefix sdktests run test:raw:rolled-fork -- --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
npm --prefix sdktests run test:raw:session-compat -- --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

Audit managed feature requirements directly through the raw app-server
protocol. This remains outside the SDK parity matrix because the TypeScript SDK
does not expose `configRequirements/read`:

```powershell
npm --prefix sdktests run test:raw:requirements -- --rust <rust-codex> --rust-app-server <rust-codex-app-server> --go <go-codex.exe>
```

The Rust app-server must be a debug build because the upstream managed-config
fixture hook is intentionally compiled out of release builds.

Run one expanded scenario or the approved matrix:

```powershell
npm --prefix sdktests run test:parity -- --scenario workspace-structured-read --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
npm --prefix sdktests run test:parity -- --all --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

Start an approved matrix at a named scenario, or resume an interrupted suite:

```powershell
npm --prefix sdktests run test:parity -- --all --from persistent-resume --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
npm --prefix sdktests run test:parity -- --resume <suite-directory> --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

Each matrix writes `suite-summary.json`. Resume verifies the platform and the
Rust, Go, and SDK hashes, marks a previously running scenario incomplete, and
skips scenarios already completed. Each scenario artifact also has a
`run-state.json` with its running, completed, or incomplete state.

Transient provider and worker failures can be retried per scenario without
hiding their evidence:

```powershell
npm --prefix sdktests run test:parity -- --all --infra-retries 3 --fail-fast-infra --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

The default is zero retries. Only `infra_failure` results are retried;
behavior mismatches are final. Every attempt and artifact is retained in the
suite summary, with a 15/30/60-second capped exponential delay between retries.
With `--fail-fast-infra`, exhausting those retries stops the suite immediately
and leaves its remaining scenarios pending instead of spending more live calls.

Only one live runner can own the repository-wide lock. A second runner exits
without disturbing the active process. Use `--recover-lock` only when the
recorded owner is known to be stuck; recovery terminates that recorded process
tree before acquiring the lock. Unreadable lock ownership is never removed
automatically.

Use `--order go-rust` to alternate which implementation runs first during repeated sampling.

For reproducible targeted reruns, pin the same model settings for both
implementations without changing the host `config.toml`:

```powershell
npm --prefix sdktests run test:parity -- --scenario persistent-resume --model <account-supported-model> --reasoning-effort high --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

Pinned model settings are recorded in artifacts and suite identity, so a suite
cannot be resumed with different values.

The approved matrix covers single-turn streaming, schema-constrained workspace
reading through the shell, and persisted thread resume across CLI processes.

Scenarios marked `optIn` are excluded from `--all`. Run them explicitly with
`--scenario`; they may require interactive approval or elevated Windows sandbox
setup that is unsuitable for an unattended matrix.

The Windows-only screen capture scenario exercises the complete live path from
desktop capture through `view_image` inspection to a structured description:

```powershell
npm --prefix sdktests run test:parity -- --scenario windows-screen-capture-description --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

MCP function-listing scenarios drive the same Chinese prompts through the
configured ida/angr MCP servers against the `../test/bwrap` ELF fixture
(copied into each isolated implementation directory so `../test/bwrap`
resolves from the fixture workspace). The three scenarios are
`mcp-list-funcs-generic`, `mcp-list-funcs-ida`, and `mcp-list-funcs-angr`.
They require the local MCP servers to be reachable; the runner pre-flights
the `[mcp_servers.*]` URLs from the user config before spending model calls
and fails fast as an infrastructure failure when a server is down.

```powershell
npm --prefix sdktests run test:parity -- --scenario mcp-list-funcs-ida --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
npm --prefix sdktests run test:parity -- --scenario mcp-list-funcs-angr --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

The CLI exits with `0` for parity, `1` for a behavior mismatch, and `2` for an
infrastructure failure.

The live runner intentionally does not pass an explicit model or reasoning
effort to the SDK. It uses the current Codex config copied into isolated
`CODEX_HOME` directories for each implementation.
