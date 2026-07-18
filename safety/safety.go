package safety

import (
	"path/filepath"
	"strings"
)

const (
	PromptConflictReason        = "approval required by policy, but AskForApproval is set to Never"
	RejectSandboxApprovalReason = "approval required by policy, but AskForApproval::Granular.sandbox_approval is false"
	RejectRulesApprovalReason   = "approval required by policy rule, but AskForApproval::Granular.rules is false"

	PatchRejectedOutsideProjectReason = "writing outside of the project; rejected by user approval settings"
	PatchRejectedReadOnlyReason       = "writing is blocked by read-only sandbox; rejected by user approval settings"
)

type ApprovalMode string

const (
	ApprovalNever         ApprovalMode = "never"
	ApprovalOnRequest     ApprovalMode = "on-request"
	ApprovalUnlessTrusted ApprovalMode = "unless-trusted"
	ApprovalGranular      ApprovalMode = "granular"
)

type ApprovalPolicy struct {
	Mode            ApprovalMode
	Rules           bool
	SandboxApproval bool
}

type Decision string

const (
	DecisionAllow     Decision = "allow"
	DecisionPrompt    Decision = "prompt"
	DecisionForbidden Decision = "forbidden"
)

type ExecRequirementKind string

const (
	ExecSkip          ExecRequirementKind = "skip"
	ExecNeedsApproval ExecRequirementKind = "needs_approval"
	ExecForbidden     ExecRequirementKind = "forbidden"
)

type ExecRequirement struct {
	Kind          ExecRequirementKind
	Reason        string
	BypassSandbox bool
	Amendment     *ExecPolicyAmendment
}

type ExecPolicyAmendment struct {
	PrefixRule []string
	Decision   Decision
}

type RuleMatch struct {
	Decision Decision
	IsPolicy bool
	Prefix   []string
	Reason   string
}

type Evaluation struct {
	Decision     Decision
	MatchedRules []RuleMatch
}

func PromptRejectedByPolicy(policy ApprovalPolicy, promptIsRule bool) (string, bool) {
	switch policy.Mode {
	case ApprovalNever:
		return PromptConflictReason, true
	case ApprovalGranular:
		if promptIsRule && !policy.Rules {
			return RejectRulesApprovalReason, true
		}
		if !promptIsRule && !policy.SandboxApproval {
			return RejectSandboxApprovalReason, true
		}
	}
	return "", false
}

func RequirementFromEvaluation(command []string, policy ApprovalPolicy, evaluation Evaluation, prefixRule []string, autoAmendmentAllowed bool) ExecRequirement {
	switch evaluation.Decision {
	case DecisionForbidden:
		return ExecRequirement{Kind: ExecForbidden, Reason: reasonForDecision(command, &evaluation, "forbidden by policy")}
	case DecisionPrompt:
		promptIsRule := false
		for _, match := range evaluation.MatchedRules {
			if match.IsPolicy && match.Decision == DecisionPrompt {
				promptIsRule = true
				break
			}
		}
		if reason, rejected := PromptRejectedByPolicy(policy, promptIsRule); rejected {
			return ExecRequirement{Kind: ExecForbidden, Reason: reason}
		}
		return ExecRequirement{
			Kind:      ExecNeedsApproval,
			Reason:    reasonForDecision(command, &evaluation, "approval required by policy"),
			Amendment: requestedAmendment(prefixRule, autoAmendmentAllowed, evaluation.MatchedRules),
		}
	case DecisionAllow:
		return ExecRequirement{Kind: ExecSkip, BypassSandbox: allMatchesArePolicyAllows(evaluation.MatchedRules)}
	default:
		return ExecRequirement{Kind: ExecForbidden, Reason: "unknown exec policy decision"}
	}
}

func requestedAmendment(prefixRule []string, allowed bool, matches []RuleMatch) *ExecPolicyAmendment {
	if !allowed {
		return nil
	}
	if len(prefixRule) > 0 && !BannedPrefixSuggestion(prefixRule) {
		return &ExecPolicyAmendment{PrefixRule: append([]string(nil), prefixRule...), Decision: DecisionAllow}
	}
	for _, match := range matches {
		if match.IsPolicy && match.Decision == DecisionPrompt && len(match.Prefix) > 0 && !BannedPrefixSuggestion(match.Prefix) {
			return &ExecPolicyAmendment{PrefixRule: append([]string(nil), match.Prefix...), Decision: DecisionAllow}
		}
	}
	return nil
}

func BannedPrefixSuggestion(prefix []string) bool {
	if len(prefix) == 0 {
		return true
	}
	joined := strings.ToLower(strings.Join(prefix, "\x00"))
	for _, banned := range bannedPrefixes() {
		if joined == strings.ToLower(strings.Join(banned, "\x00")) {
			return true
		}
	}
	return false
}

func bannedPrefixes() [][]string {
	return [][]string{
		{"python3"}, {"python3", "-"}, {"python3", "-c"},
		{"python"}, {"python", "-"}, {"python", "-c"},
		{"py"}, {"py", "-3"},
		{"git"},
		{"bash"}, {"bash", "-lc"},
		{"sh"}, {"sh", "-c"}, {"sh", "-lc"},
		{"zsh"}, {"zsh", "-lc"},
		{"pwsh"}, {"pwsh", "-Command"}, {"pwsh", "-c"},
		{"powershell"}, {"powershell", "-Command"}, {"powershell", "-c"},
		{"powershell.exe"}, {"powershell.exe", "-Command"}, {"powershell.exe", "-c"},
		{"env"}, {"sudo"},
		{"node"}, {"node", "-e"},
		{"perl"}, {"perl", "-e"},
		{"ruby"}, {"ruby", "-e"},
		{"php"}, {"php", "-r"},
		{"lua"}, {"lua", "-e"},
		{"osascript"},
	}
}

func reasonForDecision(command []string, evaluation *Evaluation, fallback string) string {
	for _, match := range evaluation.MatchedRules {
		if match.Reason != "" {
			return match.Reason
		}
	}
	if len(command) == 0 {
		return fallback
	}
	return fallback + ": " + strings.Join(command, " ")
}

func allMatchesArePolicyAllows(matches []RuleMatch) bool {
	if len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		if !match.IsPolicy || match.Decision != DecisionAllow {
			return false
		}
	}
	return true
}

type FileChangeKind string

const (
	FileAdd    FileChangeKind = "add"
	FileDelete FileChangeKind = "delete"
	FileUpdate FileChangeKind = "update"
)

type FileChange struct {
	Kind     FileChangeKind
	Path     string
	MovePath string
}

type PatchAction struct {
	Changes []FileChange
}

type PermissionProfileKind string

const (
	PermissionDisabled PermissionProfileKind = "disabled"
	PermissionExternal PermissionProfileKind = "external"
	PermissionManaged  PermissionProfileKind = "managed"
)

type FileSystemPolicy struct {
	FullDiskWrite bool
	WritableRoots []string
}

type SafetyCheckKind string

const (
	SafetyAutoApprove SafetyCheckKind = "auto_approve"
	SafetyAskUser     SafetyCheckKind = "ask_user"
	SafetyReject      SafetyCheckKind = "reject"
)

type PatchSafetyCheck struct {
	Kind                   SafetyCheckKind
	SandboxType            string
	UserExplicitlyApproved bool
	Reason                 string
}

func AssessPatchSafety(action *PatchAction, policy ApprovalPolicy, permissionKind PermissionProfileKind, filePolicy FileSystemPolicy, cwd string, platformSandbox string) PatchSafetyCheck {
	if action == nil || len(action.Changes) == 0 {
		return PatchSafetyCheck{Kind: SafetyReject, Reason: "empty patch"}
	}
	if policy.Mode == ApprovalUnlessTrusted {
		return PatchSafetyCheck{Kind: SafetyAskUser}
	}
	rejectsSandboxApproval := policy.Mode == ApprovalNever || (policy.Mode == ApprovalGranular && !policy.SandboxApproval)
	if WritePatchConstrainedToWritablePaths(action, &filePolicy, cwd) {
		if permissionKind == PermissionDisabled || permissionKind == PermissionExternal {
			return PatchSafetyCheck{Kind: SafetyAutoApprove, SandboxType: "none"}
		}
		if platformSandbox != "" {
			return PatchSafetyCheck{Kind: SafetyAutoApprove, SandboxType: platformSandbox}
		}
		if rejectsSandboxApproval {
			return PatchSafetyCheck{Kind: SafetyReject, Reason: patchRejectionReason(permissionKind, &filePolicy, cwd)}
		}
		return PatchSafetyCheck{Kind: SafetyAskUser}
	}
	if rejectsSandboxApproval {
		return PatchSafetyCheck{Kind: SafetyReject, Reason: patchRejectionReason(permissionKind, &filePolicy, cwd)}
	}
	return PatchSafetyCheck{Kind: SafetyAskUser}
}

func WritePatchConstrainedToWritablePaths(action *PatchAction, policy *FileSystemPolicy, cwd string) bool {
	if action == nil || policy == nil {
		return false
	}
	if policy.FullDiskWrite {
		return true
	}
	for _, change := range action.Changes {
		if !CanWritePath(policy, cwd, change.Path) {
			return false
		}
		if change.Kind == FileUpdate && change.MovePath != "" && !CanWritePath(policy, cwd, change.MovePath) {
			return false
		}
	}
	return true
}

func CanWritePath(policy *FileSystemPolicy, cwd string, path string) bool {
	if policy == nil {
		return false
	}
	if policy.FullDiskWrite {
		return true
	}
	absPath := normalizePath(cwd, path)
	for _, root := range policy.WritableRoots {
		absRoot := normalizePath(cwd, root)
		if absPath == absRoot || strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func patchRejectionReason(permissionKind PermissionProfileKind, policy *FileSystemPolicy, cwd string) string {
	if permissionKind == PermissionManaged && !policy.FullDiskWrite && len(writableRootsWithCWD(policy, cwd)) == 0 {
		return PatchRejectedReadOnlyReason
	}
	return PatchRejectedOutsideProjectReason
}

func writableRootsWithCWD(policy *FileSystemPolicy, cwd string) []string {
	if policy == nil {
		return nil
	}
	out := make([]string, 0, len(policy.WritableRoots))
	for _, root := range policy.WritableRoots {
		out = append(out, normalizePath(cwd, root))
	}
	return out
}

func normalizePath(cwd string, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

type NetworkProtocol string

const (
	ProtocolHTTP      NetworkProtocol = "http"
	ProtocolHTTPS     NetworkProtocol = "https"
	ProtocolSocks5TCP NetworkProtocol = "socks5_tcp"
	ProtocolSocks5UDP NetworkProtocol = "socks5_udp"
)

type NetworkDecisionPayload struct {
	Decision string
	Protocol NetworkProtocol
	Host     string
}

type NetworkApprovalContext struct {
	Host     string
	Protocol NetworkProtocol
}

type BlockedRequest struct {
	Decision string
	Host     string
	Reason   string
}

type NetworkRuleAction string

const (
	NetworkRuleAllow NetworkRuleAction = "allow"
	NetworkRuleDeny  NetworkRuleAction = "deny"
)

type NetworkPolicyAmendment struct {
	Action NetworkRuleAction
}

type ExecPolicyNetworkRuleAmendment struct {
	Protocol      NetworkProtocol
	Decision      Decision
	Justification string
}

func NetworkApprovalContextFromPayload(payload *NetworkDecisionPayload) (*NetworkApprovalContext, bool) {
	if payload == nil || payload.Decision != "ask" || payload.Protocol == "" || strings.TrimSpace(payload.Host) == "" {
		return nil, false
	}
	return &NetworkApprovalContext{Host: strings.TrimSpace(payload.Host), Protocol: payload.Protocol}, true
}

func DeniedNetworkPolicyMessage(blocked *BlockedRequest) (string, bool) {
	if blocked == nil || blocked.Decision != "deny" {
		return "", false
	}
	host := strings.TrimSpace(blocked.Host)
	if host == "" {
		return "Network access was blocked by policy.", true
	}
	detail := "request is blocked by network policy"
	switch blocked.Reason {
	case "denied":
		detail = "domain is explicitly denied by policy and cannot be approved from this prompt"
	case "not_allowed":
		detail = "domain is not on the allowlist for the current sandbox mode"
	case "not_allowed_local":
		detail = "local/private network addresses are blocked by the sandbox policy"
	case "method_not_allowed":
		detail = "request method is blocked by the current network mode"
	case "proxy_disabled":
		detail = "network proxy is disabled"
	}
	return `Network access to "` + host + `" was blocked: ` + detail + `.`, true
}

func ExecpolicyNetworkRuleAmendment(amendment *NetworkPolicyAmendment, context *NetworkApprovalContext, host string) *ExecPolicyNetworkRuleAmendment {
	if amendment == nil || context == nil {
		return nil
	}
	decision := DecisionAllow
	verb := "Allow"
	if amendment.Action == NetworkRuleDeny {
		decision = DecisionForbidden
		verb = "Deny"
	}
	return &ExecPolicyNetworkRuleAmendment{
		Protocol:      context.Protocol,
		Decision:      decision,
		Justification: verb + " " + string(context.Protocol) + " access to " + host,
	}
}

func PermissionProfilePolicyTag(permissionKind PermissionProfileKind, policy FileSystemPolicy, cwd string) string {
	switch permissionKind {
	case PermissionDisabled:
		return "danger-full-access"
	case PermissionExternal:
		return "external-sandbox"
	default:
		if policy.FullDiskWrite {
			return "danger-full-access"
		}
		if len(writableRootsWithCWD(&policy, cwd)) == 0 {
			return "read-only"
		}
		return "workspace-write"
	}
}

func PermissionProfileSandboxTag(permissionKind PermissionProfileKind, requiresPlatformSandbox bool, windowsElevated bool, platformSandbox string) string {
	switch permissionKind {
	case PermissionDisabled:
		return "none"
	case PermissionExternal:
		return "external"
	}
	if !requiresPlatformSandbox {
		return "none"
	}
	if windowsElevated {
		return "windows_elevated"
	}
	if platformSandbox == "" {
		return "none"
	}
	return platformSandbox
}
