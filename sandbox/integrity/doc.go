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
// the caller own the dependency inventory, reporting, enforcement, and the
// sandbox integrity runner that consumes it (#51842). This package keeps the
// same split: it is the library half, with no production caller wired yet.
package integrity
