// Package setupenv selects how Windows sandbox setup resolves helpers and roots.
//
// Rust keeps this selection in `runtime_ownership::SetupRuntime` and captures the
// opt-in in `app_package::registered_core_requested`. Go needs both the parent
// package and its `elevated` subpackage to agree on one process-wide capture, so
// the capture lives in this leaf package instead of being duplicated.
package setupenv

import (
	"os"
	"sync"
)

// RegisteredCoreEnv selects the registered-core runtime for Windows sandbox
// wrappers (Rust `CODEX_WINDOWS_REGISTERED_CORE`).
const RegisteredCoreEnv = "CODEX_WINDOWS_REGISTERED_CORE"

// Runtime selects legacy helper materialization or verified app-contained Core
// for setup (Rust `SetupRuntime`).
type Runtime string

const (
	Legacy     Runtime = "legacy"
	Registered Runtime = "registered"
)

// IsLegacy reports whether legacy helper materialization applies.
func (r Runtime) IsLegacy() bool {
	return r != Registered
}

// RequestedValue reports whether value requests registered-core execution. Only
// the exact value `1` opts in (Rust `requested_value`).
func RequestedValue(value string, present bool) bool {
	return present && value == "1"
}

var (
	requestedOnce  sync.Once
	requestedValue bool
)

// Requested captures the startup request once (Rust
// `registered_core_requested`'s OnceLock). The flag never authorizes a package
// or process by itself.
func Requested() bool {
	requestedOnce.Do(func() {
		raw, present := os.LookupEnv(RegisteredCoreEnv)
		requestedValue = RequestedValue(raw, present)
	})
	return requestedValue
}

// Current returns the runtime selected when this process started.
func Current() Runtime {
	if Requested() {
		return Registered
	}
	return Legacy
}
