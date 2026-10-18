# Rust #52193: enforce managed MXC requirements on Windows

Upstream commit: `f90a578f7696ca077455d59bfba60854806d401f`

## Result

Implemented.

- `ConfigRequirements` and requirements-file parsing now carry
  `windows.require_mxc`, including camelCase acceptance and clone/emptiness
  handling.
- `Config.ValidateWindowsMXCRequirement` fails on Windows when the requirement
  is true and neither an explicit `windows.sandbox = "mxc"` nor
  `features.prefer_mxc = true` selects MXC.
- The check runs after config loading and on the app-server thread-start/turn
  effective-config paths, which also cover requirements supplied by the config
  service after the initial file load.
- The requirement is ignored on non-Windows hosts.

## Verification

```text
go test ./config -count=1
go test ./appserver -run 'ThreadStart|WindowsMXC|MXC' -count=1
go vet ./config ./appserver
```

`config` and `go vet` passed. The appserver selection had only the two known
project-trust baseline failures.
