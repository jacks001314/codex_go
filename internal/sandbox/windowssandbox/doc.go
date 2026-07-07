// Package windowssandbox mirrors codex-rs/windows-sandbox-rs.
//
// The package is intentionally shaped like the Rust crate so each Windows
// sandbox subsystem can be ported in place without losing the source mapping.
// Until the native Windows backend is implemented, exported entry points return
// explicit unsupported errors rather than silently running without isolation.
package windowssandbox
