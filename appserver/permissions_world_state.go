package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/features"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// This file ports the `permissions` world-state section from Rust
// core/src/context/world_state/permissions.rs: the section snapshots a hash of
// the permission instructions (built without the approved command prefixes) and
// the approved prefix set, and re-emits the fragment whenever the instructions
// change.

const permissionsInstructionsKind = "permissions_instructions"

// permissionsWorldStateSnapshot mirrors Rust PermissionsSnapshot::Current.
type permissionsWorldStateSnapshot struct {
	Instructions            string     `json:"instructions"`
	ApprovedCommandPrefixes [][]string `json:"approved_command_prefixes,omitempty"`
}

// permissionsInstructionsForTurn renders the permissions instructions fragment
// for a turn, or an empty string when the section is disabled or nothing can be
// resolved.
func (r *RuntimeRouter) permissionsInstructionsForTurn(
	params *turn.TurnStartParams,
	cfg *config.Config,
	options sandbox.PermissionPromptProfileOptions,
) (string, string) {
	cwd := turnCWD(params)
	profile := permissionProfileForTurn(cfg, params, cwd)
	body := sandbox.BuildPermissionPromptForProfile(profile, cwd, options)
	rendered := sandbox.RenderPermissionInstructions(body)
	// The section hash excludes the approved command prefixes (Rust
	// PermissionsState builds the hash from instructions with an empty policy).
	hashOptions := options
	hashOptions.ApprovedCommandPrefixes = nil
	hashBody := sandbox.BuildPermissionPromptForProfile(profile, cwd, hashOptions)
	hash := sandbox.WorldStateFragmentHash(roleDeveloper(), sandbox.RenderPermissionInstructions(hashBody))
	return rendered, hash
}

func roleDeveloper() string {
	return contextfrag.RoleDeveloper
}

// permissionProfileForTurn resolves the turn's effective permission profile.
func permissionProfileForTurn(cfg *config.Config, params *turn.TurnStartParams, cwd string) *sandbox.PermissionProfile {
	if resolution, err := turnSandboxPermissionProfile(cfg, cwd, params); err == nil && resolution != nil {
		return resolution.Profile
	}
	return nil
}

// permissionPromptOptionsForTurn ports the turn-scoped inputs Rust's
// PermissionsState passes to PermissionsInstructions::from_permission_profile.
func (r *RuntimeRouter) permissionPromptOptionsForTurn(
	params *turn.TurnStartParams,
	cfg *config.Config,
) sandbox.PermissionPromptProfileOptions {
	approvalPolicy := turnApprovalPolicyForTurn(cfg, params)
	options := sandbox.PermissionPromptProfileOptions{
		ApprovalPolicy:                 approvalPolicy,
		ApprovalsReviewer:              turnApprovalsReviewerForTurn(cfg, params),
		ExecPermissionApprovalsEnabled: features.Enabled(cfg.FeatureSettings(), "exec_permission_approvals"),
		RequestPermissionsToolEnabled:  features.Enabled(cfg.FeatureSettings(), "request_permissions_tool"),
	}
	if approvalPolicy == sandbox.ApprovalGranular {
		options.Granular = granularApprovalConfigForTurn(cfg, params)
	}
	// Approved command prefixes come from the thread's exec policy plus the
	// prefixes saved during this thread; the exec-policy accessor lands with the
	// prefix-diff stage, so the section stays empty for now and the approval
	// path keeps reporting saved prefixes through execPolicyPostToolInputItems.
	return options
}

// granularApprovalConfigForTurn reads the granular approval categories from the
// config or turn overrides (Rust GranularApprovalConfig).
func granularApprovalConfigForTurn(cfg *config.Config, params *turn.TurnStartParams) *sandbox.GranularApprovalConfig {
	raw := any(nil)
	if cfg != nil && cfg.Values != nil {
		raw = cfg.Values["approval_policy"]
	}
	if params != nil && params.ApprovalPolicy != nil {
		raw = params.ApprovalPolicy
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if nested, ok := values["granular"].(map[string]any); ok {
		values = nested
	}
	read := func(keys ...string) bool {
		for _, key := range keys {
			if allowed, ok := values[key].(bool); ok {
				return allowed
			}
		}
		return false
	}
	return &sandbox.GranularApprovalConfig{
		SandboxApproval:    read("sandbox_approval", "sandboxApproval"),
		Rules:              read("rules"),
		SkillApproval:      read("skill_approval", "skillApproval"),
		RequestPermissions: read("request_permissions", "requestPermissions"),
		MCPElicitations:    read("mcp_elicitations", "mcpElicitations"),
	}
}

// permissionsWorldStateInputItem ports Rust PermissionsState: it returns the
// developer fragment when the instructions hash changed (or the section was
// absent or unknown), and updates the persisted snapshot.
func (r *RuntimeRouter) permissionsWorldStateInputItem(
	threadID string,
	params *turn.TurnStartParams,
	cfg *config.Config,
) (any, error) {
	if cfg == nil || !cfg.IncludePermissionsInstructions() {
		// Rust sends CompactPermissionsState (approved prefixes only) here; Go
		// reports newly saved prefixes through execPolicyPostToolInputItems.
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	options := r.permissionPromptOptionsForTurn(params, cfg)
	rendered, hash := r.permissionsInstructionsForTurn(params, cfg, options)
	current := permissionsWorldStateSnapshot{
		Instructions:            hash,
		ApprovedCommandPrefixes: options.ApprovedCommandPrefixes,
	}

	previous, previousKind := decodePermissionsWorldStateSnapshot(state.PermissionInstructions)
	emit := true
	switch previousKind {
	case "current":
		emit = previous.Instructions != current.Instructions
	case "legacy":
		// Older Go builds persisted a bare instructions hash.
		emit = previous.Instructions != current.Instructions
	case "unknown":
		// Unknown retained state stays visible for this turn, matching Rust.
		emit = true
	}

	snapshot, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	if !sameJSONValue(state.PermissionInstructions, snapshot) {
		state.PermissionInstructions = snapshot
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
	}
	if !emit || strings.TrimSpace(rendered) == "" {
		return nil, nil
	}
	return modelInputTextMessage(contextfrag.RoleDeveloper, rendered), nil
}

func decodePermissionsWorldStateSnapshot(raw json.RawMessage) (permissionsWorldStateSnapshot, string) {
	if len(raw) == 0 {
		return permissionsWorldStateSnapshot{}, "absent"
	}
	var current permissionsWorldStateSnapshot
	if err := json.Unmarshal(raw, &current); err == nil && strings.TrimSpace(current.Instructions) != "" {
		return current, "current"
	}
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err == nil && strings.TrimSpace(legacy) != "" {
		return permissionsWorldStateSnapshot{Instructions: strings.TrimSpace(legacy)}, "legacy"
	}
	return permissionsWorldStateSnapshot{}, "unknown"
}

// permissionsWorldStateSessionItemForTurn turns an emitted fragment into the
// session item that keeps it in the thread history.
func permissionsWorldStateSessionItemForTurn(turnID string, input any, createdAt time.Time) (session.Item, bool) {
	raw, ok := input.(map[string]any)
	if !ok || strings.TrimSpace(stringFromAny(raw["type"])) != "message" {
		return session.Item{}, false
	}
	role := strings.TrimSpace(stringFromAny(raw["role"]))
	text := strings.TrimSpace(textFromInputItemContent(raw["content"]))
	if role != contextfrag.RoleDeveloper ||
		!strings.Contains(text, sandbox.PermissionInstructionsOpenTag) ||
		!strings.Contains(text, sandbox.PermissionInstructionsCloseTag) {
		return session.Item{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return session.Item{}, false
	}
	metadata := appTurnMetadata(turnID, map[string]any{
		"kind":             permissionsInstructionsKind,
		"hiddenFromThread": true,
	})
	return session.Item{
		ID:        "permissions-instructions-" + safeIdentifier(turnID),
		Type:      "message",
		Role:      role,
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":             permissionsInstructionsKind,
			"hiddenFromThread": true,
		},
		Metadata: metadata,
		Raw:      encoded,
	}, true
}
