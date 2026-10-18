package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
)

func TestColdResumeRestoresThreadOwnedPermissionsLikeRust(t *testing.T) {
	t.Run("unnamed base profile", func(t *testing.T) {
		record := resumePermissionRecord(t, sandbox.FullAccessPermissionProfile())
		router := newResumePermissionTestRouter(t, `default_permissions = ":read-only"`+"\n", "", record)

		response := resumePermissionResponse(t, router, ThreadResumeParams{ThreadID: string(record.ID)})
		if kind := resumeSandboxKind(t, response.Sandbox); kind != sandbox.SandboxDangerFullAccess {
			t.Fatalf("resumed sandbox = %q, want danger-full-access", kind)
		}
		if response.ActivePermissionProfile != nil {
			t.Fatalf("active permission profile = %#v, want unnamed base permissions", response.ActivePermissionProfile)
		}
	})

	t.Run("named profile", func(t *testing.T) {
		record := resumePermissionRecord(t, sandbox.FullAccessPermissionProfile())
		record.Metadata.ActivePermissionProfile = sandbox.BuiltInPermissionProfileReadOnly
		router := newResumePermissionTestRouter(t, `default_permissions = ":danger-full-access"`+"\n", "", record)

		response := resumePermissionResponse(t, router, ThreadResumeParams{ThreadID: string(record.ID)})
		if kind := resumeSandboxKind(t, response.Sandbox); kind != sandbox.SandboxReadOnly {
			t.Fatalf("resumed sandbox = %q, want read-only", kind)
		}
		if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != sandbox.BuiltInPermissionProfileReadOnly {
			t.Fatalf("active permission profile = %#v, want read-only", response.ActivePermissionProfile)
		}
	})

	t.Run("managed requirements narrow saved profile", func(t *testing.T) {
		record := resumePermissionRecord(t, sandbox.FullAccessPermissionProfile())
		requirements := "allowed_sandbox_modes = [\"read-only\"]\n"
		router := newResumePermissionTestRouter(t, `default_permissions = ":danger-full-access"`+"\n", requirements, record)

		response := resumePermissionResponse(t, router, ThreadResumeParams{ThreadID: string(record.ID)})
		if kind := resumeSandboxKind(t, response.Sandbox); kind != sandbox.SandboxReadOnly {
			t.Fatalf("resumed sandbox = %q, want managed read-only", kind)
		}
	})

	t.Run("request config override wins over named profile", func(t *testing.T) {
		record := resumePermissionRecord(t, sandbox.FullAccessPermissionProfile())
		record.Metadata.ActivePermissionProfile = sandbox.BuiltInPermissionProfileDangerFullAccess
		router := newResumePermissionTestRouter(t, `default_permissions = ":danger-full-access"`+"\n", "", record)

		response := resumePermissionResponse(t, router, ThreadResumeParams{
			ThreadID: string(record.ID),
			Config:   map[string]any{"default_permissions": ":" + sandbox.BuiltInPermissionProfileReadOnly},
		})
		if kind := resumeSandboxKind(t, response.Sandbox); kind != sandbox.SandboxReadOnly {
			t.Fatalf("resumed sandbox = %q, want request read-only", kind)
		}
		if response.ActivePermissionProfile == nil || response.ActivePermissionProfile.ID != ":"+sandbox.BuiltInPermissionProfileReadOnly {
			t.Fatalf("active permission profile = %#v, want request read-only", response.ActivePermissionProfile)
		}
	})
}

func TestColdResumePermissionOverridesWinLikeRust(t *testing.T) {
	tests := []struct {
		name                string
		params              ThreadResumeParams
		wantKind            sandbox.SandboxMode
		wantActiveProfileID string
	}{
		{
			name:     "sandbox override",
			params:   ThreadResumeParams{Sandbox: string(sandbox.SandboxReadOnly)},
			wantKind: sandbox.SandboxReadOnly,
		},
		{
			name:                "permission override",
			params:              ThreadResumeParams{Permissions: stringPointerForResumePermissionTest(sandbox.BuiltInPermissionProfileReadOnly)},
			wantKind:            sandbox.SandboxReadOnly,
			wantActiveProfileID: sandbox.BuiltInPermissionProfileReadOnly,
		},
		{
			name: "config default permissions override",
			params: ThreadResumeParams{Config: map[string]any{
				"default_permissions": ":" + sandbox.BuiltInPermissionProfileReadOnly,
			}},
			wantKind:            sandbox.SandboxReadOnly,
			wantActiveProfileID: sandbox.BuiltInPermissionProfileReadOnly,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			record := resumePermissionRecord(t, sandbox.FullAccessPermissionProfile())
			router := newResumePermissionTestRouter(t, `default_permissions = ":danger-full-access"`+"\n", "", record)
			testCase.params.ThreadID = string(record.ID)

			response := resumePermissionResponse(t, router, testCase.params)
			if kind := resumeSandboxKind(t, response.Sandbox); kind != testCase.wantKind {
				t.Fatalf("resumed sandbox = %q, want %q", kind, testCase.wantKind)
			}
			gotProfileID := ""
			if response.ActivePermissionProfile != nil {
				gotProfileID = strings.TrimPrefix(response.ActivePermissionProfile.ID, ":")
			}
			if gotProfileID != testCase.wantActiveProfileID {
				t.Fatalf("active permission profile = %q, want %q", gotProfileID, testCase.wantActiveProfileID)
			}
		})
	}
}

func newResumePermissionTestRouter(t *testing.T, configBody string, requirements string, record *session.Record) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	if requirements != "" {
		if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
			t.Fatalf("write requirements error = %v", err)
		}
	}
	store := session.NewStore(t.TempDir())
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
}

func resumePermissionRecord(t *testing.T, profile sandbox.PermissionProfile) *session.Record {
	t.Helper()
	raw, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("Unmarshal(profile) error = %v", err)
	}
	now := time.Now().UTC()
	return &session.Record{
		ID:        "thread-resume-permissions",
		SessionID: "thread-resume-permissions",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD: t.TempDir(), Model: "gpt-resume-permissions", Source: string(SessionSourceCli),
			HistoryMode: string(ThreadHistoryLegacy),
			Extra:       map[string]any{"permission_profile": decoded},
			RolloutTurns: []session.TurnSnapshot{{
				ID: "turn-resume-permissions", Status: string(TurnStatusCompleted),
			}},
		},
	}
}

func resumePermissionResponse(t *testing.T, router *RuntimeRouter, params ThreadResumeParams) *ThreadResumeResponse {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, params))
	if response.Error != nil {
		t.Fatalf("thread/resume error = %+v", response.Error)
	}
	resumed, ok := response.Result.(*ThreadResumeResponse)
	if !ok || resumed == nil {
		t.Fatalf("thread/resume result = %#v", response.Result)
	}
	return resumed
}

func resumeSandboxKind(t *testing.T, value any) sandbox.SandboxMode {
	t.Helper()
	policy, err := parseTurnSandboxPolicy(value)
	if err != nil || policy == nil {
		t.Fatalf("resumed sandbox = %#v, error = %v", value, err)
	}
	if policy.Kind == "" {
		return sandbox.SandboxWorkspaceWrite
	}
	return policy.Kind
}

func stringPointerForResumePermissionTest(value string) *string {
	return &value
}
