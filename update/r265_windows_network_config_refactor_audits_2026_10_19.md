# Windows/network helper extraction audits

Rust upstream refactors:

- `#45180` Extract shared network configuration and environment policy helpers
- `#45169` Extract Windows sandbox setup and installation storage into the library

## Verdict

Both are not applicable as module-layout refactors. Go already keeps network
configuration resolution in `network/` and environment policy in
`config`/`appserver`, while Windows sandbox setup and installation-record helpers
are already exposed from `sandbox/windowssandbox` and `appserverdaemon` library
packages.
