# Codex SDK Parity Tests

This project drives Rust Codex and codex_go through the same local TypeScript SDK.
It records raw SDK event streams, normalizes volatile fields, and compares the
observable contract rather than exact model prose.

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

Run one expanded scenario or the approved matrix:

```powershell
npm --prefix sdktests run test:parity -- --scenario workspace-structured-read --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
npm --prefix sdktests run test:parity -- --all --rust <rust-codex> --go <go-codex.exe> --sdk D:\qax\reagent\dev\git\codex\sdk\typescript
```

Use `--order go-rust` to alternate which implementation runs first during repeated sampling.

The approved matrix covers single-turn streaming, schema-constrained workspace
reading through the shell, and persisted thread resume across CLI processes.

Scenarios marked `optIn` are excluded from `--all`. Run them explicitly with
`--scenario`; they may require interactive approval or elevated Windows sandbox
setup that is unsuitable for an unattended matrix.

The CLI exits with `0` for parity, `1` for a behavior mismatch, and `2` for an
infrastructure failure.

The live runner intentionally does not pass an explicit model or reasoning
effort to the SDK. It uses the current Codex config copied into isolated
`CODEX_HOME` directories for each implementation.
