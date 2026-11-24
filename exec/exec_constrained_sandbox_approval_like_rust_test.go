package exec

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/sandbox"
)

// Rust #63515: requirements may downgrade an explicit full-access selector,
// but the resulting read-only profile is invalid when approvals are inherited
// as never because the original request can no longer ask for access.
func TestExecRejectsRequirementConstrainedFullAccessWithNeverLikeRust(t *testing.T) {
	cfg := &config.Config{
		Values: map[string]any{},
		Requirements: &config.ConfigRequirements{
			AllowedSandboxModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly},
		},
	}
	runner := &Runner{}
	newRequest := func(selector string) *Request {
		req := &Request{}
		req.Exec.Shared.CWD = t.TempDir()
		req.Exec.Shared.Sandbox = selector
		return req
	}

	req := newRequest(sandbox.BuiltInPermissionProfileDangerFullAccess)
	effective, err := runner.resolveExecPermissionProfileForRun(cfg, req, nil)
	if err != nil {
		t.Fatalf("resolve constrained full access: %v", err)
	}
	if profile := sandboxPermissionProfile(effective); profile == nil || profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("effective constrained profile = %#v, want read-only", effective)
	}
	err = validateExecRequirementConstrainedSandbox(cfg, req, effective, sandbox.ApprovalNever)
	if err == nil || !strings.Contains(err.Error(), "approval_policy = \"never\"") || !strings.Contains(err.Error(), "danger-full-access") {
		t.Fatalf("never validation error = %v", err)
	}
	if err := validateExecRequirementConstrainedSandbox(cfg, req, effective, sandbox.ApprovalOnRequest); err != nil {
		t.Fatalf("on-request validation error = %v", err)
	}

	namedCfg := &config.Config{
		Values: map[string]any{
			"permissions": map[string]any{
				"yolo": map[string]any{"extends": ":" + sandbox.BuiltInPermissionProfileDangerFullAccess},
			},
		},
		Requirements: cfg.Requirements,
	}
	namedReq := newRequest("yolo")
	namedEffective, err := runner.resolveExecPermissionProfileForRun(namedCfg, namedReq, nil)
	if err != nil {
		t.Fatalf("resolve constrained named profile: %v", err)
	}
	if err := validateExecRequirementConstrainedSandbox(namedCfg, namedReq, namedEffective, sandbox.ApprovalNever); err == nil {
		t.Fatal("named full-access profile downgraded under never was accepted")
	}

	readOnly := newRequest(sandbox.BuiltInPermissionProfileReadOnly)
	readOnlyResolution, err := runner.resolveExecPermissionProfileForRun(cfg, readOnly, nil)
	if err != nil {
		t.Fatalf("resolve read-only: %v", err)
	}
	if err := validateExecRequirementConstrainedSandbox(cfg, readOnly, readOnlyResolution, sandbox.ApprovalNever); err != nil {
		t.Fatalf("explicit read-only with never was rejected: %v", err)
	}
}

func TestRunRejectsRequirementConstrainedFullAccessWithNeverLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"never\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_sandbox_modes = [\"read-only\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "done"}
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "hello",
			Shared: cli.SharedOptions{Sandbox: sandbox.BuiltInPermissionProfileDangerFullAccess},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "danger-full-access") || !strings.Contains(err.Error(), "approvals disabled") {
		t.Fatalf("Run error = %v, want the requirement-constrained approval error", err)
	}
}
