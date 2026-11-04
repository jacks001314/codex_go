package tool

import (
	"errors"
	"runtime"
	"strings"

	"codex_go/execpolicy"
	"codex_go/safety"
	"codex_go/sandbox"
	shellutil "codex_go/shell"
	"codex_go/utils"
)

// shellExecPolicyContext is the state Rust's ExecApprovalRequest carries into
// create_exec_approval_requirement_for_shell.
type shellExecPolicyContext struct {
	policy              *execpolicy.Policy
	approvalPolicy      sandbox.AskForApproval
	permissionProfile   *sandbox.PermissionProfile
	sandboxPermissions  sandbox.SandboxPermissions
	windowsSandboxLevel sandbox.WindowsSandboxLevel
	platform            execpolicy.DangerousCommandPlatform
	commands            [][]string
	origin              execPolicyCommandOrigin
}

// execPolicyCommandOrigin mirrors Rust's ExecPolicyCommandOrigin. The origin
// selects which dangerous-command classifier applies: a lowered PowerShell
// command is judged by PowerShell word rules instead of generic argv rules.
type execPolicyCommandOrigin int

const (
	execPolicyOriginGeneric execPolicyCommandOrigin = iota
	execPolicyOriginPowerShell
	execPolicyOriginPowerShellLiteral
)

type execPolicyCommands struct {
	commands [][]string
	origin   execPolicyCommandOrigin
}

// applyExecPolicyRequirement mirrors Rust's
// ExecPolicyManager::create_exec_approval_requirement_for_shell together with
// the orchestrator's handling of that requirement. Every policy-visible
// command is matched against the session's exec policy; a command without a
// rule match falls back to the heuristics decision derived from the approval
// policy, permission profile, and dangerous-command rules. The requirement
// either rejects the call, marks it as needing approval (carrying the
// exec-policy amendment the approval menu may offer), or skips approval.
func applyExecPolicyRequirement(request *ShellRequest, validation ShellValidationOptions, platform execpolicy.DangerousCommandPlatform) error {
	if request == nil {
		return nil
	}
	context := shellExecPolicyContextFor(request, validation, platform)
	evaluation := evaluateShellExecPolicy(context)
	prefixRule := validatedExecPolicyPrefixRule(context, evaluation, request.PrefixRule)
	requirement := safety.RequirementFromEvaluation(
		request.Command,
		shellApprovalPolicy(&validation),
		evaluation,
		prefixRule,
		!validation.IgnorePrefixRules,
	)
	switch requirement.Kind {
	case safety.ExecForbidden:
		if dangerousCommandApprovalWithinPermissions(request, validation, context, evaluation, requirement) {
			if validation.PermissionsPreapproved {
				return nil
			}
			// Rust #59120: reviewing a dangerous command does not grant extra
			// permissions, and no reusable amendment is offered for this approval.
			request.ApprovalRequired = true
			request.ApprovalReason = ""
			request.PrefixRule = nil
			return nil
		}
		reason := strings.TrimSpace(requirement.Reason)
		if reason == "" {
			reason = "command rejected by exec policy"
		}
		return errors.New(utils.TruncateMiddleChars(reason, execCommandRejectionMaxBytes))
	case safety.ExecNeedsApproval, safety.ExecNeedsHumanApproval:
		// An invocation that already carries an approval does not ask again;
		// Rust's orchestrator consults the requirement before the first
		// attempt only.
		if validation.PermissionsPreapproved {
			return nil
		}
		request.ApprovalRequired = true
		if reason := strings.TrimSpace(requirement.Reason); reason != "" {
			request.ApprovalReason = reason
		}
		// The approval request carries exactly the requirement's amendment; a
		// requested prefix that failed validation is not offered again.
		request.PrefixRule = nil
		if amendment := requirement.Amendment; amendment != nil {
			request.PrefixRule = append([]string(nil), amendment.PrefixRule...)
		}
	case safety.ExecSkip:
		if validation.PermissionsPreapproved {
			return nil
		}
		request.ApprovalRequired = false
		// Rust's Skip requirement still carries a proposed amendment: it is only
		// used if the sandboxed attempt is denied and the operator is asked to
		// retry without the sandbox, so the retry prompt can offer to remember
		// the command. The model's own prefix rule is not reused here.
		request.PrefixRule = nil
		if amendment := requirement.Amendment; amendment != nil {
			request.PrefixRule = append([]string(nil), amendment.PrefixRule...)
		}
		if requirement.BypassSandbox {
			bypassSandboxForExecPolicyAllow(request)
		}
	}
	return nil
}

// dangerousCommandApprovalWithinPermissions mirrors Rust #59120: a heuristic
// dangerous-command prompt may still reach review when granular sandbox
// approvals are disabled, provided the call asks for no sandbox override and
// the Windows sandbox is actually protecting a managed filesystem profile.
func dangerousCommandApprovalWithinPermissions(
	request *ShellRequest,
	validation ShellValidationOptions,
	context shellExecPolicyContext,
	evaluation safety.Evaluation,
	requirement safety.ExecRequirement,
) bool {
	if request == nil || strings.TrimSpace(requirement.Reason) != safety.RejectSandboxApprovalReason {
		return false
	}
	policy := shellApprovalPolicy(&validation)
	if policy.Mode != safety.ApprovalGranular || policy.SandboxApproval {
		return false
	}
	promptIsRule := false
	for _, match := range evaluation.MatchedRules {
		if match.IsPolicy && match.Decision == evaluation.Decision {
			promptIsRule = true
			break
		}
	}
	if promptIsRule {
		return false
	}
	dangerous := false
	for _, command := range context.commands {
		if _, ok := execPolicyDangerousCommandMatch(command, context); ok {
			dangerous = true
			break
		}
	}
	if !dangerous || RequestsSandboxOverride(request.SandboxPermissions) {
		return false
	}
	return !(context.platform == execpolicy.DangerousCommandPlatformWindows &&
		context.windowsSandboxLevel == sandbox.WindowsSandboxDisabled &&
		execPolicyPermissionProfileRestricted(context.permissionProfile))
}

func shellExecPolicyContextFor(request *ShellRequest, validation ShellValidationOptions, platform execpolicy.DangerousCommandPlatform) shellExecPolicyContext {
	parsed := execPolicyCommandsForApproval(request.Command, platform)
	context := shellExecPolicyContext{
		policy:              validation.ExecPolicy,
		approvalPolicy:      validation.ApprovalPolicy,
		permissionProfile:   request.PermissionProfile,
		sandboxPermissions:  request.SandboxPermissions,
		windowsSandboxLevel: validation.WindowsSandboxLevel,
		platform:            platform,
		commands:            parsed.commands,
		origin:              parsed.origin,
	}
	if validation.PermissionsPreapproved || validation.AdditionalPermissionsPreapproved {
		// Rust evaluates an already-approved invocation with the default
		// permissions, so an explicit escalation does not prompt twice.
		context.sandboxPermissions = sandbox.SandboxPermissionsUseDefault
	}
	if validation.IgnorePrefixRules {
		// Rust's AllowPrefixRules::IgnoreForCyberModel drops allow rules and
		// stops proposing reusable amendments.
		context.policy = execPolicyWithoutAllowRules(context.policy)
	}
	return context
}

// execPolicyCommandsForApproval mirrors Rust
// commands_for_exec_policy_for_platform: a POSIX shell script made only of
// literal words joined by `&&`, `||`, `;`, or `|` is checked command by
// command, and Windows PowerShell `-Command` bodies are lowered the same way.
// Anything the literal parser cannot see statically is checked as the raw argv.
func execPolicyCommandsForApproval(command []string, platform execpolicy.DangerousCommandPlatform) execPolicyCommands {
	if _, script, ok := shellutil.ExtractPOSIXShellCommand(command); ok {
		if commands, ok := shellutil.ParsePlainShellCommands(script); ok && len(commands) > 0 {
			return execPolicyCommands{commands: commands, origin: execPolicyOriginGeneric}
		}
	}
	if platform == execpolicy.DangerousCommandPlatformWindows {
		if _, script, ok := shellutil.ExtractPowerShellCommand(command); ok {
			// Go prefixes PowerShell scripts with the UTF-8 console setup before
			// the approval requirement is derived, while Rust derives it from the
			// unprefixed command. Strip the same prefix so the lowered commands
			// match what the model actually asked to run.
			script = strings.TrimLeft(script, " \t\r\n")
			script = strings.TrimPrefix(script, shellutil.UTF8OutputPrefix)
			if commands, ok := shellutil.ParsePlainPowerShellCommands(script); ok && len(commands) > 0 {
				origin := execPolicyOriginPowerShell
				if execpolicy.PowerShellCommandsAreIndependent(script, commands) {
					origin = execPolicyOriginPowerShellLiteral
				}
				return execPolicyCommands{commands: commands, origin: origin}
			}
		}
	}
	return execPolicyCommands{
		commands: [][]string{append([]string(nil), command...)},
		origin:   execPolicyOriginGeneric,
	}
}

// execPolicyDangerousCommandMatch applies the dangerous-command classifier for
// the parsed command's origin (Rust `dangerous_command_match_for_origin`).
func execPolicyDangerousCommandMatch(command []string, context shellExecPolicyContext) (execpolicy.DangerousCommandMatch, bool) {
	switch context.origin {
	case execPolicyOriginPowerShell:
		return execpolicy.MatchDangerousPowerShellWordsForPlatform(command, context.platform)
	case execPolicyOriginPowerShellLiteral:
		return execpolicy.MatchDangerousPowerShellLiteralWords(command)
	default:
		return execpolicy.MatchDangerousCommandForPlatform(command, context.platform)
	}
}

func evaluateShellExecPolicy(context shellExecPolicyContext) safety.Evaluation {
	matches := make([]safety.RuleMatch, 0, len(context.commands))
	for _, command := range context.commands {
		matched := execPolicyMatches(context.policy, command)
		if len(matched) > 0 && execPolicyAllAllow(matched) {
			// Go's unified-exec safety gate (#42113): a dangerous command never
			// runs without approval, even when an allow rule matches it.
			if _, dangerous := execPolicyDangerousCommandMatch(command, context); dangerous {
				matches = append(matches, safety.RuleMatch{
					Decision: safety.Decision(execPolicyDangerousDecision(context)),
					Prefix:   append([]string(nil), command...),
				})
				continue
			}
		}
		if len(matched) == 0 {
			matches = append(matches, safety.RuleMatch{
				Decision: safety.Decision(execPolicyFallbackDecision(command, context)),
				Prefix:   append([]string(nil), command...),
			})
			continue
		}
		for _, match := range matched {
			matches = append(matches, safety.RuleMatch{
				Decision: safety.Decision(match.PrefixRuleMatch.Decision),
				IsPolicy: true,
				Prefix:   append([]string(nil), match.PrefixRuleMatch.MatchedPrefix...),
				Reason:   strings.TrimSpace(match.PrefixRuleMatch.Justification),
			})
		}
	}
	return safety.Evaluation{
		Decision:     safety.StrictestDecision(matches),
		MatchedRules: matches,
	}
}

func execPolicyAllAllow(matches []execpolicy.RuleMatch) bool {
	for _, match := range matches {
		if match.PrefixRuleMatch.Decision != execpolicy.DecisionAllow {
			return false
		}
	}
	return true
}

func execPolicyDangerousDecision(context shellExecPolicyContext) execpolicy.Decision {
	if context.approvalPolicy == sandbox.ApprovalNever {
		return execpolicy.DecisionForbidden
	}
	return execpolicy.DecisionPrompt
}

func execPolicyMatches(policy *execpolicy.Policy, command []string) []execpolicy.RuleMatch {
	if policy == nil {
		return nil
	}
	output := policy.EvaluateCommand(command)
	if output == nil {
		return nil
	}
	return output.MatchedRules
}

// execPolicyFallbackDecision mirrors Rust
// render_decision_for_unmatched_command_for_platform: a command without a rule
// match is never allowed to run unapproved when it is dangerous or when the
// platform sandbox is disabled, and otherwise follows the approval policy.
func execPolicyFallbackDecision(command []string, context shellExecPolicyContext) execpolicy.Decision {
	_, dangerous := execPolicyDangerousCommandMatch(command, context)
	if dangerous || windowsManagedFilesystemRestrictionsWithoutSandbox(context) {
		if context.approvalPolicy == sandbox.ApprovalNever {
			return execpolicy.DecisionForbidden
		}
		return execpolicy.DecisionPrompt
	}
	switch context.approvalPolicy {
	case sandbox.ApprovalNever:
		return execpolicy.DecisionAllow
	case sandbox.ApprovalUnlessTrusted:
		return execpolicy.DecisionPrompt
	default:
		if !execPolicyPermissionProfileRestricted(context.permissionProfile) {
			return execpolicy.DecisionAllow
		}
		if RequestsSandboxOverride(context.sandboxPermissions) {
			return execpolicy.DecisionPrompt
		}
		return execpolicy.DecisionAllow
	}
}

func windowsManagedFilesystemRestrictionsWithoutSandbox(context shellExecPolicyContext) bool {
	return runtime.GOOS == "windows" &&
		context.windowsSandboxLevel == sandbox.WindowsSandboxDisabled &&
		execPolicyPermissionProfileRestricted(context.permissionProfile)
}

// execPolicyPermissionProfileRestricted mirrors
// FileSystemSandboxPolicy::kind == Restricted: disabled and external profiles,
// and managed profiles with full disk write access, run unrestricted.
func execPolicyPermissionProfileRestricted(profile *sandbox.PermissionProfile) bool {
	if profile == nil {
		return true
	}
	if profile.Disabled {
		return false
	}
	policy := profile.LegacySandboxPolicy()
	if policy == nil {
		return true
	}
	if policy.Kind == sandbox.SandboxModeExternalSandbox || policy.HasFullDiskWriteAccess() {
		return false
	}
	return true
}

func execPolicyWithoutAllowRules(policy *execpolicy.Policy) *execpolicy.Policy {
	if policy == nil {
		return nil
	}
	clone := policy.Clone()
	rules := make([]execpolicy.PrefixRule, 0, len(clone.Rules))
	for _, rule := range clone.Rules {
		if rule.Decision == "" || rule.Decision == execpolicy.DecisionAllow {
			continue
		}
		rules = append(rules, rule)
	}
	clone.Rules = rules
	return clone
}

// validatedExecPolicyPrefixRule mirrors Rust
// derive_requested_execpolicy_amendment_from_prefix_rule: the model's
// requested prefix only becomes a proposed amendment when it is not banned, no
// policy rule already matches the command, and adding it would approve every
// parsed command.
func validatedExecPolicyPrefixRule(context shellExecPolicyContext, evaluation safety.Evaluation, prefixRule []string) []string {
	if len(prefixRule) == 0 || safety.BannedPrefixSuggestion(prefixRule) {
		return nil
	}
	for _, match := range evaluation.MatchedRules {
		if match.IsPolicy {
			return nil
		}
	}
	if !execPolicyPrefixApprovesAllCommands(context, prefixRule) {
		return nil
	}
	return append([]string(nil), prefixRule...)
}

func execPolicyPrefixApprovesAllCommands(context shellExecPolicyContext, prefix []string) bool {
	pattern := make([][]string, 0, len(prefix))
	for _, token := range prefix {
		token = strings.TrimSpace(token)
		if token == "" {
			return false
		}
		pattern = append(pattern, []string{token})
	}
	policy := &execpolicy.Policy{}
	if context.policy != nil {
		policy = context.policy.Clone()
	}
	policy.Rules = append(policy.Rules, execpolicy.PrefixRule{
		Pattern:  pattern,
		Decision: execpolicy.DecisionAllow,
	})
	amended := context
	amended.policy = policy
	for _, command := range context.commands {
		matched := execPolicyMatches(policy, command)
		if len(matched) == 0 {
			if execPolicyFallbackDecision(command, amended) != execpolicy.DecisionAllow {
				return false
			}
			continue
		}
		for _, match := range matched {
			if match.PrefixRuleMatch.Decision != execpolicy.DecisionAllow {
				return false
			}
		}
	}
	return true
}

// bypassSandboxForExecPolicyAllow mirrors Rust's
// SandboxOverride::BypassSandboxFirstAttempt for an exec-policy allow rule.
// Denied-read restrictions need the sandbox to stay in force, and a managed
// network proxy must not be dropped, so those keep the sandboxed launch.
func bypassSandboxForExecPolicyAllow(request *ShellRequest) {
	if request == nil || !sandbox.UnsandboxedExecutionAllowed(request.PermissionProfile) {
		return
	}
	if request.EnforceManagedNetwork || request.ManagedNetwork != nil || request.RemoteNetworkProxy != nil {
		return
	}
	profileID, profile, err := effectiveShellPermissionProfile(
		"", nil, sandbox.SandboxPermissionsRequireEscalated, nil, true, request.PathConvention,
	)
	if err != nil || profile == nil {
		return
	}
	request.SandboxPermissions = sandbox.SandboxPermissionsRequireEscalated
	request.PermissionProfileID = profileID
	request.PermissionProfile = profile
	request.PermissionProfileJSON = ""
	request.SandboxProfile = buildShellSandboxProfile(profile, nil, request.CWD)
}
