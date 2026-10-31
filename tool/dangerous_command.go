package tool

import (
	"errors"
	"fmt"

	"codex_go/execpolicy"
	"codex_go/sandbox"
	shellutil "codex_go/shell"
	"codex_go/utils"
)

// applyDangerousCommandRequirement mirrors Rust #42113's unified-exec safety
// gate: a command matched by the target platform's dangerous-command rules can
// never run without approval, even inside a sandbox, and is rejected outright
// when approvals are disabled. An already-approved call keeps its grant.
func applyDangerousCommandRequirement(request *ShellRequest, policy sandbox.AskForApproval, platform execpolicy.DangerousCommandPlatform, preapproved bool) error {
	if request == nil || preapproved {
		return nil
	}
	match, dangerous := execpolicy.MatchDangerousCommandForPlatform(request.Command, platform)
	if !dangerous {
		return nil
	}
	if policy == sandbox.ApprovalNever {
		// Rust derives the forbidden reason from the dangerous match and the
		// exec_command handler bounds every rejection to a fixed byte budget.
		message := fmt.Sprintf("`%s` rejected: %s", shellutil.ShlexJoin(request.Command), execpolicy.DangerousCommandRejectionReason(match))
		return errors.New(utils.TruncateMiddleChars(message, execCommandRejectionMaxBytes))
	}
	request.ApprovalRequired = true
	return nil
}
