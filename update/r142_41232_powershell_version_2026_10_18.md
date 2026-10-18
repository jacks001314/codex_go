# Rust #41232: PowerShell version in environment context

Upstream commit: `dc031d4bc70965247478724f84e2ba341c2813f1`

## Result

Implemented with stricter probe parity.

- The under-development `powershell_shell_version` feature gates the probe.
- The version is cached per executable path, bounded by a two-second context,
  runs without a console window, and renders as
  `<shell_version>major.minor</shell_version>`.
- Go emits the complete environment context each turn, so a version that
  becomes unavailable is absent from the next context instead of leaving a
  stale value; no separate removal marker is needed.
- The probe now applies only to a single local environment. A remote executor's
  PowerShell path is never executed on the app-server host.
- Parsing now rejects output over 64 bytes and non-numeric or out-of-range
  major/minor components, matching Rust's `u16` parsing.

## Verification

```text
go test ./appserver -run 'PowerShellShellVersionGate|ParsePowerShellVersion|RendersPowerShellVersion' -count=1
```

Passed on 2026-10-18.
