package tool

import (
	"net/http"
	"testing"

	"codex_go/execserver"
)

func providerSnapshot(entries ...execserver.NamedEnvironment) execserver.EnvironmentProviderSnapshot {
	return execserver.EnvironmentProviderSnapshot{Environments: entries}
}

func stdioEnvironment(id string, program string) execserver.NamedEnvironment {
	return execserver.NamedEnvironment{
		ID:        id,
		Transport: execserver.EnvironmentTransport{Kind: execserver.EnvironmentTransportStdio, Command: &execserver.StdioExecServerCommand{Program: program}},
	}
}

func webSocketEnvironment(id string, url string, headers http.Header) execserver.NamedEnvironment {
	return execserver.NamedEnvironment{
		ID:        id,
		Transport: execserver.EnvironmentTransport{Kind: execserver.EnvironmentTransportWebSocket, WebSocketURL: url, HTTPHeaders: headers},
	}
}

// Mirrors Rust EnvironmentManager::default_environment: the provider's selected
// default replaces the implicit local environment, and `default = "none"`, a
// local default or an unconfigured id all keep tools local.
func TestUnifiedExecEnvironmentsForProviderDefaultMatchesRust(t *testing.T) {
	snapshot := providerSnapshot(
		stdioEnvironment("ssh-dev", "codex-exec-server"),
		webSocketEnvironment("remote-dev", "wss://example.test/exec", http.Header{"Authorization": []string{"Bearer token"}}),
	)
	snapshot.Default = execserver.EnvironmentDefault{Kind: execserver.EnvironmentDefaultID, ID: "ssh-dev"}

	environments := UnifiedExecEnvironmentsForProviderDefault(snapshot)
	if len(environments) != 1 {
		t.Fatalf("default environments = %#v", environments)
	}
	if environments[0].ID != "ssh-dev" || environments[0].ExecServerStdioCommand == nil || environments[0].ExecServerStdioCommand.Program != "codex-exec-server" {
		t.Fatalf("stdio default = %#v", environments[0])
	}

	// No default at all (an unconfigured provider or `default = "none"`).
	none := providerSnapshot(stdioEnvironment("ssh-dev", "codex-exec-server"))
	none.Default = execserver.EnvironmentDefault{Kind: execserver.EnvironmentDefaultDisabled}
	if got := UnifiedExecEnvironmentsForProviderDefault(none); len(got) != 0 {
		t.Fatalf("disabled default = %#v", got)
	}

	// A default that names the implicit local environment stays local.
	local := none
	local.IncludeLocal = true
	local.Default = execserver.EnvironmentDefault{Kind: execserver.EnvironmentDefaultID, ID: execserver.LocalEnvironmentID}
	if got := UnifiedExecEnvironmentsForProviderDefault(local); len(got) != 0 {
		t.Fatalf("local default = %#v", got)
	}

	// A default id the provider does not offer resolves to nothing rather than a
	// half-built environment.
	unknown := none
	unknown.Default = execserver.EnvironmentDefault{Kind: execserver.EnvironmentDefaultID, ID: "missing"}
	if got := UnifiedExecEnvironmentsForProviderDefault(unknown); len(got) != 0 {
		t.Fatalf("unknown default = %#v", got)
	}
}

func TestUnifiedExecEnvironmentsForProviderIDsCarriesTransports(t *testing.T) {
	headers := http.Header{"Authorization": []string{"Bearer token"}}
	snapshot := providerSnapshot(
		stdioEnvironment("ssh-dev", "codex-exec-server"),
		webSocketEnvironment("remote-dev", "wss://example.test/exec", headers),
	)

	environments := UnifiedExecEnvironmentsForProviderIDs(snapshot, []string{"remote-dev", "ssh-dev", "local", "", "missing"})
	if len(environments) != 2 {
		t.Fatalf("environments = %#v", environments)
	}
	if environments[0].ID != "remote-dev" || environments[0].ExecServerURL != "wss://example.test/exec" {
		t.Fatalf("websocket environment = %#v", environments[0])
	}
	if got := environments[0].ExecServerHTTPHeaders.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("websocket headers = %#v", environments[0].ExecServerHTTPHeaders)
	}
	if environments[1].ID != "ssh-dev" || environments[1].ExecServerStdioCommand == nil {
		t.Fatalf("stdio environment = %#v", environments[1])
	}

	// The headers are a clone: mutating them must not touch the snapshot.
	environments[0].ExecServerHTTPHeaders.Set("Authorization", "changed")
	if got := snapshot.Environments[1].Transport.HTTPHeaders.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("snapshot headers were mutated: %q", got)
	}

	if got := UnifiedExecEnvironmentsForProviderIDs(snapshot, nil); len(got) != 0 {
		t.Fatalf("empty selection = %#v", got)
	}
}
