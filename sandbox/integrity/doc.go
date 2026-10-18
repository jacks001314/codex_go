// Package integrity ports upstream's policy-based containment integrity checks
// (Rust codex-rs/sandboxing/src/sandbox_integrity).
//
// Layout mirrors the upstream module (sandbox_integrity/mod.rs:1-7):
//
//	finding.go   <- sandbox_integrity/finding.rs
//	policy.go    <- sandbox_integrity/policy.rs
//	contents.go  <- sandbox_integrity/contents.rs
//
// Upstream re-exports the library surface from codex-sandboxing/src/lib.rs
// (`FileContentsChecker`, `IntegrityFinding`, `IntegrityFindingDetails`) and lets
// the caller own the dependency inventory, reporting, and enforcement. This
// package also provides the observational runner from #51842; production
// callers can invoke RunIntegrityChecks without changing the launch result.
package integrity
