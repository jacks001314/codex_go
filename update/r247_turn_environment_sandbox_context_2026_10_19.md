# Turn-environment sandbox contexts

Rust upstream: `8444cf63b50a8a88521e0d2970d49f659b48eac7` -
`Derive sandbox contexts from turn environments (#40551)`.

## Alignment

Go derives executor filesystem sandbox contexts from each selected turn
environment rather than from a single runtime-wide sandbox state:

- each context carries that environment's permission profile and cwd;
- workspace roots, Windows sandbox level, private-desktop and legacy-Landlock
  settings are applied per environment;
- remote temporary directories remain attached to the remote context;
- local and remote contexts remain distinct;
- a selected executor skill root whose environment has no matching sandbox
  context fails closed.

## Verification

```text
go test ./appserver -run 'ExecutorSkillSandbox|SandboxContext|EnvironmentConfig' -count=1
go test ./tool -run 'FileSystemSandboxContext|SandboxContext' -count=1
go test ./parity -count=1
```
