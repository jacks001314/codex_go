package sandbox

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type PermissionPromptSandboxMode string

const (
	PermissionPromptDangerFullAccess PermissionPromptSandboxMode = "danger-full-access"
	PermissionPromptWorkspaceWrite   PermissionPromptSandboxMode = "workspace-write"
	PermissionPromptReadOnly         PermissionPromptSandboxMode = "read-only"
)

type PermissionPromptNetworkAccess string

const (
	PermissionPromptNetworkEnabled    PermissionPromptNetworkAccess = "enabled"
	PermissionPromptNetworkRestricted PermissionPromptNetworkAccess = "restricted"
)

type PermissionPromptApprovalPolicy string

const (
	PermissionPromptApprovalNever         PermissionPromptApprovalPolicy = "never"
	PermissionPromptApprovalUnlessTrusted PermissionPromptApprovalPolicy = "unless_trusted"
	PermissionPromptApprovalOnRequest     PermissionPromptApprovalPolicy = "on_request"
	PermissionPromptApprovalGranular      PermissionPromptApprovalPolicy = "granular"
)

type PermissionPromptReviewer string

const PermissionPromptAutoReview PermissionPromptReviewer = "auto_review"

// permissionPromptNetworkPlaceholder mirrors Rust NETWORK_ACCESS_PLACEHOLDER.
const permissionPromptNetworkPlaceholder = "{{ network_access }}"

type PermissionPromptConfig struct {
	SandboxMode                    PermissionPromptSandboxMode
	NetworkAccess                  PermissionPromptNetworkAccess
	ApprovalPolicy                 PermissionPromptApprovalPolicy
	ApprovalsReviewer              PermissionPromptReviewer
	WritableRoots                  []string
	DeniedReadPaths                []string
	DeniedReadGlobs                []string
	ApprovedCommandPrefixes        [][]string
	ExecPermissionApprovalsEnabled bool
	RequestPermissionsToolEnabled  bool
	GranularAllowedCategories      []string
	GranularAutomaticallyRejected  []string
	// PermissionMessages carries the model catalog's sandbox-mode overrides
	// (Rust PermissionMessages); a missing mode falls back to the built-in
	// template and an empty override drops the sandbox section entirely.
	PermissionMessages *PermissionPromptPermissionMessages
	// ApprovalMessages carries the model catalog's approval-policy overrides
	// (Rust ApprovalMessages); a missing policy falls back to the built-in
	// templates.
	ApprovalMessages *PermissionPromptApprovalMessages
}

// PermissionPromptPermissionMessages mirrors Rust
// codex_protocol::openai_models::PermissionMessages.
type PermissionPromptPermissionMessages struct {
	DangerFullAccess *string
	WorkspaceWrite   *string
	ReadOnly         *string
	ApprovedPrefixes *string
	// WritableRoot/WritableRoots replace the bundled writable-root sentence
	// when it has exactly one `{{ roots }}` placeholder (Rust #56309).
	WritableRoot  *string
	WritableRoots *string
	// OmittedPaths adds catalog presentation ahead of the harness-owned
	// omission warning (Rust #57263).
	OmittedPaths *string
	// DeniedReads wraps the harness-owned denied-reads facts with exactly one
	// `{{ denied_reads }}` placeholder (Rust #57263).
	DeniedReads *string
}

// PermissionPromptApprovalMessages mirrors Rust
// codex_protocol::openai_models::ApprovalMessages.
type PermissionPromptApprovalMessages struct {
	OnRequest           *string
	OnRequestAutoReview *string
	Never               *string
	UnlessTrusted       *string
	// Granular carries the catalog's granular-approval templates (Rust
	// #57325). Templates are preserved verbatim; consumers validate size and
	// markers before rendering.
	Granular *PermissionPromptGranularApprovalMessages
}

// PermissionPromptGranularApprovalMessages preserves the catalog's optional
// granular-approval presentation templates.
type PermissionPromptGranularApprovalMessages struct {
	Intro              *string
	PromptedCategories *string
	RejectedCategories *string
}

// BuildPermissionPrompt ports Rust prompts::PermissionsInstructions: sections
// are appended with a single newline separator and the body ends with a
// newline, so the rendered fragment reads
// `<permissions instructions>\n<body></permissions instructions>`.
func BuildPermissionPrompt(config *PermissionPromptConfig) string {
	if config == nil {
		config = &PermissionPromptConfig{}
	}
	writableRoots, deniedReadPaths, deniedReadGlobs, pathsOmitted := boundPermissionPromptPaths(
		config.WritableRoots,
		config.DeniedReadPaths,
		config.DeniedReadGlobs,
	)
	text := ""
	if sandbox := sandboxPromptText(config.SandboxMode, config.NetworkAccess, config.PermissionMessages); sandbox != "" {
		text = appendPermissionSection(text, sandbox)
	}
	text = appendPermissionSection(text, approvalPromptText(config))
	if roots := writableRootsText(writableRoots, config.PermissionMessages); roots != "" {
		text = appendPermissionSection(text, roots)
	}
	if denied := deniedReadsText(deniedReadPaths, deniedReadGlobs, config.PermissionMessages); denied != "" {
		text = appendPermissionSection(text, denied)
	}
	if pathsOmitted {
		text = appendPermissionSection(text, omittedPathsText(config.PermissionMessages))
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

const (
	// maxPermissionPromptPathBytes bounds the displayed writable roots and
	// deny entries together (Rust MAX_PERMISSION_PATH_BYTES, #47867).
	maxPermissionPromptPathBytes = 32 * 1024
	// permissionPromptOmittedPathsText is Rust's OMITTED_PERMISSION_PATHS.
	permissionPromptOmittedPathsText = "Additional permission paths/globs are omitted. All restrictions still apply; do not use escalation or additional permissions to bypass omitted read denials."
	// permissionPromptDeniedReadsPolicy is the harness-owned denial explanation
	// Rust always renders ahead of the path/glob list.
	permissionPromptDeniedReadsPolicy      = "The active permission profile denies reading these paths/globs. Do not request escalation or additional permissions to read them; these denials are policy restrictions."
	permissionPromptDeniedReadsTemplate    = "## Denied filesystem reads\n{{ denied_reads }}"
	permissionPromptDeniedReadsPlaceholder = "{{ denied_reads }}"
	permissionPromptWritableRootTemplate   = " The writable root is {{ roots }}."
	permissionPromptWritableRootsTemplate  = " The writable roots are {{ roots }}."
	permissionPromptRootsPlaceholder       = "{{ roots }}"
	// maxPermissionPresentationBytes bounds each catalog presentation override
	// (Rust #57263): omitted_paths and denied_reads contribute at most 1,000
	// bytes together.
	maxPermissionPresentationBytes = 500
	// maxWritableRootsTemplateBytes bounds the catalog writable-root sentence;
	// replacing the 11-byte marker leaves at most 989 bytes of wording.
	maxWritableRootsTemplateBytes = 1000
)

// boundPermissionPromptPaths applies the shared display budget, omitting whole
// entries only. Each entry reserves ten bytes for its label, quoting and
// separator (Rust #47867).
func boundPermissionPromptPaths(writableRoots []string, deniedReadPaths []string, deniedReadGlobs []string) ([]string, []string, []string, bool) {
	remaining := maxPermissionPromptPathBytes - len(permissionPromptOmittedPathsText) - 1
	omitted := false
	take := func(values []string) []string {
		bounded := make([]string, 0, len(values))
		for _, value := range values {
			if remaining >= len(value)+10 {
				remaining -= len(value) + 10
				bounded = append(bounded, value)
				continue
			}
			omitted = true
		}
		return bounded
	}
	return take(writableRoots), take(deniedReadPaths), take(deniedReadGlobs), omitted
}

// appendPermissionSection ports Rust append_section: a newline is inserted
// before the section unless the text already ends with one.
func appendPermissionSection(text string, section string) string {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + section
}

// sandboxPromptText ports Rust sandbox_text: a catalog override is used when
// present (with only the exact `{{ network_access }}` placeholder replaced),
// otherwise the built-in template applies.
func sandboxPromptText(
	mode PermissionPromptSandboxMode,
	network PermissionPromptNetworkAccess,
	messages *PermissionPromptPermissionMessages,
) string {
	// Go safety defaults: Rust callers always pass resolved enum values, but an
	// unset field would otherwise render as an empty mode or network access.
	if mode == "" {
		mode = PermissionPromptWorkspaceWrite
	}
	if network == "" {
		network = PermissionPromptNetworkRestricted
	}
	if override := permissionMessageForMode(messages, mode); override != nil {
		if *override == "" {
			return ""
		}
		return strings.ReplaceAll(*override, permissionPromptNetworkPlaceholder, string(network))
	}
	template := permissionPromptSandboxTemplate(mode)
	return strings.ReplaceAll(template, permissionPromptNetworkPlaceholder, string(network))
}

func permissionMessageForMode(messages *PermissionPromptPermissionMessages, mode PermissionPromptSandboxMode) *string {
	if messages == nil {
		return nil
	}
	switch mode {
	case PermissionPromptDangerFullAccess:
		return messages.DangerFullAccess
	case PermissionPromptReadOnly:
		return messages.ReadOnly
	default:
		return messages.WorkspaceWrite
	}
}

func permissionPromptSandboxTemplate(mode PermissionPromptSandboxMode) string {
	switch mode {
	case PermissionPromptDangerFullAccess:
		return permissionPromptSandboxDangerFullAccess
	case PermissionPromptReadOnly:
		return permissionPromptSandboxReadOnly
	default:
		return permissionPromptSandboxWorkspaceWrite
	}
}

// approvalPromptText ports Rust approval_text.
func approvalPromptText(config *PermissionPromptConfig) string {
	if selected := approvalMessageForPolicy(config); selected != nil {
		// A catalog approval message replaces the bundled approval prose, but
		// an explicitly supplied approved-prefix template still renders for
		// on-request approvals (Rust #55366).
		if config.ApprovalPolicy == PermissionPromptApprovalOnRequest && approvedPrefixTemplate(config) != nil {
			if prefixes := approvedPrefixesText(config.ApprovedCommandPrefixes, approvedPrefixTemplate(config)); prefixes != "" {
				if strings.TrimSpace(*selected) == "" {
					return prefixes
				}
				return strings.TrimSuffix(*selected, "\n") + "\n\n" + prefixes
			}
		}
		return *selected
	}
	var text string
	switch config.ApprovalPolicy {
	case PermissionPromptApprovalNever:
		text = permissionPromptApprovalNever
	case PermissionPromptApprovalUnlessTrusted:
		text = withRequestPermissions(permissionPromptApprovalUnlessTrusted, config.RequestPermissionsToolEnabled)
	case PermissionPromptApprovalGranular:
		text = granularText(config)
	default:
		onRequestRule := permissionPromptApprovalOnRequest
		if config.ExecPermissionApprovalsEnabled {
			onRequestRule = permissionPromptApprovalOnRequestRuleRequestPermission
		}
		sections := []string{strings.TrimSuffix(onRequestRule, "\n")}
		if config.RequestPermissionsToolEnabled {
			sections = append(sections, requestPermissionsToolText())
		}
		if prefixes := approvedPrefixesText(config.ApprovedCommandPrefixes, approvedPrefixTemplate(config)); prefixes != "" {
			sections = append(sections, prefixes)
		}
		text = strings.Join(sections, "\n\n")
	}
	if config.ApprovalsReviewer == PermissionPromptAutoReview && config.ApprovalPolicy != PermissionPromptApprovalNever {
		text = strings.TrimSuffix(text, "\n") + "\n\n" + autoReviewText()
	}
	return text
}

// approvalMessageForPolicy ports Rust's catalog approval-message selection: a
// catalog message wins when present, and Granular has no catalog variant.
func approvalMessageForPolicy(config *PermissionPromptConfig) *string {
	messages := config.ApprovalMessages
	if messages == nil {
		return nil
	}
	switch config.ApprovalPolicy {
	case PermissionPromptApprovalOnRequest:
		if config.ApprovalsReviewer == PermissionPromptAutoReview {
			return messages.OnRequestAutoReview
		}
		return messages.OnRequest
	case PermissionPromptApprovalNever:
		return messages.Never
	case PermissionPromptApprovalUnlessTrusted:
		return messages.UnlessTrusted
	default:
		return nil
	}
}

func granularText(config *PermissionPromptConfig) string {
	sections := []string{"# Approval Requests\n\nApproval policy is `granular`. Categories set to `false` are automatically rejected instead of prompting the user."}
	if len(config.GranularAllowedCategories) > 0 {
		sections = append(sections, "These approval categories may still prompt the user when needed:\n"+bulletList(config.GranularAllowedCategories))
	}
	if len(config.GranularAutomaticallyRejected) > 0 {
		sections = append(sections, "These approval categories are automatically rejected instead of prompting the user:\n"+bulletList(config.GranularAutomaticallyRejected))
	}
	if config.ExecPermissionApprovalsEnabled && granularCategoryAllowed(config.GranularAllowedCategories, "sandbox_approval") {
		sections = append(sections, strings.TrimSuffix(permissionPromptApprovalOnRequestRuleRequestPermission, "\n"))
	}
	if config.RequestPermissionsToolEnabled {
		sections = append(sections, requestPermissionsToolText())
	}
	if prefixes := approvedPrefixesText(config.ApprovedCommandPrefixes, approvedPrefixTemplate(config)); prefixes != "" {
		sections = append(sections, prefixes)
	}
	return strings.Join(sections, "\n\n")
}

func granularCategoryAllowed(categories []string, category string) bool {
	for _, value := range categories {
		if strings.TrimSpace(value) == category {
			return true
		}
	}
	return false
}

func withRequestPermissions(text string, enabled bool) string {
	if !enabled {
		return text
	}
	return text + "\n\n" + requestPermissionsToolText()
}

func requestPermissionsToolText() string {
	return "# request_permissions Tool\n\nThe built-in `request_permissions` tool is available in this session. Invoke it when you need to request additional `network` or `file_system` permissions before later shell-like commands need them. Request only the specific permissions required for the task."
}

func autoReviewText() string {
	return "`approvals_reviewer` is `auto_review`: Sandbox escalations with require_escalated will be reviewed for compliance with the policy. If a rejection happens, you should proceed only with a materially safer alternative, or inform the user of the risk and send a final message to ask for approval."
}

func writableRootsText(roots []string, messages *PermissionPromptPermissionMessages) string {
	if len(roots) == 0 {
		return ""
	}
	roots = append([]string(nil), roots...)
	sort.Strings(roots)
	wrapped := make([]string, len(roots))
	for i, root := range roots {
		wrapped[i] = "`" + root + "`"
	}
	rootsList := strings.Join(wrapped, ", ")
	fallback := permissionPromptWritableRootsTemplate
	var override *string
	if len(wrapped) == 1 {
		fallback = permissionPromptWritableRootTemplate
	}
	if messages != nil {
		if len(wrapped) == 1 {
			override = messages.WritableRoot
		} else {
			override = messages.WritableRoots
		}
	}
	if override != nil {
		if rendered, ok := renderWritableRootsOverride(*override, rootsList); ok {
			return rendered
		}
	}
	return strings.ReplaceAll(fallback, permissionPromptRootsPlaceholder, rootsList)
}

// renderWritableRootsOverride validates one catalog writable-root sentence
// (Rust #56309). The caller has already bounded paths at whole-entry
// boundaries, so this only bounds presentation.
func renderWritableRootsOverride(template string, roots string) (string, bool) {
	if len(template) > maxWritableRootsTemplateBytes {
		return "", false
	}
	for _, value := range template {
		if unicode.IsControl(value) || value == '\u2028' || value == '\u2029' {
			return "", false
		}
	}
	index := strings.Index(template, permissionPromptRootsPlaceholder)
	if index < 0 {
		return "", false
	}
	before := template[:index]
	after := template[index+len(permissionPromptRootsPlaceholder):]
	if strings.ContainsAny(before+after, "{}") ||
		strings.Contains(template, PermissionInstructionsOpenTag) ||
		strings.Contains(template, PermissionInstructionsCloseTag) {
		return "", false
	}
	if len(before)+len(roots)+len(after) > maxPermissionPromptPathBytes+maxWritableRootsTemplateBytes {
		return "", false
	}
	return before + roots + after, true
}

func deniedReadsText(paths []string, globs []string, messages *PermissionPromptPermissionMessages) string {
	// Rust emits the denied roots first, then the denied globs.
	sortedPaths := append([]string(nil), paths...)
	sort.Strings(sortedPaths)
	sortedGlobs := append([]string(nil), globs...)
	sort.Strings(sortedGlobs)
	entries := make([]string, 0, len(sortedPaths)+len(sortedGlobs))
	for _, path := range sortedPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		entries = append(entries, "- path `"+path+"`")
	}
	for _, glob := range sortedGlobs {
		if strings.TrimSpace(glob) == "" {
			continue
		}
		entries = append(entries, "- glob `"+glob+"`")
	}
	if len(entries) == 0 {
		return ""
	}
	facts := permissionPromptDeniedReadsPolicy + "\n" + strings.Join(entries, "\n")
	var before, after string
	overrideApplied := false
	if messages != nil && messages.DeniedReads != nil && validPermissionPresentation(*messages.DeniedReads) {
		if templateBefore, templateAfter, ok := splitPermissionTemplate(*messages.DeniedReads, permissionPromptDeniedReadsPlaceholder); ok &&
			!strings.ContainsAny(templateBefore+templateAfter, "{}") &&
			len(templateBefore)+len(facts)+len(templateAfter) <= maxPermissionPromptPathBytes+len(permissionPromptDeniedReadsPolicy)+1+maxPermissionPresentationBytes {
			before, after = templateBefore, templateAfter
			overrideApplied = true
		}
	}
	if !overrideApplied {
		before, after, _ = splitPermissionTemplate(permissionPromptDeniedReadsTemplate, permissionPromptDeniedReadsPlaceholder)
	}
	return before + facts + after
}

// omittedPathsText renders the harness-owned omission warning, optionally
// preceded by bounded catalog presentation (Rust #57263).
func omittedPathsText(messages *PermissionPromptPermissionMessages) string {
	if messages != nil && messages.OmittedPaths != nil {
		override := *messages.OmittedPaths
		if validPermissionPresentation(override) && !strings.ContainsAny(override, "{}") {
			return override + "\n" + permissionPromptOmittedPathsText
		}
	}
	return permissionPromptOmittedPathsText
}

// validPermissionPresentation bounds catalog presentation text (Rust #57263):
// non-empty, at most 500 UTF-8 bytes, no control characters other than
// newlines, no Unicode line separators, and no permission-fragment markers.
func validPermissionPresentation(text string) bool {
	if strings.TrimSpace(text) == "" || len(text) > maxPermissionPresentationBytes {
		return false
	}
	if strings.Contains(text, PermissionInstructionsOpenTag) || strings.Contains(text, PermissionInstructionsCloseTag) {
		return false
	}
	for _, value := range text {
		if (unicode.IsControl(value) && value != '\n') || value == '\u2028' || value == '\u2029' {
			return false
		}
	}
	return true
}

func splitPermissionTemplate(template string, placeholder string) (string, string, bool) {
	index := strings.Index(template, placeholder)
	if index < 0 {
		return "", "", false
	}
	return template[:index], template[index+len(placeholder):], true
}

func approvedPrefixTemplate(config *PermissionPromptConfig) *string {
	if config == nil || config.PermissionMessages == nil {
		return nil
	}
	return config.PermissionMessages.ApprovedPrefixes
}

const bundledApprovedPrefixTemplate = "## Approved command prefixes\nThe following prefix rules have already been approved: {{ approved_prefixes }}"

func approvedPrefixesText(prefixes [][]string, template *string) string {
	copied := make([][]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if len(prefix) == 0 {
			continue
		}
		copied = append(copied, append([]string(nil), prefix...))
	}
	if len(copied) == 0 {
		return ""
	}
	// Rust uses protocol::models::format_allow_prefixes for the approved-prefix
	// section.
	rendered := FormatAllowPrefixes(copied)
	if rendered == "" {
		return ""
	}
	section := bundledApprovedPrefixTemplate
	if template != nil {
		section = *template
	}
	if section == "" {
		return ""
	}
	return strings.ReplaceAll(section, "{{ approved_prefixes }}", rendered)
}

const (
	maxRenderedPrefixes     = 100
	maxAllowPrefixTextChars = 5000
	truncatedPrefixesMarker = "...\n[Some commands were truncated]"
)

// FormatAllowPrefixes mirrors Rust protocol::models::format_allow_prefixes:
// approved command prefixes are sorted by token count, then total token bytes,
// then lexicographically, and rendered as a line-delimited list of
// "- [\"git\", \"pull\"]" entries capped at 100 prefixes and 5000 characters.
func FormatAllowPrefixes(prefixes [][]string) string {
	truncated := false
	if len(prefixes) > maxRenderedPrefixes {
		truncated = true
	}
	sort.SliceStable(prefixes, func(i int, j int) bool {
		left := prefixes[i]
		right := prefixes[j]
		if len(left) != len(right) {
			return len(left) < len(right)
		}
		leftLen := combinedPrefixTokenBytes(left)
		rightLen := combinedPrefixTokenBytes(right)
		if leftLen != rightLen {
			return leftLen < rightLen
		}
		return prefixTokensLess(left, right)
	})
	lines := make([]string, 0, len(prefixes))
	for index := 0; index < len(prefixes) && index < maxRenderedPrefixes; index++ {
		lines = append(lines, "- "+renderCommandPrefix(prefixes[index]))
	}
	output := strings.Join(lines, "\n")
	if byteIndex := nthRuneByteIndex(output, maxAllowPrefixTextChars); byteIndex >= 0 {
		truncated = true
		output = output[:byteIndex]
	}
	if truncated {
		return output + truncatedPrefixesMarker
	}
	return output
}

func combinedPrefixTokenBytes(prefix []string) int {
	total := 0
	for _, token := range prefix {
		total += len(token)
	}
	return total
}

func prefixTokensLess(left []string, right []string) bool {
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return false
}

func renderCommandPrefix(prefix []string) string {
	quoted := make([]string, 0, len(prefix))
	for _, token := range prefix {
		encoded, err := json.Marshal(token)
		if err != nil {
			quoted = append(quoted, fmt.Sprintf("%q", token))
			continue
		}
		quoted = append(quoted, string(encoded))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func nthRuneByteIndex(value string, nth int) int {
	if nth <= 0 || utf8.RuneCountInString(value) <= nth {
		return -1
	}
	index := 0
	for range value {
		if index == nth {
			return index
		}
		_, size := utf8.DecodeRuneInString(value[index:])
		index += size
	}
	return -1
}

func bulletList(values []string) string {
	// Rust preserves the category order it builds (sandbox_approval, rules,
	// skill_approval, request_permissions, mcp_elicitations).
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, "- `"+value+"`")
	}
	return strings.Join(lines, "\n")
}
