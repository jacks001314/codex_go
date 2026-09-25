package appserver

import (
	"os"
	"testing"

	execserverclient "codex_go/execserver"
	"codex_go/turn"
)

// Rust parity: EnvironmentManager::default_environment: a turn that selects no
// environment uses the provider's default, so an environments.toml `default`
// pointing at a configured executor replaces the implicit local environment.
func TestUnifiedExecEnvironmentsHonorProviderDefaultLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
	if err := manager.ApplyProviderSnapshot(execserverclient.EnvironmentProviderSnapshot{
		IncludeLocal: true,
		Default:      execserverclient.EnvironmentDefault{Kind: execserverclient.EnvironmentDefaultID, ID: "ssh-dev"},
		Environments: []execserverclient.NamedEnvironment{{
			ID: "ssh-dev",
			Transport: execserverclient.EnvironmentTransport{
				Kind: execserverclient.EnvironmentTransportStdio,
				Command: &execserverclient.StdioExecServerCommand{
					Program: executable,
					Args:    []string{"-test.run=^TestAppServerStdioEnvironmentHelperProcess$"},
					Env:     map[string]string{appserverStdioEnvironmentHelperEnv: "1"},
				},
			},
		}},
	}); err != nil {
		t.Fatalf("ApplyProviderSnapshot() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})

	environments := router.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{})
	if len(environments) != 1 {
		t.Fatalf("unified exec environments = %#v, want the provider default", environments)
	}
	if environments[0].ID != "ssh-dev" || environments[0].ExecServerStdioCommand == nil {
		t.Fatalf("environment = %#v, want the stdio default environment", environments[0])
	}

	// An explicit selection still wins over the provider default.
	explicit := router.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "ssh-dev"}},
	})
	if len(explicit) != 1 || explicit[0].ID != "ssh-dev" {
		t.Fatalf("explicit environments = %#v", explicit)
	}
}

// TestUnifiedExecEnvironmentsIgnoreDisabledProviderDefaultLikeRust pins the
// `default = "none"` (or local) case: no environment is injected, so tools keep
// running locally.
func TestUnifiedExecEnvironmentsIgnoreDisabledProviderDefaultLikeRust(t *testing.T) {
	for _, defaultSelection := range []execserverclient.EnvironmentDefault{
		{Kind: execserverclient.EnvironmentDefaultDisabled},
		{Kind: execserverclient.EnvironmentDefaultID, ID: execserverclient.LocalEnvironmentID},
	} {
		manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
		if err := manager.ApplyProviderSnapshot(execserverclient.EnvironmentProviderSnapshot{
			IncludeLocal: true,
			Default:      defaultSelection,
		}); err != nil {
			t.Fatalf("ApplyProviderSnapshot() error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{Environment: manager})
		if environments := router.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{}); len(environments) != 0 {
			t.Fatalf("unified exec environments = %#v, want none for default %#v", environments, defaultSelection)
		}
	}
}
