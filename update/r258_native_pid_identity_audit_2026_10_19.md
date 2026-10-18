# Native PID process identity audit

Rust upstream: `c0316291ca Use native process identities for PID-managed daemons (#45779)`.

## Verdict

Complete. Go PID records carry a platform-native process identity: Linux boot
id plus start ticks, Windows creation/unique identity, and Darwin boot id from
`kern.bootsessionuuid` plus XNU flavor-17 `proc_uniqidentifierinfo` read via
`SYS_PROC_INFO` without cgo. Darwin start seconds/microseconds from
`kern.proc.pid` remain the fallback comparison. Matching detects PID reuse and
zombie processes, legacy records are promoted by double-reading the identity,
and mismatched identities are never trusted.

## Verification

```text
go test ./appserverdaemon -count=1
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./appserverdaemon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go test -c ./appserverdaemon
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./appserverdaemon
```
