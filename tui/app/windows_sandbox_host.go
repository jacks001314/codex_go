package app

import (
	"codex_go/appserver"
	"codex_go/execserver"
)

// Rust parity: codex-rs/tui/src/app/platform_actions.rs WindowsSandboxHost and
// codex-rs/tui/src/windows_sandbox.rs host_from_environments.

// WindowsSandboxHost classifies the executors a thread runs on. Unknown means
// the thread has not reported its environments, so the TUI must not assume the
// sandbox is local.
type WindowsSandboxHost string

const (
	WindowsSandboxHostLocal   WindowsSandboxHost = "local"
	WindowsSandboxHostMixed   WindowsSandboxHost = "mixed"
	WindowsSandboxHostRemote  WindowsSandboxHost = "remote"
	WindowsSandboxHostUnknown WindowsSandboxHost = "unknown"
)

// HostFromEnvironments classifies a thread's reported environments (Rust
// #45813). A server-local connection can still select remote executors, so a
// missing or empty selection stays Unknown.
func HostFromEnvironments(environments []appserver.ThreadEnvironment) WindowsSandboxHost {
	if len(environments) == 0 {
		return WindowsSandboxHostUnknown
	}
	local := false
	remote := false
	for _, environment := range environments {
		if environment.EnvironmentID == execserver.LocalEnvironmentID {
			local = true
		} else {
			remote = true
		}
	}
	switch {
	case local && remote:
		return WindowsSandboxHostMixed
	case local:
		return WindowsSandboxHostLocal
	case remote:
		return WindowsSandboxHostRemote
	default:
		return WindowsSandboxHostUnknown
	}
}
