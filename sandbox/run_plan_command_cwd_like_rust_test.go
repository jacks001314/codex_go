package sandbox

import "testing"

// Rust #46112 separates the command's launch directory from the sandbox policy
// cwd so a removed policy directory cannot block a filesystem helper.
func TestPlanCommandCWDDefaultsToPolicyCWDLikeRust(t *testing.T) {
	if got := planCommandCWD(&CommandRunPlan{CWD: "/policy/cwd"}); got != "/policy/cwd" {
		t.Fatalf("planCommandCWD(default) = %q, want the policy cwd", got)
	}
	if got := planCommandCWD(&CommandRunPlan{CWD: "/policy/cwd", CommandCWD: "/"}); got != "/" {
		t.Fatalf("planCommandCWD(explicit) = %q, want the launch root", got)
	}
	if got := planCommandCWD(&CommandRunPlan{CWD: "/policy/cwd", CommandCWD: "  "}); got != "/policy/cwd" {
		t.Fatalf("planCommandCWD(blank) = %q, want the policy cwd fallback", got)
	}
	if got := planCommandCWD(nil); got != "" {
		t.Fatalf("planCommandCWD(nil) = %q, want empty", got)
	}
}

func TestBuildCommandRunPlanCarriesCommandCWDLikeRust(t *testing.T) {
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		CWD:        "/policy/cwd",
		CommandCWD: "/",
		Command:    []string{"/opt/codex/codex", "--codex-run-as-fs-helper"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if plan.CWD != "/policy/cwd" {
		t.Fatalf("plan.CWD = %q, want the policy cwd", plan.CWD)
	}
	if plan.CommandCWD != "/" {
		t.Fatalf("plan.CommandCWD = %q, want the launch root", plan.CommandCWD)
	}
	if got := planCommandCWD(plan); got != "/" {
		t.Fatalf("planCommandCWD(plan) = %q, want the launch root", got)
	}
}
