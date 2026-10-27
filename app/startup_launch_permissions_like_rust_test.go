package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/sandbox"
	codextui "codex_go/tui"
)

// TestStartupApprovalSelectionFromLaunchLikeRust mirrors
// StartupApprovalChoices::from_launch (#56090/#56516): a typed flag wins, then a
// parseable `-c approval_policy` value, then the original assignment boundaries
// for granular/dotted values, and finally an unset server-resolved policy.
func TestStartupApprovalSelectionFromLaunchLikeRust(t *testing.T) {
	parse := func(t *testing.T, raw ...string) []config.Override {
		t.Helper()
		overrides, err := config.ParseOverrides(raw)
		if err != nil {
			t.Fatalf("parse overrides %v: %v", raw, err)
		}
		return overrides
	}
	inlineTable := map[string]any{
		"granular": map[string]any{"request_permissions": true},
	}
	cases := []struct {
		name        string
		typed       string
		raw         []string
		wantPolicy  any
		wantConfig  bool
		wantEntries map[string]any
	}{
		{name: "unset lets the server resolve"},
		{name: "typed policy wins", typed: "on-request", raw: []string{"approval_policy=never"}, wantPolicy: "on-request"},
		{name: "last kv assignment wins", raw: []string{"approval_policy=on-request", "approval_policy=never"}, wantPolicy: "never"},
		{
			name:        "invalid policy stays a config override",
			raw:         []string{"approval_policy=bogus"},
			wantConfig:  true,
			wantEntries: map[string]any{"approval_policy": "bogus"},
		},
		{
			name:       "dotted granular assignments",
			raw:        []string{"approval_policy.granular.sandbox_approval=true", "approval_policy.granular.rules=true"},
			wantConfig: true,
			wantEntries: map[string]any{
				"approval_policy.granular.sandbox_approval": true,
				"approval_policy.granular.rules":            true,
			},
		},
		{
			name:        "parent assignment replaces children",
			raw:         []string{"approval_policy.granular.rules=true", "approval_policy={granular={request_permissions=true}}"},
			wantConfig:  true,
			wantEntries: map[string]any{"approval_policy": inlineTable},
		},
		{
			name:        "inline table only",
			raw:         []string{"approval_policy={granular={request_permissions=true}}"},
			wantConfig:  true,
			wantEntries: map[string]any{"approval_policy": inlineTable},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			selection := resolveStartupApprovalSelection(testCase.typed, parse(t, testCase.raw...))
			if selection.policy != testCase.wantPolicy {
				t.Fatalf("policy = %#v, want %#v", selection.policy, testCase.wantPolicy)
			}
			if selection.configOverride != testCase.wantConfig {
				t.Fatalf("configOverride = %v, want %v", selection.configOverride, testCase.wantConfig)
			}
			if !reflect.DeepEqual(selection.configOverrides, testCase.wantEntries) {
				t.Fatalf("configOverrides = %#v, want %#v", selection.configOverrides, testCase.wantEntries)
			}

			params := appserver.ThreadStartParams{
				ApprovalPolicy: "stale",
				Config:         map[string]any{"approval_policy": "stale", "model": "gpt-5"},
			}
			applyStartupApprovalSelection(&params, selection)
			if params.ApprovalPolicy != selection.policy {
				t.Fatalf("thread approval policy = %#v, want %#v", params.ApprovalPolicy, selection.policy)
			}
			if params.Config["model"] != "gpt-5" {
				t.Fatalf("unrelated config entry lost: %#v", params.Config)
			}
			if !testCase.wantConfig {
				if _, ok := params.Config["approval_policy"]; ok {
					t.Fatalf("resolved approval_policy must not remain in config: %#v", params.Config)
				}
			}
			for path, want := range testCase.wantEntries {
				got, ok := lookupConfigValue(params.Config, splitConfigPath(path))
				if !ok || !reflect.DeepEqual(got, want) {
					t.Fatalf("config %q = %#v/%v, want %#v", path, got, ok, want)
				}
			}
		})
	}
}

func splitConfigPath(path string) []string {
	parts := []string{}
	current := ""
	for _, r := range path {
		if r == '.' {
			parts = append(parts, current)
			current = ""
			continue
		}
		current += string(r)
	}
	return append(parts, current)
}

// TestApplyStartupApprovalsReviewerLikeRust covers
// StartupApprovalsReviewer::Invocation: only an explicit, valid
// `-c approvals_reviewer` value is forwarded, repeated assignments use the last
// value, and the legacy guardian_subagent spelling normalizes to auto_review.
func TestApplyStartupApprovalsReviewerLikeRust(t *testing.T) {
	cases := []struct {
		name string
		raw  []string
		want *string
	}{
		{name: "unset lets the server choose"},
		{name: "user", raw: []string{"approvals_reviewer=user"}, want: stringPointer("user")},
		{name: "last assignment wins", raw: []string{"approvals_reviewer=user", "approvals_reviewer=auto_review"}, want: stringPointer("auto_review")},
		{name: "legacy guardian alias", raw: []string{"approvals_reviewer=guardian_subagent"}, want: stringPointer("auto_review")},
		{name: "invalid value", raw: []string{"approvals_reviewer=guardian"}},
		{name: "non-string value", raw: []string{"approvals_reviewer=true"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			overrides, err := config.ParseOverrides(testCase.raw)
			if err != nil {
				t.Fatalf("parse overrides: %v", err)
			}
			params := appserver.ThreadStartParams{
				Config: map[string]any{"approvals_reviewer": "stale", "model": "gpt-5"},
			}
			applyStartupApprovalsReviewer(&params, overrides)
			if !reflect.DeepEqual(params.ApprovalsReviewer, testCase.want) {
				t.Fatalf("approvals reviewer = %#v, want %#v", params.ApprovalsReviewer, testCase.want)
			}
			if _, ok := params.Config["approvals_reviewer"]; ok {
				t.Fatalf("explicit reviewer must not remain in config: %#v", params.Config)
			}
			if params.Config["model"] != "gpt-5" {
				t.Fatalf("unrelated config entry lost: %#v", params.Config)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

// TestStartupApprovalPolicyAllowedLikeRust covers
// apply_projectless_approval_policy's allow-list check: an absent list is
// unrestricted, while a present list must include the granular policy.
func TestStartupApprovalPolicyAllowedLikeRust(t *testing.T) {
	if !startupApprovalPolicyAllowed(nil) {
		t.Fatal("nil requirements must allow the implicit granular policy")
	}
	if !startupApprovalPolicyAllowed(&config.ConfigRequirements{}) {
		t.Fatal("an absent allow-list must allow the implicit granular policy")
	}
	if startupApprovalPolicyAllowed(&config.ConfigRequirements{AllowedApprovalPolicies: []sandbox.AskForApproval{}}) {
		t.Fatal("a present empty allow-list must not allow the implicit granular policy")
	}
	if !startupApprovalPolicyAllowed(&config.ConfigRequirements{
		AllowedApprovalPolicies: []sandbox.AskForApproval{sandbox.ApprovalNever, sandbox.ApprovalGranular},
	}) {
		t.Fatal("an allow-list admitting granular must allow the implicit policy")
	}
	if startupApprovalPolicyAllowed(&config.ConfigRequirements{
		AllowedApprovalPolicies: []sandbox.AskForApproval{sandbox.ApprovalNever},
	}) {
		t.Fatal("an allow-list without granular must reject the implicit policy")
	}
}

// TestRemoteStartupThreadPermissionsLikeRust exercises the connected fresh-start
// path: only explicit launch choices travel on the first thread, while a later
// session keeps the resolved session state.
func TestRemoteStartupThreadPermissionsLikeRust(t *testing.T) {
	client, _ := newTrustDriverClient(t, map[string]any{
		"model":           "server-model",
		"approval_policy": "never",
	}, nil)
	state := codextui.NewState(&codextui.Options{
		CWD:            t.TempDir(),
		ApprovalPolicy: "never",
	})
	client.state = state
	root := &cli.RootOptions{ConfigOverrides: []string{
		"approval_policy.granular.sandbox_approval=true",
		"approvals_reviewer=user",
		"approvals_reviewer=auto_review",
	}}

	params, err := client.remoteManagedThreadStartParams(context.Background(), root, state)
	if err != nil {
		t.Fatalf("startup thread/start params: %v", err)
	}
	if params.ApprovalPolicy != nil {
		t.Fatalf("startup approval policy = %#v, want unset for the server", params.ApprovalPolicy)
	}
	if !reflect.DeepEqual(params.Config["approval_policy"], map[string]any{
		"granular": map[string]any{"sandbox_approval": true},
	}) {
		t.Fatalf("startup config approval_policy = %#v, want only the launch assignment", params.Config["approval_policy"])
	}
	if _, ok := params.Config["approvals_reviewer"]; ok {
		t.Fatalf("startup config must not carry approvals_reviewer: %#v", params.Config)
	}
	if params.ApprovalsReviewer == nil || *params.ApprovalsReviewer != "auto_review" {
		t.Fatalf("startup approvals reviewer = %#v, want auto_review", params.ApprovalsReviewer)
	}

	state.SetThreadID("thread-later")
	state.ApprovalPolicy = "on-request"
	params, err = client.remoteManagedThreadStartParams(context.Background(), root, state)
	if err != nil {
		t.Fatalf("later thread/start params: %v", err)
	}
	if params.ApprovalPolicy != "on-request" {
		t.Fatalf("later approval policy = %#v, want the resolved session policy", params.ApprovalPolicy)
	}
}

// TestLocalStartupThreadPermissionsLikeRust covers the embedded bootstrap path:
// granular launch assignments keep the projectless sandbox but do not become an
// implicit approval policy, and managed requirements can veto that policy.
func TestLocalStartupThreadPermissionsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	state := codextui.NewState(&codextui.Options{CWD: t.TempDir()})

	granular := &cli.RootOptions{ConfigOverrides: []string{"approval_policy={granular={sandbox_approval=true}}"}}
	params := interactiveLocalThreadStartParams(granular, state)
	if params.ApprovalPolicy != nil {
		t.Fatalf("granular override approval policy = %#v, want unset", params.ApprovalPolicy)
	}
	if params.Sandbox != string(sandbox.SandboxWorkspaceWrite) {
		t.Fatalf("sandbox = %#v, want workspace-write", params.Sandbox)
	}
	if !reflect.DeepEqual(params.Config["approval_policy"], map[string]any{
		"granular": map[string]any{"sandbox_approval": true},
	}) {
		t.Fatalf("approval config = %#v", params.Config["approval_policy"])
	}

	explicit := &cli.RootOptions{ConfigOverrides: []string{"approval_policy=on-request"}}
	params = interactiveLocalThreadStartParams(explicit, state)
	if params.ApprovalPolicy != "on-request" {
		t.Fatalf("explicit approval policy = %#v, want on-request", params.ApprovalPolicy)
	}

	typed := &cli.RootOptions{Shared: cli.SharedOptions{ApprovalPolicy: "never"}}
	params = interactiveLocalThreadStartParams(typed, state)
	if params.ApprovalPolicy != "never" {
		t.Fatalf("typed approval policy = %#v, want never", params.ApprovalPolicy)
	}

	if err := os.WriteFile(filepath.Join(home, "requirements.toml"),
		[]byte("allowed_approval_policies = [\"on-request\"]\n"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	params = interactiveLocalThreadStartParams(&cli.RootOptions{}, state)
	if params.ApprovalPolicy != nil {
		t.Fatalf("managed requirements approval policy = %#v, want unset", params.ApprovalPolicy)
	}
	if params.Sandbox != string(sandbox.SandboxWorkspaceWrite) {
		t.Fatalf("managed requirements sandbox = %#v, want workspace-write", params.Sandbox)
	}
}
