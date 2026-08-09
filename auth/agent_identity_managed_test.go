package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapManagedAgentIdentityRegistersAndPersistsRecord(t *testing.T) {
	home := t.TempDir()
	snapshot := AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":    fakeJWTForManagedAgentIdentity(map[string]any{"chatgpt_account_id": "account-1", "chatgpt_user_id": "user-1", "email": "user@example.com", "plan_type": "pro"}),
			"refresh_token":   "refresh-token",
			"chatgpt_user_id": "user-1",
		},
	}
	var agentRequest map[string]any
	var taskRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Fatalf("Authorization = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&agentRequest); err != nil {
				t.Fatalf("decode agent request: %v", err)
			}
			writeJSONAuthAgentIdentity(t, w, map[string]string{"agent_runtime_id": "agent-runtime"})
		case "/v1/agent/agent-runtime/task/register":
			if err := json.NewDecoder(r.Body).Decode(&taskRequest); err != nil {
				t.Fatalf("decode task request: %v", err)
			}
			writeJSONAuthAgentIdentity(t, w, map[string]string{"task_id": "task-runtime"})
		default:
			t.Fatalf("unexpected path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	resolved, err := BootstrapManagedAgentIdentity(context.Background(), &AgentIdentityBootstrapOptions{
		CodexHome:               home,
		AuthSnapshot:            &snapshot,
		HTTPClient:              server.Client(),
		AgentIdentityAuthAPIURL: server.URL,
		SessionSource:           "cli",
		AgentVersion:            "1.2.3",
	})
	if err != nil {
		t.Fatalf("BootstrapManagedAgentIdentity() error = %v", err)
	}
	record := AgentIdentityRecordFromAuth(resolved)
	if record == nil || record.AgentRuntimeID != "agent-runtime" || record.TaskID == nil || *record.TaskID != "task-runtime" {
		t.Fatalf("record = %#v", record)
	}
	if got := agentRequest["capabilities"]; got == nil {
		t.Fatalf("agent request missing capabilities: %#v", agentRequest)
	}
	if taskRequest["timestamp"] == "" || taskRequest["signature"] == "" {
		t.Fatalf("task request = %#v", taskRequest)
	}
	loaded, err := NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded == nil || loaded.Mode() != "chatgpt" || AgentIdentityRecordFromAuth(&AuthDotJSON{AuthMode: "agent-identity", AgentIdentity: loaded.AgentIdentity}) == nil {
		t.Fatalf("loaded auth = %#v", loaded)
	}
}

func TestAgentIdentityAuthAPIBaseURLEnvOverride(t *testing.T) {
	t.Setenv(AgentIdentityAuthAPIBaseURLEnv, "https://identity.example.test/api/")
	got, err := agentIdentityAuthAPIBaseURL("https://chatgpt.com/backend-api/", "")
	if err != nil || got != "https://identity.example.test/api" {
		t.Fatalf("agent identity auth API URL = %q, %v", got, err)
	}
	got, err = agentIdentityAuthAPIBaseURL("", "https://explicit.example.test/")
	if err != nil || got != "https://explicit.example.test" {
		t.Fatalf("explicit agent identity auth API URL = %q, %v", got, err)
	}
}

func TestBootstrapManagedAgentIdentitySuppressesRetryDuringCooldown(t *testing.T) {
	clearAgentIdentityBootstrapCooldown()
	defer clearAgentIdentityBootstrapCooldown()
	snapshot := AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token": fakeJWTForManagedAgentIdentity(map[string]any{"chatgpt_account_id": "account-cooldown", "chatgpt_user_id": "user-cooldown"}),
		},
	}
	registerAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/register" {
			t.Fatalf("unexpected path = %q", r.URL.Path)
		}
		registerAttempts++
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()
	opts := &AgentIdentityBootstrapOptions{
		AuthSnapshot:            &snapshot,
		HTTPClient:              server.Client(),
		AgentIdentityAuthAPIURL: server.URL,
	}

	_, err := BootstrapManagedAgentIdentity(context.Background(), opts)
	var unavailable *AgentIdentityBootstrapUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("BootstrapManagedAgentIdentity() error = %v, want AgentIdentityBootstrapUnavailableError", err)
	}
	if registerAttempts != maxAgentIdentityBootstrapAttempts {
		t.Fatalf("registerAttempts = %d, want %d", registerAttempts, maxAgentIdentityBootstrapAttempts)
	}

	_, err = BootstrapManagedAgentIdentity(context.Background(), opts)
	var suppressed *AgentIdentityBootstrapUnavailableError
	if !errors.As(err, &suppressed) {
		t.Fatalf("second BootstrapManagedAgentIdentity() error = %v, want AgentIdentityBootstrapUnavailableError", err)
	}
	if registerAttempts != maxAgentIdentityBootstrapAttempts {
		t.Fatalf("registerAttempts after cooldown = %d, want %d", registerAttempts, maxAgentIdentityBootstrapAttempts)
	}
}

func fakeJWTForManagedAgentIdentity(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func writeJSONAuthAgentIdentity(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
}
