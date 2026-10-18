# Compatible PowerShell for elevated sandbox

Rust upstream: `426fa8cdab4247e5623e9617d531f6917482b947` -
`Use compatible PowerShell for elevated Windows sandbox commands (#41227)`.

## Alignment

Go prepares PowerShell commands for elevated Windows sandbox execution:

- Store/WindowsApps `pwsh.exe` and `powershell.exe` launchers are detected
  case-insensitively;
- local elevated commands substitute the compatible Windows PowerShell path;
- remote-environment commands retain their original executable;
- `-NoProfile` is inserted once when absent, avoiding profiles in the mixed
  sandbox-account/user-profile environment;
- non-elevated sandbox levels and non-PowerShell shells are unchanged.

The no-compatible-shell regression isolates fixed fallback paths so a host that
ships built-in Windows PowerShell does not make the negative case
environment-dependent.

## Verification

```text
go test ./execserver -run 'WindowsPowerShell|PowerShellFallback|SandboxProgram' -count=1
go test ./parity -count=1
```
