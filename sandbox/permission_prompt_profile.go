package sandbox

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// Permission-instructions fragment markers (Rust
// PermissionsInstructions::type_markers).
const (
	PermissionInstructionsOpenTag  = "<permissions instructions>"
	PermissionInstructionsCloseTag = "</permissions instructions>"
)

// RenderPermissionInstructions wraps a prompt body in Rust's fragment markers.
func RenderPermissionInstructions(body string) string {
	return PermissionInstructionsOpenTag + body + PermissionInstructionsCloseTag
}

// worldStateFragmentDomain mirrors Rust's WorldStateHash domain separator.
const worldStateFragmentDomain = "codex-world-state-fragment-v1\x00"

// WorldStateFragmentHash ports Rust WorldStateHash::from_fragment: SHA-1 over
// the domain separator plus the fragment role and rendered text, each written
// length-prefixed after CRLF normalization.
func WorldStateFragmentHash(role string, rendered string) string {
	hasher := sha1.New()
	_, _ = hasher.Write([]byte(worldStateFragmentDomain))
	writeWorldStateHashComponent(hasher, role)
	writeWorldStateHashComponent(hasher, rendered)
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeWorldStateHashComponent(hasher interface{ Write([]byte) (int, error) }, value string) {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(normalized)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(normalized))
}

// PermissionPromptProfileOptions carries the turn-scoped inputs Rust's
// PermissionsState hands to
// PermissionsInstructions::from_permission_profile.
type PermissionPromptProfileOptions struct {
	ApprovalPolicy                 AskForApproval
	ApprovalsReviewer              string
	Granular                       *GranularApprovalConfig
	ApprovedCommandPrefixes        [][]string
	ExecPermissionApprovalsEnabled bool
	RequestPermissionsToolEnabled  bool
	// PermissionMessages / ApprovalMessages carry the model catalog's
	// per-mode and per-policy text overrides (Rust PermissionMessages /
	// ApprovalMessages); nil falls back to the built-in templates.
	PermissionMessages *PermissionPromptPermissionMessages
	ApprovalMessages   *PermissionPromptApprovalMessages
}

// BuildPermissionPromptForProfile ports Rust
// PermissionsInstructions::from_permission_profile: the sandbox mode, network
// access, writable roots and denied reads come from the resolved permission
// profile, and the approval sections come from the turn's approval policy.
func BuildPermissionPromptForProfile(
	profile *PermissionProfile,
	cwd string,
	options PermissionPromptProfileOptions,
) string {
	mode, writableRoots := permissionPromptSandboxModeForProfile(profile, cwd)
	deniedPaths, deniedGlobs := permissionPromptDeniedReads(profile, cwd)
	config := PermissionPromptConfig{
		SandboxMode:                    mode,
		NetworkAccess:                  permissionPromptNetworkAccess(profile),
		ApprovalPolicy:                 permissionPromptApprovalPolicy(options.ApprovalPolicy),
		ApprovalsReviewer:              permissionPromptReviewer(options.ApprovalsReviewer),
		WritableRoots:                  writableRoots,
		DeniedReadPaths:                deniedPaths,
		DeniedReadGlobs:                deniedGlobs,
		ApprovedCommandPrefixes:        options.ApprovedCommandPrefixes,
		ExecPermissionApprovalsEnabled: options.ExecPermissionApprovalsEnabled,
		RequestPermissionsToolEnabled:  options.RequestPermissionsToolEnabled,
		PermissionMessages:             options.PermissionMessages,
		ApprovalMessages:               options.ApprovalMessages,
	}
	// Rust granular_instructions derives the category lists in this order,
	// including request_permissions only when the tool is available.
	config.GranularAllowedCategories, config.GranularAutomaticallyRejected = granularPromptCategories(
		options.Granular,
		options.RequestPermissionsToolEnabled,
	)
	return BuildPermissionPrompt(&config)
}

// permissionPromptSandboxModeForProfile ports Rust sandbox_prompt_from_policy.
func permissionPromptSandboxModeForProfile(profile *PermissionProfile, cwd string) (PermissionPromptSandboxMode, []string) {
	if profile == nil {
		return PermissionPromptWorkspaceWrite, nil
	}
	policy := profile.LegacySandboxPolicy()
	if profile.Disabled || (policy != nil && policy.HasFullDiskWriteAccess()) {
		return PermissionPromptDangerFullAccess, nil
	}
	if policy == nil {
		return PermissionPromptReadOnly, nil
	}
	roots := policy.GetWritableRootsWithCWD(cwd)
	if len(roots) == 0 {
		return PermissionPromptReadOnly, nil
	}
	writable := make([]string, 0, len(roots))
	for _, root := range roots {
		if path := strings.TrimSpace(root.Root); path != "" {
			writable = append(writable, path)
		}
	}
	if len(writable) == 0 {
		return PermissionPromptReadOnly, nil
	}
	return PermissionPromptWorkspaceWrite, writable
}

func permissionPromptNetworkAccess(profile *PermissionProfile) PermissionPromptNetworkAccess {
	if profile == nil || profile.AllowsNetwork() {
		return PermissionPromptNetworkEnabled
	}
	return PermissionPromptNetworkRestricted
}

// permissionPromptDeniedReads ports Rust denied_reads_text's inputs: the
// profile's deny entries, split into paths and globs and resolved against cwd.
func permissionPromptDeniedReads(profile *PermissionProfile, cwd string) ([]string, []string) {
	if profile == nil {
		return nil, nil
	}
	var paths, globs []string
	for _, entry := range profile.DeniedReadEntries {
		if entry.Access != FileSystemAccessDeny {
			continue
		}
		switch entry.Path.Type {
		case "glob_pattern":
			if pattern := strings.TrimSpace(entry.Path.Pattern); pattern != "" {
				globs = append(globs, resolvePermissionPromptPath(pattern, cwd))
			}
		default:
			if path := strings.TrimSpace(entry.Path.Path); path != "" {
				paths = append(paths, resolvePermissionPromptPath(path, cwd))
			}
		}
	}
	return paths, globs
}

func resolvePermissionPromptPath(path string, cwd string) string {
	if filepath.IsAbs(path) || strings.TrimSpace(cwd) == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

func permissionPromptApprovalPolicy(policy AskForApproval) PermissionPromptApprovalPolicy {
	switch policy {
	case ApprovalNever:
		return PermissionPromptApprovalNever
	case ApprovalUnlessTrusted:
		return PermissionPromptApprovalUnlessTrusted
	case ApprovalGranular:
		return PermissionPromptApprovalGranular
	default:
		return PermissionPromptApprovalOnRequest
	}
}

func permissionPromptReviewer(reviewer string) PermissionPromptReviewer {
	if strings.TrimSpace(reviewer) == "auto_review" {
		return PermissionPromptAutoReview
	}
	return ""
}

// granularPromptCategories ports Rust granular_instructions' category lists.
func granularPromptCategories(config *GranularApprovalConfig, requestPermissionsToolEnabled bool) ([]string, []string) {
	type category struct {
		allowed bool
		label   string
	}
	categories := []category{
		{config.AllowsSandboxApproval(), "sandbox_approval"},
		{config.AllowsRulesApproval(), "rules"},
		{config.AllowsSkillApproval(), "skill_approval"},
	}
	if requestPermissionsToolEnabled {
		categories = append(categories, category{config.AllowsRequestPermissions(), "request_permissions"})
	}
	categories = append(categories, category{config.AllowsMCPElicitations(), "mcp_elicitations"})
	var allowed, rejected []string
	for _, entry := range categories {
		if entry.allowed {
			allowed = append(allowed, entry.label)
			continue
		}
		rejected = append(rejected, entry.label)
	}
	return allowed, rejected
}
