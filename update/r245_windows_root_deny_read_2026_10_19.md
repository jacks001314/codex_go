# Windows root reads with deny-read rules

Rust upstream: `a12c4f4c01f0fbbcd7060021fbf77327dc8be7cb` -
`Preserve Windows root reads with deny-read rules (#40441)`.

## Alignment

Go's Windows sandbox path already distinguishes symbolic root read access from
full unrestricted read access:

- a symbolic root read entry keeps ordinary root/public paths readable;
- exact-path and glob deny-read entries still narrow those reads;
- write roots do not automatically erase read roots unless the path is denied;
- bundled plugin/skill cache paths remain readable while protected setup paths
  stay denied;
- full-disk-write profiles with deny-read entries still sandbox read operations;
- missing deny paths and existing canonical targets are both materialized for
  Windows ACL application.

## Verification

```text
go test ./sandbox -run 'Windows|DenyRead|Root' -count=1
go test ./sandbox/windowssandbox -run 'DenyRead|Root|ACL' -count=1
go test ./parity -count=1
```
