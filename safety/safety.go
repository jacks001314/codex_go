package safety

import (
	"path/filepath"
	"strings"

	"codex_go/utils"
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
	DecisionAllow      Decision = "allow"
	DecisionPrompt     Decision = "prompt"
	DecisionPromptUser Decision = "prompt-user"
	DecisionForbidden  Decision = "forbidden"
)

type ExecRequirementKind string

const (
	ExecSkip               ExecRequirementKind = "skip"
	ExecNeedsApproval      ExecRequirementKind = "needs_approval"
	ExecNeedsHumanApproval ExecRequirementKind = "needs_human_approval"
	ExecForbidden          ExecRequirementKind = "forbidden"
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
	case DecisionPrompt, DecisionPromptUser:
		// Rust #58045 derives the rule-ness from the evaluation's own decision so
		// a prompt-user checkpoint is treated as a policy rule.
		promptIsRule := false
		for _, match := range evaluation.MatchedRules {
			if match.IsPolicy && match.Decision == evaluation.Decision {
				promptIsRule = true
				break
			}
		}
		if reason, rejected := PromptRejectedByPolicy(policy, promptIsRule); rejected {
			return ExecRequirement{Kind: ExecForbidden, Reason: reason}
		}
		if evaluation.Decision == DecisionPromptUser {
			// A managed checkpoint needs a fresh human decision, so it never
			// proposes a reusable exec-policy amendment.
			return ExecRequirement{
				Kind:   ExecNeedsHumanApproval,
				Reason: humanApprovalReason(command, &evaluation),
			}
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

// humanApprovalReason mirrors Rust derive_prompt_reason for a prompt-user
// checkpoint: "`cmd` requires human approval: <justification>" or
// "`cmd` requires human approval by policy".
func humanApprovalReason(command []string, evaluation *Evaluation) string {
	if evaluation != nil {
		for _, match := range evaluation.MatchedRules {
			if match.IsPolicy && match.Decision == DecisionPromptUser {
				if strings.TrimSpace(match.Reason) != "" {
					return "`" + strings.Join(command, " ") + "` requires human approval: " + strings.TrimSpace(match.Reason)
				}
			}
		}
	}
	if len(command) == 0 {
		return "human approval required by policy"
	}
	return "`" + strings.Join(command, " ") + "` requires human approval by policy"
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

// FileSystemPolicyContext is the executor-side view of a filesystem policy.
// Rust #42391 (`FileSystemSandboxPolicyContext`) evaluates patch targets
// against the selected executor's path convention, so a target spelled with a
// foreign convention is no longer misclassified by host-native path conversion.
type FileSystemPolicyContext struct {
	// CWD is the executor working directory. Its path convention decides how
	// relative and absolute policy roots are resolved.
	CWD *utils.PathURI
	// ExtraWritableRoots are roots the caller resolved for the executor, such
	// as the executor's cwd and temporary directory.
	ExtraWritableRoots []*utils.PathURI
	// ProtectedMetadataNames stay read-only inside every writable root unless a
	// root grants them explicitly.
	ProtectedMetadataNames []string
}

// ResolvePath resolves a patch path (absolute in the executor convention or
// relative to the executor cwd) to a path URI without touching the filesystem.
func (c FileSystemPolicyContext) ResolvePath(path string) (*utils.PathURI, bool) {
	if c.CWD == nil {
		return nil, false
	}
	resolved, err := c.CWD.Join(path)
	if err != nil || resolved == nil {
		return nil, false
	}
	return resolved, true
}

// CanWritePath reports whether policy grants write access to target when its
// writable roots are interpreted under the executor path convention. Mirrors
// Rust #42391 `FileSystemSandboxPolicy::can_write_path(path, context)`.
func (c FileSystemPolicyContext) CanWritePath(policy *FileSystemPolicy, target *utils.PathURI) bool {
	if policy == nil || target == nil || c.CWD == nil {
		return false
	}
	if policy.FullDiskWrite {
		return true
	}
	for _, root := range policy.WritableRoots {
		rootURI, err := c.CWD.Join(root)
		if err != nil || rootURI == nil {
			continue
		}
		if c.permitsWrite(rootURI, target) {
			return true
		}
	}
	for _, rootURI := range c.ExtraWritableRoots {
		if c.permitsWrite(rootURI, target) {
			return true
		}
	}
	return false
}

// HasConfiguredWritableRoots mirrors Rust #42391
// `has_configured_writable_roots(context)`: the policy configures a writable
// root that resolves under the executor convention. Full-disk policies report
// false because they carry no narrowing roots.
func (c FileSystemPolicyContext) HasConfiguredWritableRoots(policy *FileSystemPolicy) bool {
	if policy == nil || policy.FullDiskWrite {
		return false
	}
	for _, root := range policy.WritableRoots {
		rootURI, err := c.CWD.Join(root)
		if err != nil || rootURI == nil {
			continue
		}
		if c.CanWritePath(policy, rootURI) {
			return true
		}
	}
	return false
}

func (c FileSystemPolicyContext) permitsWrite(root, target *utils.PathURI) bool {
	if !target.StartsWith(root) {
		return false
	}
	return !c.introducesProtectedMetadata(root, target)
}

func (c FileSystemPolicyContext) introducesProtectedMetadata(root, target *utils.PathURI) bool {
	if len(c.ProtectedMetadataNames) == 0 {
		return false
	}
	relative, ok := lexicalRelativeNativePath(root, target)
	if !ok || relative == "" {
		return false
	}
	first := relative
	if index := strings.IndexAny(relative, `/\`); index >= 0 {
		first = relative[:index]
	}
	convention, _ := target.InferConvention()
	for _, name := range c.ProtectedMetadataNames {
		if name == "" {
			continue
		}
		if first == name || (convention == utils.ConventionWindows && strings.EqualFold(first, name)) {
			return true
		}
	}
	return false
}

// CanWritePathInContext mirrors Rust #42391's context-aware can_write_path.
func CanWritePathInContext(policy *FileSystemPolicy, context FileSystemPolicyContext, target *utils.PathURI) bool {
	return context.CanWritePath(policy, target)
}

// WritePatchConstrainedToWritablePathsInContext mirrors Rust #42391: patch
// containment is decided with the executor's path convention instead of the
// host's.
func WritePatchConstrainedToWritablePathsInContext(action *PatchAction, policy *FileSystemPolicy, context FileSystemPolicyContext) bool {
	if action == nil || policy == nil {
		return false
	}
	if policy.FullDiskWrite {
		return true
	}
	for _, change := range action.Changes {
		target, ok := context.ResolvePath(change.Path)
		if !ok || !context.CanWritePath(policy, target) {
			return false
		}
		if change.Kind == FileUpdate && change.MovePath != "" {
			moveTarget, ok := context.ResolvePath(change.MovePath)
			if !ok || !context.CanWritePath(policy, moveTarget) {
				return false
			}
		}
	}
	return true
}

// AssessPatchSafetyWithContext mirrors Rust #42391's context-aware
// `assess_patch_safety`: writable-root containment and read-only rejection
// reasons follow the executor's path convention. The platform sandbox string
// reports the sandbox the caller can enforce, if any.
func AssessPatchSafetyWithContext(action *PatchAction, policy ApprovalPolicy, permissionKind PermissionProfileKind, filePolicy FileSystemPolicy, context FileSystemPolicyContext, platformSandbox string) PatchSafetyCheck {
	if action == nil || len(action.Changes) == 0 {
		return PatchSafetyCheck{Kind: SafetyReject, Reason: "empty patch"}
	}
	if policy.Mode == ApprovalUnlessTrusted {
		return PatchSafetyCheck{Kind: SafetyAskUser}
	}
	rejectsSandboxApproval := policy.Mode == ApprovalNever || (policy.Mode == ApprovalGranular && !policy.SandboxApproval)
	if WritePatchConstrainedToWritablePathsInContext(action, &filePolicy, context) {
		if permissionKind == PermissionDisabled || permissionKind == PermissionExternal {
			return PatchSafetyCheck{Kind: SafetyAutoApprove, SandboxType: "none"}
		}
		if platformSandbox != "" {
			return PatchSafetyCheck{Kind: SafetyAutoApprove, SandboxType: platformSandbox}
		}
		if rejectsSandboxApproval {
			return PatchSafetyCheck{Kind: SafetyReject, Reason: patchRejectionReasonInContext(permissionKind, &filePolicy, context)}
		}
		return PatchSafetyCheck{Kind: SafetyAskUser}
	}
	if rejectsSandboxApproval {
		return PatchSafetyCheck{Kind: SafetyReject, Reason: patchRejectionReasonInContext(permissionKind, &filePolicy, context)}
	}
	return PatchSafetyCheck{Kind: SafetyAskUser}
}

func patchRejectionReasonInContext(permissionKind PermissionProfileKind, policy *FileSystemPolicy, context FileSystemPolicyContext) string {
	if permissionKind == PermissionManaged && policy != nil && !policy.FullDiskWrite && !context.HasConfiguredWritableRoots(policy) {
		return PatchRejectedReadOnlyReason
	}
	return PatchRejectedOutsideProjectReason
}

// lexicalRelativeNativePath returns the path of target below root using the
// shared path convention, without touching the filesystem.
func lexicalRelativeNativePath(root, target *utils.PathURI) (string, bool) {
	if root == nil || target == nil {
		return "", false
	}
	convention, ok := target.InferConvention()
	if !ok {
		return "", false
	}
	rootSegments := nonEmptyNativeSegments(convention, root.NativePathString())
	targetSegments := nonEmptyNativeSegments(convention, target.NativePathString())
	if len(rootSegments) > len(targetSegments) {
		return "", false
	}
	for index := range rootSegments {
		if convention == utils.ConventionWindows {
			if !strings.EqualFold(rootSegments[index], targetSegments[index]) {
				return "", false
			}
			continue
		}
		if rootSegments[index] != targetSegments[index] {
			return "", false
		}
	}
	return strings.Join(targetSegments[len(rootSegments):], "/"), true
}

func nonEmptyNativeSegments(convention utils.PathConvention, native string) []string {
	segments := utils.PathSegments(convention, native)
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" || segment == "." {
			continue
		}
		out = append(out, segment)
	}
	return out
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
