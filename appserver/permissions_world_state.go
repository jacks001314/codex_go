package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/execpolicy"
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
	threadID string,
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
	options.ApprovedCommandPrefixes = r.approvedCommandPrefixesForThread(threadID)
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
	if cfg == nil {
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
	if !cfg.IncludePermissionsInstructions() {
		// Rust sends CompactPermissionsState (section id
		// `approved_command_prefixes`) in this mode: only newly approved
		// prefixes are reported.
		return r.compactPermissionsWorldStateUpdate(record, state, threadID)
	}
	options := r.permissionPromptOptionsForTurn(threadID, params, cfg)
	rendered, hash := r.permissionsInstructionsForTurn(params, cfg, options)
	current := permissionsWorldStateSnapshot{
		Instructions:            hash,
		ApprovedCommandPrefixes: options.ApprovedCommandPrefixes,
	}

	previous, previousKind := decodePermissionsWorldStateSnapshot(state.PermissionInstructions)
	emitFull := true
	savedPrefixes := ""
	switch previousKind {
	case "current":
		if previous.Instructions == current.Instructions {
			emitFull = false
			added := addedCommandPrefixes(current.ApprovedCommandPrefixes, previous.ApprovedCommandPrefixes)
			switch {
			case len(added) == 0:
				// Nothing changed.
			case prefixSubset(previous.ApprovedCommandPrefixes, current.ApprovedCommandPrefixes):
				savedPrefixes = approvedCommandPrefixSavedText(added)
			default:
				// A removed prefix re-renders the full instructions, matching
				// Rust's fallthrough.
				emitFull = true
			}
		}
	case "legacy":
		// Older Go builds persisted a bare instructions hash.
		emitFull = previous.Instructions != current.Instructions
	case "unknown":
		// Unknown retained state stays visible for this turn, matching Rust.
		emitFull = true
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
	if savedPrefixes != "" {
		return modelInputTextMessage(contextfrag.RoleDeveloper, savedPrefixes), nil
	}
	if !emitFull || strings.TrimSpace(rendered) == "" {
		return nil, nil
	}
	return modelInputTextMessage(contextfrag.RoleDeveloper, rendered), nil
}

// compactPermissionsWorldStateUpdate ports Rust CompactPermissionsState: the
// persisted snapshot is the approved prefix set, an unknown/absent previous
// state emits nothing, and the newly added prefixes are reported.
func (r *RuntimeRouter) compactPermissionsWorldStateUpdate(
	record *session.Record,
	state *session.WorldState,
	threadID string,
) (any, error) {
	current := r.approvedCommandPrefixesForThread(threadID)
	previous, previousKind := decodeApprovedCommandPrefixes(state.ApprovedCommandPrefixes)
	emit := ""
	if previousKind == "current" {
		emit = approvedCommandPrefixSavedText(addedCommandPrefixes(current, previous))
	}
	if current == nil {
		// Persist an empty set rather than null so the section reads as a set.
		current = [][]string{}
	}
	snapshot, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	if !sameJSONValue(state.ApprovedCommandPrefixes, snapshot) {
		state.ApprovedCommandPrefixes = snapshot
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
	}
	if emit == "" {
		return nil, nil
	}
	return modelInputTextMessage(contextfrag.RoleDeveloper, emit), nil
}

// decodeApprovedCommandPrefixes parses the compact section snapshot.
func decodeApprovedCommandPrefixes(raw json.RawMessage) ([][]string, string) {
	if len(raw) == 0 {
		return nil, "absent"
	}
	var prefixes [][]string
	if err := json.Unmarshal(raw, &prefixes); err != nil {
		return nil, "unknown"
	}
	return execpolicy.CanonicalCommandPrefixes(prefixes), "current"
}

// addedCommandPrefixes returns the canonical prefixes in current that are not in
// previous.
func addedCommandPrefixes(current [][]string, previous [][]string) [][]string {
	if len(current) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, prefix := range previous {
		seen[commandPrefixKey(prefix)] = true
	}
	out := make([][]string, 0, len(current))
	for _, prefix := range current {
		if seen[commandPrefixKey(prefix)] {
			continue
		}
		out = append(out, append([]string(nil), prefix...))
	}
	return execpolicy.CanonicalCommandPrefixes(out)
}

func prefixSubset(subset [][]string, superset [][]string) bool {
	if len(subset) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, prefix := range superset {
		seen[commandPrefixKey(prefix)] = true
	}
	for _, prefix := range subset {
		if !seen[commandPrefixKey(prefix)] {
			return false
		}
	}
	return true
}

func commandPrefixKey(prefix []string) string {
	return strings.Join(prefix, "\x00")
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
