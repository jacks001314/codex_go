package execserver

import (
	"context"

	"codex_go/sandbox"
	"codex_go/sandbox/integrity"
)

// runSandboxIntegrityChecks wires the observational integrity runner into
// exec-server preparation (Rust #51843). Checker failures and timeouts only
// affect telemetry; the prepared command is never changed.
func runSandboxIntegrityChecks(plan *sandbox.CommandRunPlan, sandboxOverride, cwd string, command []string) {
	if plan == nil || plan.PermissionProfile == nil || plan.PermissionProfile.Disabled {
		return
	}
	integrity.RunIntegrityChecks(context.Background(), &integrity.RunnerRequest{
		SandboxType:     plan.SandboxType,
		SandboxOverride: sandboxOverride,
		Entries:         plan.PermissionProfile.RuntimeFileSystemEntries(),
		CWD:             cwd,
		Command:         command,
	})
}
