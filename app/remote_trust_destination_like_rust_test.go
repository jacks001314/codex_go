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

	"codex_go/config"
	codextui "codex_go/tui"
)

// trustRoutingServer answers config/read per requested cwd so the #44746 tests
// can model a selected folder and a resumed task's saved folder separately.
type trustRoutingServer struct {
	mu       sync.Mutex
	requests []trustRoutingRequest
	byCWD    map[string]map[string]any
	def      map[string]any
	layers   []config.Layer
}

type trustRoutingRequest struct {
	Method string
	CWD    string
}

func (s *trustRoutingServer) recorded() []trustRoutingRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]trustRoutingRequest(nil), s.requests...)
}

func (s *trustRoutingServer) methods() []string {
	var out []string
	for _, request := range s.recorded() {
		out = append(out, request.Method)
	}
	return out
}

func (s *trustRoutingServer) sent(method string) bool {
	for _, recorded := range s.methods() {
		if recorded == method {
			return true
		}
	}
	return false
}

func newTrustRoutingClient(t *testing.T, server *trustRoutingServer) *remoteAppServerTUIClient {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage         `json:"id"`
				Method string                  `json:"method"`
				Params config.ConfigReadParams `json:"params"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			cwd := ""
			if request.Params.CWD != nil {
				cwd = strings.TrimSpace(*request.Params.CWD)
			}
			server.mu.Lock()
			server.requests = append(server.requests, trustRoutingRequest{Method: request.Method, CWD: cwd})
			values := server.def
			if routed, ok := server.byCWD[config.CanonicalProjectTrustPath(cwd)]; ok {
				values = routed
			}
			layers := server.layers
			server.mu.Unlock()
			result := map[string]any{"config": values, "origins": map[string]any{}}
			if layers != nil {
				result["layers"] = layers
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()
	return &remoteAppServerTUIClient{
		state:     codextui.NewState(nil),
		transport: &remoteJSONLineTransport{conn: clientConn, reader: bufio.NewReader(clientConn)},
	}
}

func newTrustRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	return repo
}

// TestReadRemoteProjectTrustSendsDestinationCWDLikeRust pins the config/read
// request shape Rust uses when resolving folder trust: include layers for the
// destination folder.
func TestReadRemoteProjectTrustSendsDestinationCWDLikeRust(t *testing.T) {
	repo := newTrustRepo(t)
	server := &trustRoutingServer{def: map[string]any{"projects": map[string]any{}}}
	client := newTrustRoutingClient(t, server)
	if _, err := readRemoteProjectTrust(context.Background(), client, repo, remoteTrustHostLocal); err != nil {
		t.Fatalf("readRemoteProjectTrust() error = %v", err)
	}
	requests := server.recorded()
	if len(requests) != 1 || requests[0].Method != "config/read" {
		t.Fatalf("requests = %#v, want one config/read", requests)
	}
	if requests[0].CWD != repo {
		t.Fatalf("config/read cwd = %q, want %q", requests[0].CWD, repo)
	}
}

// TestReadRemoteProjectTrustUsesGitRootLikeRust covers Rust #44746's local
// resolution: a cwd without its own entry takes the repository root's decision
// and reports it as the trust target.
func TestReadRemoteProjectTrustUsesGitRootLikeRust(t *testing.T) {
	repo := newTrustRepo(t)
	cwd := filepath.Join(repo, "src")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	values := map[string]any{"projects": map[string]any{repo: map[string]any{"trust_level": "untrusted"}}}
	server := &trustRoutingServer{def: values}
	client := newTrustRoutingClient(t, server)
	read, err := readRemoteProjectTrust(context.Background(), client, cwd, remoteTrustHostLocal)
	if err != nil {
		t.Fatalf("readRemoteProjectTrust() error = %v", err)
	}
	if read.Status != TrustStatusUntrusted || read.TrustLevel != "untrusted" {
		t.Fatalf("read = %#v, want restricted repository decision", read)
	}
	if filepath.Clean(read.TrustTarget) != filepath.Clean(repo) {
		t.Fatalf("trust target = %q, want repository root %q", read.TrustTarget, repo)
	}
}

// TestReadRemoteProjectTrustPrefersCWDSavedDecisionLikeRust covers the
// cwd-before-repository-root precedence: a cwd entry wins even when the
// repository root disagrees.
func TestReadRemoteProjectTrustPrefersCWDSavedDecisionLikeRust(t *testing.T) {
	repo := newTrustRepo(t)
	cwd := filepath.Join(repo, "src")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	values := map[string]any{"projects": map[string]any{
		cwd:  map[string]any{"trust_level": "trusted"},
		repo: map[string]any{"trust_level": "untrusted"},
	}}
	client := newTrustRoutingClient(t, &trustRoutingServer{def: values})
	read, err := readRemoteProjectTrust(context.Background(), client, cwd, remoteTrustHostLocal)
	if err != nil {
		t.Fatalf("readRemoteProjectTrust() error = %v", err)
	}
	if read.Status != TrustStatusTrusted {
		t.Fatalf("status = %q, want trusted cwd decision to win", read.Status)
	}
	if filepath.Clean(read.TrustTarget) != filepath.Clean(cwd) {
		t.Fatalf("trust target = %q, want cwd %q", read.TrustTarget, cwd)
	}
}

// TestReadRemoteProjectTrustHonorsCustomMarkersLikeRust covers a repository
// that carries only a configured marker: the marker root supplies the saved
// decision even though no Git checkout exists.
func TestReadRemoteProjectTrustHonorsCustomMarkersLikeRust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(root, ".company-root"), 0o755); err != nil {
		t.Fatalf("mkdir marker: %v", err)
	}
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	values := map[string]any{
		"project_root_markers": []any{".company-root"},
		"projects":             map[string]any{root: map[string]any{"trust_level": "untrusted"}},
	}
	client := newTrustRoutingClient(t, &trustRoutingServer{def: values})
	read, err := readRemoteProjectTrust(context.Background(), client, cwd, remoteTrustHostLocal)
	if err != nil {
		t.Fatalf("readRemoteProjectTrust() error = %v", err)
	}
	if read.Status != TrustStatusUntrusted {
		t.Fatalf("status = %q, want untrusted marker-root decision", read.Status)
	}
	if filepath.Clean(read.TrustTarget) != filepath.Clean(root) {
		t.Fatalf("trust target = %q, want marker root %q", read.TrustTarget, root)
	}
}

// TestReadRemoteProjectTrustRemoteNestedUntrustedProjectLikeRust covers the
// remote-workspace guard: an unlisted cwd inside an explicitly untrusted
// project is refused with Rust's message.
func TestReadRemoteProjectTrustRemoteNestedUntrustedProjectLikeRust(t *testing.T) {
	repo := newTrustRepo(t)
	cwd := filepath.Join(repo, "src")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	values := map[string]any{"projects": map[string]any{repo: map[string]any{"trust_level": "untrusted"}}}
	client := newTrustRoutingClient(t, &trustRoutingServer{def: values})
	_, err := readRemoteProjectTrust(context.Background(), client, cwd, remoteTrustHostRemote)
	if err == nil || err.Error() != errRemoteProjectInsideUntrusted.Error() {
		t.Fatalf("error = %v, want %q", err, errRemoteProjectInsideUntrusted)
	}

	// Passing the repository root explicitly resolves the saved decision.
	read, err := readRemoteProjectTrust(context.Background(), client, repo, remoteTrustHostRemote)
	if err != nil {
		t.Fatalf("readRemoteProjectTrust(repo) error = %v", err)
	}
	if read.Status != TrustStatusUntrusted {
		t.Fatalf("repository root status = %q, want untrusted", read.Status)
	}
}

// TestEnsureRemoteProjectTrustRestrictedKeepsSavedDecisionLikeRust covers the
// restricted-folder path: the prompt is skipped and no decision is persisted.
func TestEnsureRemoteProjectTrustRestrictedKeepsSavedDecisionLikeRust(t *testing.T) {
	repo := newTrustRepo(t)
	values := map[string]any{"projects": map[string]any{repo: map[string]any{"trust_level": "untrusted"}}}
	server := &trustRoutingServer{def: values}
	client := newTrustRoutingClient(t, server)
	prompted := false
	status, err := ensureRemoteProjectTrust(context.Background(), client, remoteTrustRequest{
		CWD:         repo,
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
	if status != TrustStatusUntrusted || prompted {
		t.Fatalf("status/prompted = %q/%v, want untrusted without a prompt", status, prompted)
	}
	if server.sent("config/batchWrite") {
		t.Fatalf("restricted folders must not persist trust, requests = %v", server.methods())
	}
}

// TestCheckDirectoryTrustOpensRestrictedTaskWithoutPersistingLikeRust covers the
// local-daemon resume pass: both the selected and saved folders are checked, an
// untrusted folder opens restricted, and the existing-task disclosure applies.
func TestCheckDirectoryTrustOpensRestrictedTaskWithoutPersistingLikeRust(t *testing.T) {
	selected := newTrustRepo(t)
	saved := newTrustRepo(t)
	server := &trustRoutingServer{def: map[string]any{"projects": map[string]any{
		selected: map[string]any{"trust_level": "untrusted"},
		saved:    map[string]any{"trust_level": "untrusted"},
	}}}
	client := newTrustRoutingClient(t, server)
	prompted := false
	result, err := checkDirectoryTrust(context.Background(), directoryTrustRequest{
		Client:      client,
		Host:        remoteTrustHostLocal,
		CWD:         selected,
		SavedCWD:    saved,
		ResumedTask: true,
		Interactive: true,
		Confirm: func(string, string) (bool, error) {
			prompted = true
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("checkDirectoryTrust() error = %v", err)
	}
	if prompted {
		t.Fatal("restricted folders must not prompt for a trust decision")
	}
	if !result.Restricted || !result.ExistingTask || result.Persisted {
		t.Fatalf("result = %#v, want restricted existing task without persistence", result)
	}
	cwds := map[string]bool{}
	for _, request := range server.recorded() {
		cwds[config.CanonicalProjectTrustPath(request.CWD)] = true
	}
	if !cwds[config.CanonicalProjectTrustPath(selected)] || !cwds[config.CanonicalProjectTrustPath(saved)] {
		t.Fatalf("checked folders = %v, want selected and saved", cwds)
	}
	if server.sent("config/batchWrite") {
		t.Fatalf("restricted folders must not persist trust, requests = %v", server.methods())
	}
}

// TestCheckDirectoryTrustRereadsTaskAfterConsentLikeRust covers the post-consent
// re-read: a folder that changed while the prompt was open is checked, and an
// untrusted destination ends up restricted even though trust was just saved.
func TestCheckDirectoryTrustRereadsTaskAfterConsentLikeRust(t *testing.T) {
	selected := newTrustRepo(t)
	moved := newTrustRepo(t)
	server := &trustRoutingServer{def: map[string]any{"projects": map[string]any{
		moved: map[string]any{"trust_level": "untrusted"},
	}}}
	client := newTrustRoutingClient(t, server)
	confirmCalls := 0
	result, err := checkDirectoryTrust(context.Background(), directoryTrustRequest{
		Client:      client,
		Host:        remoteTrustHostLocal,
		CWD:         selected,
		ResumedTask: true,
		RereadCWD: func(context.Context) (string, error) {
			return moved, nil
		},
		Interactive: true,
		Confirm: func(string, string) (bool, error) {
			confirmCalls++
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("checkDirectoryTrust() error = %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("confirm calls = %d, want one consent prompt", confirmCalls)
	}
	if !result.Persisted {
		t.Fatalf("result = %#v, want the accepted decision persisted", result)
	}
	if !result.Restricted || !result.ExistingTask {
		t.Fatalf("result = %#v, want the re-read folder to be restricted", result)
	}
	if !server.sent("config/batchWrite") {
		t.Fatalf("requests = %v, want a config/batchWrite", server.methods())
	}
}

// TestCheckDirectoryTrustDeclinedConsentExitsLikeRust covers a declined prompt:
// local startup stops instead of starting the session.
func TestCheckDirectoryTrustDeclinedConsentExitsLikeRust(t *testing.T) {
	repo := newTrustRepo(t)
	client := newTrustRoutingClient(t, &trustRoutingServer{def: map[string]any{"projects": map[string]any{}}})
	result, err := checkDirectoryTrust(context.Background(), directoryTrustRequest{
		Client:      client,
		Host:        remoteTrustHostLocal,
		CWD:         repo,
		Interactive: true,
		Confirm: func(string, string) (bool, error) {
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("checkDirectoryTrust() error = %v", err)
	}
	if !result.ShouldExit || result.Status != TrustStatusDeclined {
		t.Fatalf("result = %#v, want declined exit", result)
	}
}
