package appserver

import (
	"testing"

	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
)

// Rust #62846: an explicit thread/guardianReview returns an assessment for a
// recorded MCP call even when the tool would be approved automatically, and the
// review carries the MCP server's owning environment permissions.
func TestThreadGuardianReviewRecordedMCPCarriesOwningEnvironmentPermissionsLikeRust(t *testing.T) {
	profile := &sandbox.PermissionProfile{
		DeniedReadEntries: []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "path", Path: "/env/secret"},
			Access: sandbox.FileSystemAccessRead,
		}},
	}
	cases := []struct {
		name       string
		source     mcp.MCPServerPermissionSource
		wantOrigin bool
	}{
		{
			name: "environment owned",
			source: mcp.MCPServerPermissionSource{
				Profile:       profile,
				CWD:           "/env/docs",
				EnvironmentID: "env-docs",
			},
			wantOrigin: true,
		},
		{
			name: "thread owned",
			source: mcp.MCPServerPermissionSource{
				Profile:       profile,
				CWD:           "/env/docs",
				EnvironmentID: "env-docs",
				Thread:        true,
			},
			wantOrigin: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviewer := &assessmentGuardianReviewer{
				decision:   state.DecisionApproved,
				assessment: &state.Assessment{RiskLevel: state.RiskLow, UserAuthorization: state.AuthorizationHigh, Rationale: "reviewed"},
			}
			payload := mustJSON(t, map[string]any{
				"type":      "function_call",
				"namespace": "mcp__docs",
				"name":      "search",
				"call_id":   "mcp-auto",
				"arguments": `{"query":"release notes"}`,
			})
			router := guardianReviewTestRouter(t, reviewer, string(payload))
			service := mcp.NewMCPService(&mcp.RuntimeConfig{
				ServerPermissionSources: map[string]mcp.MCPServerPermissionSource{"docs": tc.source},
			})
			defer service.Close()
			service.SetServer(mcp.MCPServerStatus{
				Name:  "docs",
				State: mcp.MCPServerReady,
				Tools: []mcp.MCPToolInfo{{
					Name:        "search",
					Title:       "Search docs",
					Description: "Search the documentation.",
					Annotations: map[string]any{"readOnlyHint": true},
				}},
			})
			router.services.MCP = service

			response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
			if response.Error != nil {
				t.Fatalf("thread/guardianReview error = %+v", response.Error)
			}
			called, action, target, ctx := reviewer.snapshot()
			if !called || target != "mcp-auto" {
				t.Fatalf("an automatically approved MCP tool must still be assessed: called=%v target=%q", called, target)
			}
			if action.Type != "mcp_tool_call" || action.Server != "docs" || action.ToolName != "search" {
				t.Fatalf("action = %#v, want the recorded MCP call", action)
			}
			origin := guardianReviewOriginFromContext(ctx)
			if !tc.wantOrigin {
				if origin != nil && origin.MCPOwnerPermissions {
					t.Fatalf("thread-owned server applied owner permissions: %#v", origin)
				}
				return
			}
			if origin == nil || !origin.MCPOwnerPermissions || !origin.PermissionEvidenceOnly {
				t.Fatalf("review origin = %#v, want the server's owning environment permissions", origin)
			}
			if origin.EnvironmentID != "env-docs" || origin.CWD != "/env/docs" || origin.PermissionProfile == nil {
				t.Fatalf("review origin = %#v, want the owning environment id, cwd and profile", origin)
			}
			if len(origin.PermissionProfile.DeniedReadEntries) != 1 || origin.PermissionProfile.DeniedReadEntries[0].Path.Path != "/env/secret" {
				t.Fatalf("review origin profile = %#v, want the denied read entry", origin.PermissionProfile)
			}
		})
	}
}
