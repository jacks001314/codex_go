package appserver

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// requiredSkillsWiringAgent records the model requests a wired turn issues, so a
// test can prove the turn failed before inference.
type requiredSkillsWiringAgent struct {
	requests []model.AgentRequest
}

func (a *requiredSkillsWiringAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{
		ResponseID: "resp-1",
		Message:    "done",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

// requiredSkillsTestSkill writes a `review` skill under a fresh capability root.
func requiredSkillsTestSkill(t *testing.T) string {
	t.Helper()
	capabilityRoot := t.TempDir()
	skillDir := filepath.Join(capabilityRoot, "plugin", "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: review\ndescription: Review through the executor.\n---\n\nREVIEW_MAIN_MARKER\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	return capabilityRoot
}

// newRequiredSkillsRouter builds the runtime host the wiring tests run against:
// a config that keeps the plugins feature available plus an environment manager.
func newRequiredSkillsRouter(t *testing.T, home string) *RuntimeRouter {
	t.Helper()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[skills]\ninclude_instructions = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	configService := config.NewConfigService(home)
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	return NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       configService,
		Skills:       NewSkillsServiceWithOptions(&SkillsServiceOptions{Config: configService}),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Environment:  manager,
	})
}

// startRequiredSkillsThread starts a thread whose selected capability root lives
// in environmentID. rootPath must be spelled the way that environment's
// filesystem reads it: a plain path for the local environment, a file:// URI for
// a registered remote environment (`fs/walk` requires an absolute file URI).
func startRequiredSkillsThread(t *testing.T, router *RuntimeRouter, environmentID string, rootPath string) string {
	t.Helper()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(),
		SelectedCapabilityRoots: []SelectedCapabilityRoot{{
			ID:       "demo-plugin@1",
			Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: environmentID, Path: rootPath},
		}},
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	return threadStart.Result.(*ThreadStartResponse).Thread.ID
}

func requiredSkillsTurnParams(threadID string, environmentID string) *turn.TurnStartParams {
	return &turn.TurnStartParams{
		ThreadID:     threadID,
		Environments: []map[string]any{{"environmentId": environmentID, "cwd": "/tmp/" + environmentID}},
	}
}

func runRequiredSkillsTurn(t *testing.T, agent *requiredSkillsWiringAgent, threadID string, hook turn.AgentPreSamplingValidation) error {
	t.Helper()
	loop := turn.NewAgentLoop(&turn.AgentLoopOptions{Agent: agent, MaxTurns: 3})
	_, err := loop.Run(context.Background(), &turn.AgentLoopRequest{
		Prompt:                "Review the workspace.",
		Model:                 "gpt-test",
		ThreadID:              threadID,
		TurnID:                "turn-1",
		PreSamplingValidation: hook,
	})
	return err
}

// TestRequiredSkillsPreSamplingValidationGatesTheTurnLikeRust covers the Rust
// #51157 wiring in `Session::run_turn`: app-server turns carry the environment
// requirement gate into the turn loop, and the gate fails the turn before its
// first model request. The requirement is read from the registered environment
// (`environment/add` or `environments.toml`) and the discovered skills come from
// the thread's selected capability roots, exactly as the skills tools read them.
func TestRequiredSkillsPreSamplingValidationGatesTheTurnLikeRust(t *testing.T) {
	router := newRequiredSkillsRouter(t, t.TempDir())
	manager := router.services.Environment
	capabilityRoot := requiredSkillsTestSkill(t)
	threadID := startRequiredSkillsThread(t, router, "required", capabilityRoot)

	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "required",
		ExecServerURL: "ws://example.test/exec",
		Skills:        &EnvironmentSkillsParams{Required: []string{"review"}},
	}); err != nil {
		t.Fatalf("Add(required) error = %v", err)
	}
	environments := requiredSkillsTurnParams(threadID, "required").Environments

	// A turn that selects no requiring environment is not gated at all, so a
	// caller can assign the hook unconditionally.
	if hook := router.requiredSkillsPreSamplingValidation(&turn.TurnStartParams{ThreadID: threadID}, threadID, "turn-empty"); hook != nil {
		t.Fatal("hook for a turn without environments = non-nil, want nil")
	}
	// Rust exempts isolated Guardian reviewers (`is_basic_session_source`).
	guardian := &turn.TurnStartParams{ThreadID: threadID, Originator: "guardian", Environments: environments}
	if hook := router.requiredSkillsPreSamplingValidation(guardian, threadID, "turn-guardian"); hook != nil {
		t.Fatal("guardian hook = non-nil, want nil")
	}
	// A host without environment services cannot require anything.
	if hook := (&RuntimeRouter{}).requiredSkillsPreSamplingValidation(
		&turn.TurnStartParams{ThreadID: threadID, Environments: environments}, threadID, "turn-no-env",
	); hook != nil {
		t.Fatal("hook without environment services = non-nil, want nil")
	}

	// The registered environment requires `review` and its exec server is
	// unreachable here, so the turn fails before any model request. Rust fails
	// the turn when environment setup fails, so the requirement still applies.
	params := requiredSkillsTurnParams(threadID, "required")
	hook := router.requiredSkillsPreSamplingValidation(params, threadID, "turn-1")
	if hook == nil {
		t.Fatal("hook for a requiring environment = nil, want the pre-sampling gate")
	}
	agent := &requiredSkillsWiringAgent{}
	err := runRequiredSkillsTurn(t, agent, threadID, hook)
	if err == nil || !strings.Contains(err.Error(), `Required skill "review" from environment "required" is unavailable`) {
		t.Fatalf("Run() error = %v, want the missing required skill", err)
	}
	if len(agent.requests) != 0 {
		t.Fatalf("model requests = %d, want 0 (the turn must fail before inference)", len(agent.requests))
	}
}

// TestRequiredSkillsPreSamplingValidationReleasesSatisfiedTurnsLikeRust is the
// positive half of the same wiring: when the requiring environment really
// supplies the skill, the gate passes and the turn reaches the model. The
// environment is a live exec server serving this process's filesystem, so the
// skill is discovered through the same `fs/walk` path a real remote executor
// uses — a satisfying skill must be enabled and come from the requiring
// environment, which is why the capability root is declared in that environment.
func TestRequiredSkillsPreSamplingValidationReleasesSatisfiedTurnsLikeRust(t *testing.T) {
	router := newRequiredSkillsRouter(t, t.TempDir())
	manager := router.services.Environment
	capabilityRoot := requiredSkillsTestSkill(t)

	server := execserverclient.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addresses, writer := io.Pipe()
	defer writer.Close()
	go func() { _ = server.ServeWebSocket(ctx, "127.0.0.1:0", writer) }()
	scanner := bufio.NewScanner(addresses)
	if !scanner.Scan() {
		t.Fatalf("exec server did not report its address: %v", scanner.Err())
	}
	execServerURL := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(execServerURL, "ws://") {
		t.Fatalf("exec server address = %q, want a websocket URL", execServerURL)
	}
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "supplied",
		ExecServerURL: execServerURL,
		Skills:        &EnvironmentSkillsParams{Required: []string{"review"}},
	}); err != nil {
		t.Fatalf("Add(supplied) error = %v", err)
	}
	threadID := startRequiredSkillsThread(t, router, "supplied", "file://"+capabilityRoot)

	hook := router.requiredSkillsPreSamplingValidation(requiredSkillsTurnParams(threadID, "supplied"), threadID, "turn-1")
	if hook == nil {
		t.Fatal("hook for a requiring environment = nil, want the pre-sampling gate")
	}
	agent := &requiredSkillsWiringAgent{}
	if err := runRequiredSkillsTurn(t, agent, threadID, hook); err != nil {
		t.Fatalf("Run() error = %v, want the satisfied requirement to reach the model", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("model requests = %d, want 1 (the satisfied requirement must not block inference)", len(agent.requests))
	}
}

// TestApplyProviderSnapshotProjectsTOMLRequiredSkillsLikeRust mirrors the
// manager half of Rust #51157's
// `required_skills_from_toml_reach_their_environment`: an `environments.toml`
// entry declares its own `skills.required`, it reaches only that environment's
// registered record, and a turn selecting that environment enforces it exactly
// like one sent through `environment/add`.
func TestApplyProviderSnapshotProjectsTOMLRequiredSkillsLikeRust(t *testing.T) {
	home := t.TempDir()
	contents := `include_local = false
[[environments]]
id = "training"
url = "ws://127.0.0.1:4512"
[environments.skills]
required = ["computer-use"]

[[environments]]
id = "other"
url = "ws://127.0.0.1:4513"
`
	if err := os.WriteFile(filepath.Join(home, execserverclient.EnvironmentsTOMLFile), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(environments.toml) error = %v", err)
	}
	snapshot, err := execserverclient.EnvironmentProviderFromCodexHome(home)
	if err != nil {
		t.Fatalf("EnvironmentProviderFromCodexHome() error = %v", err)
	}
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	if err := manager.ApplyProviderSnapshot(snapshot); err != nil {
		t.Fatalf("ApplyProviderSnapshot() error = %v", err)
	}
	if got := manager.RequiredSkills("training"); len(got) != 1 || got[0] != "computer-use" {
		t.Fatalf("training required skills = %#v, want [computer-use]", got)
	}
	if got := manager.RequiredSkills("other"); len(got) != 0 {
		t.Fatalf("other required skills = %#v, want none", got)
	}
	selections := mcp.NewSelectedEnvironments([]mcp.TurnEnvironmentSelection{
		{EnvironmentID: "training", State: mcp.EnvironmentSelectionReady},
		{EnvironmentID: "other", State: mcp.EnvironmentSelectionReady},
	})
	requirements := manager.RequiredSkillsForSelections(selections)
	if len(requirements) != 1 || requirements[0].EnvironmentID != "training" ||
		len(requirements[0].SkillNames) != 1 || requirements[0].SkillNames[0] != "computer-use" {
		t.Fatalf("requirements = %#v, want only training's TOML requirement", requirements)
	}
}

// TestRuntimeRunCallSitesWireRequiredSkillsValidationLikeRust pins the wiring
// itself: every app-server turn started through `runtime.Run` — the regular turn
// and the review turn — carries the Rust #51157 requirement gate, so the check
// cannot be dropped from the real turn path while its unit tests keep passing.
func TestRuntimeRunCallSitesWireRequiredSkillsValidationLikeRust(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("appserver", "turn_runtime.go"))
	if err != nil {
		// The test runs from the package directory, where the file is local.
		source, err = os.ReadFile("turn_runtime.go")
		if err != nil {
			t.Fatalf("read turn_runtime.go: %v", err)
		}
	}
	text := string(source)
	const callSite = "runtime.Run(ctx, &turn.AgentLoopRequest{"
	const wiring = "r.requiredSkillsPreSamplingValidation("
	callSites := strings.Count(text, callSite)
	if callSites == 0 {
		t.Fatalf("no runtime.Run call sites found")
	}
	if wired := strings.Count(text, wiring); wired != callSites {
		t.Fatalf("runtime.Run call sites = %d but wired gates = %d, want one gate per turn path", callSites, wired)
	}
}
