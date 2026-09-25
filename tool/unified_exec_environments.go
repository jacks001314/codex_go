package tool

import (
	"strings"

	"codex_go/execserver"
)

// Rust parity: the host-side assembly of an EnvironmentManager from CODEX_HOME
// (codex-rs/app-server/src/lib.rs `EnvironmentManager::from_codex_home`, which
// `codex exec`, the TUI and the worktree paths share). Hosts that run turns
// in-process install the configured executors on their tools the same way the
// app-server does, so an `environments.toml` entry selects the same transport
// everywhere.

// UnifiedExecEnvironmentsForProviderDefault maps the provider's default
// environment. Rust's `EnvironmentManager::default_environment` replaces the
// implicit local environment only when the provider selects a configured
// executor: `default = "none"` (a disabled default), a local default or an
// unconfigured id all leave tools local.
func UnifiedExecEnvironmentsForProviderDefault(snapshot execserver.EnvironmentProviderSnapshot) []UnifiedExecEnvironment {
	defaultID, ok := snapshot.EnvironmentID()
	if !ok {
		return nil
	}
	return UnifiedExecEnvironmentsForProviderIDs(snapshot, []string{defaultID})
}

// UnifiedExecEnvironmentsForProviderIDs maps the named configured environments,
// skipping ids the provider does not offer and the implicit local environment.
func UnifiedExecEnvironmentsForProviderIDs(snapshot execserver.EnvironmentProviderSnapshot, ids []string) []UnifiedExecEnvironment {
	if len(ids) == 0 {
		return nil
	}
	out := make([]UnifiedExecEnvironment, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || id == execserver.LocalEnvironmentID {
			continue
		}
		transport, ok := providerEnvironmentTransport(snapshot, id)
		if !ok {
			continue
		}
		environment := UnifiedExecEnvironment{ID: id}
		switch transport.Kind {
		case execserver.EnvironmentTransportStdio:
			if transport.Command == nil {
				continue
			}
			environment.ExecServerStdioCommand = transport.Command
		default:
			environment.ExecServerURL = strings.TrimSpace(transport.WebSocketURL)
			if len(transport.HTTPHeaders) > 0 {
				environment.ExecServerHTTPHeaders = transport.HTTPHeaders.Clone()
			}
		}
		out = append(out, environment)
	}
	return out
}

func providerEnvironmentTransport(snapshot execserver.EnvironmentProviderSnapshot, id string) (execserver.EnvironmentTransport, bool) {
	for _, environment := range snapshot.Environments {
		if strings.TrimSpace(environment.ID) == id {
			return environment.Transport, true
		}
	}
	return execserver.EnvironmentTransport{}, false
}
