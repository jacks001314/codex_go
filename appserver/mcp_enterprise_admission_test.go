package appserver

// Rust parity: codex-rs/core/src/config/runtime_refresh.rs (#49260). Enterprise
// (EMA) MCP authority is admitted, never re-derived: a running process keeps
// only the trusted profile and registration it was admitted with.

import (
	"strings"
	"testing"
)

const mcpEnterpriseAdmissionTestServers = `[mcp_servers.docs]
command = "mcp-docs"

[mcp_servers.enterprise]
url = "https://resource.example/mcp"
auth = "ema_auth"
oauth_resource = "https://resource.example/mcp"

[mcp_servers.enterprise.oauth]
client_id = "mcp-client"
`

const mcpEnterpriseAdmissionTestProfile = `[mcp_enterprise_managed_auth.idp]
issuer = "https://idp.example"
client_id = "idp-client"
`

func reloadMCPConfigForTest(t *testing.T, router *RuntimeRouter, id int64) {
	t.Helper()
	if reload := router.Handle(requestWithParams(t, IntID(id), MethodConfigMCPServerReload, map[string]any{})); reload.Error != nil {
		t.Fatalf("reload = %+v", reload.Error)
	}
}

// TestRuntimeRouterRetiresEnterpriseMCPWithoutAdmittedProfileLikeRust covers
// the Rust #49260 admission gate: Config::to_mcp_config_with_loaded_plugins
// enables EMA only when `use_xaa` is on and a trusted profile is declared, so an
// enterprise server that survives a config without either must not stay enabled.
func TestRuntimeRouterRetiresEnterpriseMCPWithoutAdmittedProfileLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		prefix string
	}{
		{
			name: "profile_absent",
			prefix: `[features]
use_xaa = true

`,
		},
		{
			name: "use_xaa_disabled",
			prefix: `[features]
use_xaa = false

` + mcpEnterpriseAdmissionTestProfile,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			writeMCPConfigRefreshTestConfig(t, home, testCase.prefix+mcpEnterpriseAdmissionTestServers)
			router := newMCPConfigRefreshTestRouter(home)
			t.Cleanup(func() { _ = router.Close() })

			reloadMCPConfigForTest(t, router, 1)
			names := threadlessMCPServerNames(t, router, 2)
			if names["enterprise"] {
				t.Fatalf("enterprise MCP server was enabled without an admitted profile: %v", names)
			}
			if !names["docs"] {
				t.Fatalf("ordinary MCP server was dropped: %v", names)
			}
		})
	}
}

// TestRuntimeRouterRetiresEnterpriseMCPWhenRegistrationChangesLikeRust covers
// the Rust #49260 `enterprise_retired` predicate: a refresh that replaces the
// trusted profile or the admitted registration disables enterprise MCP instead
// of granting the running process new enterprise authority.
func TestRuntimeRouterRetiresEnterpriseMCPWhenRegistrationChangesLikeRust(t *testing.T) {
	admitted := `[features]
use_xaa = true

` + mcpEnterpriseAdmissionTestProfile + "\n" + mcpEnterpriseAdmissionTestServers

	t.Run("unchanged_registration_stays_admitted", func(t *testing.T) {
		home := t.TempDir()
		writeMCPConfigRefreshTestConfig(t, home, admitted)
		router := newMCPConfigRefreshTestRouter(home)
		t.Cleanup(func() { _ = router.Close() })
		reloadMCPConfigForTest(t, router, 1)
		if names := threadlessMCPServerNames(t, router, 2); !names["enterprise"] {
			t.Fatalf("enterprise MCP server was not admitted: %v", names)
		}
		reloadMCPConfigForTest(t, router, 3)
		if names := threadlessMCPServerNames(t, router, 4); !names["enterprise"] {
			t.Fatalf("an unchanged registration was retired: %v", names)
		}
	})

	t.Run("changed_registration_is_retired", func(t *testing.T) {
		home := t.TempDir()
		writeMCPConfigRefreshTestConfig(t, home, admitted)
		router := newMCPConfigRefreshTestRouter(home)
		t.Cleanup(func() { _ = router.Close() })
		reloadMCPConfigForTest(t, router, 1)
		if names := threadlessMCPServerNames(t, router, 2); !names["enterprise"] {
			t.Fatalf("enterprise MCP server was not admitted: %v", names)
		}
		changed := strings.Replace(admitted, "https://resource.example/mcp", "https://other.example/mcp", 1)
		if changed == admitted {
			t.Fatal("test fixture did not change the server URL")
		}
		writeMCPConfigRefreshTestConfig(t, home, changed)
		reloadMCPConfigForTest(t, router, 3)
		names := threadlessMCPServerNames(t, router, 4)
		if names["enterprise"] {
			t.Fatalf("a changed enterprise registration was adopted instead of retired: %v", names)
		}
		if !names["docs"] {
			t.Fatalf("ordinary MCP server was dropped: %v", names)
		}
	})

	t.Run("changed_profile_secret_is_retired", func(t *testing.T) {
		confidential := `[features]
use_xaa = true

[mcp_enterprise_managed_auth.idp]
issuer = "https://idp.example"
client_id = "idp-client"
client_secret = "secret-one"

` + mcpEnterpriseAdmissionTestServers
		home := t.TempDir()
		writeMCPConfigRefreshTestConfig(t, home, confidential)
		router := newMCPConfigRefreshTestRouter(home)
		t.Cleanup(func() { _ = router.Close() })
		reloadMCPConfigForTest(t, router, 1)
		if names := threadlessMCPServerNames(t, router, 2); !names["enterprise"] {
			t.Fatalf("enterprise MCP server was not admitted: %v", names)
		}
		changed := strings.Replace(confidential, `client_secret = "secret-one"`, `client_secret = "secret-two"`, 1)
		if changed == confidential {
			t.Fatal("test fixture did not change the IdP secret")
		}
		writeMCPConfigRefreshTestConfig(t, home, changed)
		reloadMCPConfigForTest(t, router, 3)
		if names := threadlessMCPServerNames(t, router, 4); names["enterprise"] {
			t.Fatalf("a replaced IdP secret was adopted instead of retired: %v", names)
		}
	})

	t.Run("changed_oauth_auth_method_is_retired", func(t *testing.T) {
		confidential := `[features]
use_xaa = true

` + mcpEnterpriseAdmissionTestProfile + `
[mcp_servers.enterprise]
url = "https://resource.example/mcp"
auth = "ema_auth"
oauth_resource = "https://resource.example/mcp"

[mcp_servers.enterprise.oauth]
client_id = "mcp-client"
client_secret = "resource-secret"
token_endpoint_auth_method = "client_secret_basic"
authorization_server_issuer = "https://as.example"
`
		home := t.TempDir()
		writeMCPConfigRefreshTestConfig(t, home, confidential)
		router := newMCPConfigRefreshTestRouter(home)
		t.Cleanup(func() { _ = router.Close() })
		reloadMCPConfigForTest(t, router, 1)
		if names := threadlessMCPServerNames(t, router, 2); !names["enterprise"] {
			t.Fatalf("enterprise MCP server was not admitted: %v", names)
		}
		changed := strings.Replace(confidential, `token_endpoint_auth_method = "client_secret_basic"`, `token_endpoint_auth_method = "client_secret_post"`, 1)
		if changed == confidential {
			t.Fatal("test fixture did not change the OAuth authentication method")
		}
		writeMCPConfigRefreshTestConfig(t, home, changed)
		reloadMCPConfigForTest(t, router, 3)
		if names := threadlessMCPServerNames(t, router, 4); names["enterprise"] {
			t.Fatalf("a changed OAuth authentication method was adopted instead of retired: %v", names)
		}
	})

	t.Run("changed_profile_is_retired", func(t *testing.T) {
		home := t.TempDir()
		writeMCPConfigRefreshTestConfig(t, home, admitted)
		router := newMCPConfigRefreshTestRouter(home)
		t.Cleanup(func() { _ = router.Close() })
		reloadMCPConfigForTest(t, router, 1)
		if names := threadlessMCPServerNames(t, router, 2); !names["enterprise"] {
			t.Fatalf("enterprise MCP server was not admitted: %v", names)
		}
		changed := strings.Replace(admitted, `client_id = "idp-client"`, `client_id = "idp-client-rotated"`, 1)
		if changed == admitted {
			t.Fatal("test fixture did not change the trusted profile")
		}
		writeMCPConfigRefreshTestConfig(t, home, changed)
		reloadMCPConfigForTest(t, router, 3)
		if names := threadlessMCPServerNames(t, router, 4); names["enterprise"] {
			t.Fatalf("a changed enterprise profile was adopted instead of retired: %v", names)
		}
	})
}
