package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/sandbox"
	codextui "codex_go/tui"
)

// trustDriverServer answers config/read with the supplied effective config and
// layers, and records every method the trust driver sent (Rust
// read_remote_project_trust + config/batchWrite).
type trustDriverServer struct {
	mu      sync.Mutex
	methods []string
}

func (s *trustDriverServer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

func (s *trustDriverServer) sent(method string) bool {
	for _, recorded := range s.recorded() {
		if recorded == method {
			return true
		}
	}
	return false
}

func newTrustDriverClient(t *testing.T, values map[string]any, layers []config.Layer) (*remoteAppServerTUIClient, *trustDriverServer) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	server := &trustDriverServer{}
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			server.mu.Lock()
			server.methods = append(server.methods, request.Method)
			server.mu.Unlock()
			result := map[string]any{"config": values, "origins": map[string]any{}}
			if layers != nil {
				result["layers"] = layers
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()
	client := &remoteAppServerTUIClient{
		state:     codextui.NewState(nil),
		transport: &remoteJSONLineTransport{conn: clientConn, reader: bufio.NewReader(clientConn)},
	}
	return client, server
}

// TestProjectlessFolderTrustEligibleLikeRust covers the guard set of
// codex-rs/tui/src/config_update.rs (#49160): only a local, marker-less folder
// without a saved decision and without project layers may skip folder trust.
func TestProjectlessFolderTrustEligibleLikeRust(t *testing.T) {
	projectless := t.TempDir()
	if !ProjectlessFolderTrustEligible(ProjectlessFolderTrustInputs{CWD: projectless, Local: true}) {
		t.Fatal("a marker-less local folder should skip folder trust")
	}
	if ProjectlessFolderTrustEligible(ProjectlessFolderTrustInputs{CWD: projectless, Local: false}) {
		t.Fatal("a remote connection must not skip folder trust")
	}
	if ProjectlessFolderTrustEligible(ProjectlessFolderTrustInputs{CWD: projectless, Local: true, ProjectLayers: 1}) {
		t.Fatal("a project config layer must not skip folder trust")
	}
	if ProjectlessFolderTrustEligible(ProjectlessFolderTrustInputs{CWD: projectless, Local: true, SavedTrustDecision: true}) {
		t.Fatal("a saved trust decision must not skip folder trust")
	}

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if ProjectlessFolderTrustEligible(ProjectlessFolderTrustInputs{CWD: repo, Local: true}) {
		t.Fatal("a git checkout must not skip folder trust")
	}
}

// TestProjectlessImplicitDefaultsLikeRust covers the extra guards of
// codex-rs/tui/src/projectless.rs (#49160) and the exact defaults it applies:
// workspace-write with granular approval where only request_permissions and
// mcp_elicitations stay enabled.
func TestProjectlessImplicitDefaultsLikeRust(t *testing.T) {
	cwd := t.TempDir()
	defaults, ok := ProjectlessImplicitDefaults(ProjectlessFolderTrustInputs{CWD: cwd, Local: true})
	if !ok {
		t.Fatal("an unconfigured local projectless folder should take the implicit defaults")
	}
	if defaults.SandboxMode != sandbox.SandboxWorkspaceWrite ||
		defaults.PermissionProfileID != sandbox.BuiltInPermissionProfileWorkspace ||
		defaults.ApprovalPolicy != sandbox.ApprovalGranular {
		t.Fatalf("defaults = %#v", defaults)
	}
	if defaults.Granular.SandboxApproval || defaults.Granular.Rules || defaults.Granular.SkillApproval ||
		!defaults.Granular.RequestPermissions || !defaults.Granular.MCPElicitations {
		t.Fatalf("granular defaults = %#v", defaults.Granular)
	}

	cases := map[string]ProjectlessFolderTrustInputs{
		"configured sandbox mode": {CWD: cwd, Local: true, Effective: map[string]any{"sandbox_mode": "read-only"}},
		"configured permissions":  {CWD: cwd, Local: true, Effective: map[string]any{"default_permissions": "read-only"}},
		"launch override":         {CWD: cwd, Local: true, ExplicitOverrides: true},
		"managed default perms": {CWD: cwd, Local: true, Requirements: &config.ConfigRequirements{
			DefaultPermissions: stringPtr("read-only"),
		}},
		"disallowed workspace profile": {CWD: cwd, Local: true, Requirements: &config.ConfigRequirements{
			AllowedPermissionProfiles: map[string]bool{":read-only": true},
		}},
		"disallowed sandbox mode": {CWD: cwd, Local: true, Requirements: &config.ConfigRequirements{
			AllowedSandboxModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly},
		}},
	}
	for name, inputs := range cases {
		if _, ok := ProjectlessImplicitDefaults(inputs); ok {
			t.Fatalf("%s: implicit projectless defaults must stay disabled", name)
		}
	}

	// A nil effective value is absence, matching Rust's Option::is_none.
	if _, ok := ProjectlessImplicitDefaults(ProjectlessFolderTrustInputs{
		CWD: cwd, Local: true,
		Effective: map[string]any{"sandbox_mode": nil, "projects": map[string]any{}},
	}); !ok {
		t.Fatal("null entries must count as unset")
	}
	// An allow-list that admits the workspace profile keeps working.
	if _, ok := ProjectlessImplicitDefaults(ProjectlessFolderTrustInputs{
		CWD: cwd, Local: true,
		Requirements: &config.ConfigRequirements{AllowedPermissionProfiles: map[string]bool{":workspace": true}},
	}); !ok {
		t.Fatal("an allow-list admitting :workspace must keep the implicit defaults")
	}
}

// TestEnsureRemoteProjectTrustSkipsPromptForProjectlessFolderLikeRust is the
// live-path regression for #49160's trust skip: the production trust driver
// returns trusted without prompting and without persisting a decision.
func TestEnsureRemoteProjectTrustSkipsPromptForProjectlessFolderLikeRust(t *testing.T) {
	cwd := t.TempDir()
	client, server := newTrustDriverClient(t, map[string]any{"projects": nil}, nil)
	prompted := false
	status, err := ensureRemoteProjectTrust(context.Background(), client, remoteTrustRequest{
		CWD:         cwd,
		Interactive: true,
		Local:       true,
		Confirm: func(string, string) (bool, error) {
			prompted = true
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("ensureRemoteProjectTrust() error = %v", err)
	}
	if status != TrustStatusTrusted {
		t.Fatalf("status = %q, want trusted", status)
	}
	if prompted {
		t.Fatal("the folder-trust prompt must be skipped for a projectless folder")
	}
	if server.sent(string("config/batchWrite")) {
		t.Fatalf("no trust decision may be persisted, requests = %v", server.recorded())
	}
}

// TestEnsureRemoteProjectTrustPromptsOutsideProjectlessFoldersLikeRust keeps the
// existing behavior for remote connections and for folders that are a project.
func TestEnsureRemoteProjectTrustPromptsOutsideProjectlessFoldersLikeRust(t *testing.T) {
	cwd := t.TempDir()
	// A remote connection never takes the local projectless shortcut.
	client, server := newTrustDriverClient(t, map[string]any{"projects": nil}, nil)
	prompted := false
	status, err := ensureRemoteProjectTrust(context.Background(), client, remoteTrustRequest{
		CWD:         cwd,
		Interactive: true,
		Local:       false,
		Confirm: func(string, string) (bool, error) {
			prompted = true
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("ensureRemoteProjectTrust() error = %v", err)
	}
	if !prompted || status != TrustStatusDeclined {
		t.Fatalf("remote prompt = %v/%q, want prompt + declined", prompted, status)
	}
	_ = server

	// A project config layer keeps the prompt.
	layer := config.Layer{Name: config.LayerSource{Type: config.LayerSourceProject, DotCodexFolder: filepath.Join(cwd, ".gcode")}}
	client, _ = newTrustDriverClient(t, map[string]any{"projects": nil}, []config.Layer{layer})
	prompted = false
	status, err = ensureRemoteProjectTrust(context.Background(), client, remoteTrustRequest{
		CWD:         cwd,
		Interactive: true,
		Local:       true,
		Confirm: func(string, string) (bool, error) {
			prompted = true
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("ensureRemoteProjectTrust() error = %v", err)
	}
	if !prompted || status != TrustStatusDeclined {
		t.Fatalf("project-layer prompt = %v/%q, want prompt + declined", prompted, status)
	}
}

// TestInteractiveLocalThreadStartParamsUseProjectlessDefaultsLikeRust covers
// the "apply the implicit defaults" half of #49160 on the live local
// thread-start path (app/interactive.go builds these params and sends
// thread/start).
func TestInteractiveLocalThreadStartParamsUseProjectlessDefaultsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	state := codextui.NewState(&codextui.Options{CWD: t.TempDir(), Model: "gpt-5.6-sol", Provider: "openai"})

	params := interactiveLocalThreadStartParams(&cli.RootOptions{}, state)
	if params.Sandbox != string(sandbox.SandboxWorkspaceWrite) {
		t.Fatalf("sandbox = %#v, want workspace-write", params.Sandbox)
	}
	granular, ok := params.ApprovalPolicy.(map[string]any)
	if !ok {
		t.Fatalf("approval policy = %#v, want a granular object", params.ApprovalPolicy)
	}
	nested, ok := granular["granular"].(map[string]any)
	if !ok {
		t.Fatalf("approval policy = %#v, want a granular table", params.ApprovalPolicy)
	}
	if nested["sandbox_approval"] != false || nested["rules"] != false || nested["skill_approval"] != false ||
		nested["request_permissions"] != true || nested["mcp_elicitations"] != true {
		t.Fatalf("granular table = %#v", nested)
	}

	// A configured selection keeps the configured permissions.
	configured := codextui.NewState(&codextui.Options{CWD: t.TempDir(), Model: "gpt-5.6-sol", Sandbox: "read-only"})
	if got := interactiveLocalThreadStartParams(&cli.RootOptions{}, configured); got.Sandbox != "read-only" {
		t.Fatalf("configured sandbox = %#v, want read-only", got.Sandbox)
	}
	// A folder with a project marker is not projectless.
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	projectState := codextui.NewState(&codextui.Options{CWD: repo, Model: "gpt-5.6-sol"})
	if got := interactiveLocalThreadStartParams(&cli.RootOptions{}, projectState); strings.TrimSpace(got.Sandbox.(string)) != "" {
		t.Fatalf("project sandbox = %#v, want empty", got.Sandbox)
	}
}
