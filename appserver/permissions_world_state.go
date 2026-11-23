package appserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/execpolicy"
	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/model"
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

// permissionsScopedWorldStateSnapshot mirrors Rust
// PermissionsSnapshot::Scoped (#61494): one fingerprint per environment plus an
// optional labelled thread-default description.
type permissionsScopedWorldStateSnapshot struct {
	Environments            map[string]string `json:"environments"`
	ThreadDefaults          string            `json:"thread_defaults,omitempty"`
	ApprovedCommandPrefixes [][]string        `json:"approved_command_prefixes"`
}

// permissionDescription is one permission fragment and the comparison value
// Rust keeps in PermissionDescription: the instructions hash excludes saved
// command prefixes, which the section tracks separately.
type permissionDescription struct {
	instructions string
	fingerprint  string
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
	description := permissionDescriptionForProfile(profile, cwd, options)
	return sandbox.RenderPermissionInstructions(description.instructions), description.fingerprint
}

// permissionDescriptionForProfile ports Rust PermissionDescription::new: the
// rendered permissions body and the fingerprint of the same body built without
// the approved command prefixes.
func permissionDescriptionForProfile(
	profile *sandbox.PermissionProfile,
	cwd string,
	options sandbox.PermissionPromptProfileOptions,
) permissionDescription {
	body := sandbox.BuildPermissionPromptForProfile(profile, cwd, options)
	// The section hash excludes the approved command prefixes (Rust
	// PermissionsState builds the hash from instructions with an empty policy).
	hashOptions := options
	hashOptions.ApprovedCommandPrefixes = nil
	hashBody := sandbox.BuildPermissionPromptForProfile(profile, cwd, hashOptions)
	hash := sandbox.WorldStateFragmentHash(roleDeveloper(), sandbox.RenderPermissionInstructions(hashBody))
	return permissionDescription{instructions: body, fingerprint: hash}
}

// permissionDescriptionsForTurn builds the permission descriptions Rust's
// PermissionsState holds (#61494). With per_environment_context disabled the
// single thread-scoped description is keyed by the empty string, matching the
// unscoped snapshot. With it enabled every selected environment gets its own
// description keyed by environment id; when no environment is selected (or
// ready), the labelled thread defaults use the empty key.
func (r *RuntimeRouter) permissionDescriptionsForTurn(
	threadID string,
	params *turn.TurnStartParams,
	modelInfo *model.ModelInfo,
	cfg *config.Config,
	options sandbox.PermissionPromptProfileOptions,
) (map[string]permissionDescription, bool) {
	perEnvironment := cfg != nil && features.Enabled(cfg.FeatureSettings(), "per_environment_context")
	if !perEnvironment {
		rendered, hash := r.permissionsInstructionsForTurn(params, cfg, options)
		return map[string]permissionDescription{"": {instructions: rendered, fingerprint: hash}}, false
	}
	descriptions := map[string]permissionDescription{}
	selections := r.permissionEnvironmentSelections(params)
	for _, selection := range selections {
		environmentID := strings.TrimSpace(selectionEnvironmentID(selection))
		if environmentID == "" {
			continue
		}
		cwd := r.permissionEnvironmentCWD(params, selection)
		profile := r.permissionProfileForEnvironmentSelection(cfg, params, selection, cwd)
		environmentOptions := options
		environmentOptions.ExecutorContext = r.permissionPromptExecutorContextForSelection(params, selection)
		descriptions[environmentID] = permissionDescriptionForProfile(profile, cwd, environmentOptions)
	}
	if len(descriptions) == 0 {
		cwd := turnCWD(params)
		profile := permissionProfileForTurn(cfg, params, cwd)
		options.ExecutorContext = r.permissionPromptExecutorContextForTurn(params)
		descriptions[""] = permissionDescriptionForProfile(profile, cwd, options)
	}
	return descriptions, true
}

// permissionEnvironmentSelections returns the turn's captured environment
// selections, falling back to the provider default like Rust's turn snapshot.
func (r *RuntimeRouter) permissionEnvironmentSelections(params *turn.TurnStartParams) []map[string]any {
	if params != nil && len(params.Environments) > 0 {
		return params.Environments
	}
	if r == nil || r.services.Environment == nil {
		return nil
	}
	defaultID, ok := r.services.Environment.DefaultEnvironmentID()
	if !ok || strings.TrimSpace(defaultID) == "" {
		return nil
	}
	return []map[string]any{{"environmentId": defaultID}}
}

func (r *RuntimeRouter) permissionEnvironmentCWD(params *turn.TurnStartParams, selection map[string]any) string {
	cwd := strings.TrimSpace(firstNonEmpty(
		threadItemStringFromAnyMap(selection, "cwd"),
		threadItemStringFromAnyMap(selection, "CWD"),
	))
	if cwd != "" {
		return cwd
	}
	if params != nil && strings.TrimSpace(params.CWD) != "" {
		return strings.TrimSpace(params.CWD)
	}
	if r != nil && strings.TrimSpace(r.services.DefaultCWD) != "" {
		return strings.TrimSpace(r.services.DefaultCWD)
	}
	return "."
}

func permissionWorkspaceRootsForSelection(selection map[string]any, cwd string) []string {
	roots := stringSliceFromAny(firstNonNil(selection["workspaceRoots"], selection["workspace_roots"]))
	trimmed := make([]string, 0, len(roots)+1)
	for _, root := range roots {
		if root = strings.TrimSpace(root); root != "" {
			trimmed = append(trimmed, root)
		}
	}
	if len(trimmed) > 0 {
		return trimmed
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		return []string{cwd}
	}
	return nil
}

// permissionProfileForEnvironmentSelection mirrors Rust
// TurnEnvironment::permission_profile_with_workspace_roots: a ready attachment's
// own profile wins, otherwise the thread defaults are materialized with that
// environment's workspace roots.
func (r *RuntimeRouter) permissionProfileForEnvironmentSelection(
	cfg *config.Config,
	params *turn.TurnStartParams,
	selection map[string]any,
	cwd string,
) *sandbox.PermissionProfile {
	if profile, _ := environmentSelectionOwnPermissionProfile(selection); profile != nil {
		return profile
	}
	roots := permissionWorkspaceRootsForSelection(selection, cwd)
	if resolution, err := threadDefaultSandboxPermissionProfileWithRoots(cfg, cwd, params, roots); err == nil && resolution != nil && resolution.Profile != nil {
		return resolution.Profile
	}
	return permissionProfileForTurn(cfg, params, cwd)
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
	modelInfo *model.ModelInfo,
	cfg *config.Config,
) sandbox.PermissionPromptProfileOptions {
	approvalPolicy := turnApprovalPolicyForTurn(cfg, params)
	options := sandbox.PermissionPromptProfileOptions{
		ApprovalPolicy:                 approvalPolicy,
		ApprovalsReviewer:              turnApprovalsReviewerForTurn(cfg, params),
		ExecPermissionApprovalsEnabled: features.Enabled(cfg.FeatureSettings(), "exec_permission_approvals"),
		RequestPermissionsToolEnabled:  features.Enabled(cfg.FeatureSettings(), "request_permissions_tool"),
		ExecutorContext:                r.permissionPromptExecutorContextForTurn(params),
	}
	if approvalPolicy == sandbox.ApprovalGranular {
		options.Granular = granularApprovalConfigForTurn(cfg, params)
	}
	options.ApprovedCommandPrefixes = r.approvedCommandPrefixesForThread(threadID)
	if modelInfo != nil && modelInfo.ModelMessages != nil {
		// Rust ApprovalPromptContext / PermissionMessages: catalog texts replace
		// the built-in approval and sandbox sections.
		if messages := modelInfo.ModelMessages.Approvals; messages != nil {
			options.ApprovalMessages = &sandbox.PermissionPromptApprovalMessages{
				OnRequest:           messages.OnRequest,
				OnRequestAutoReview: messages.OnRequestAutoReview,
				Never:               messages.Never,
				UnlessTrusted:       messages.UnlessTrusted,
				// Rust #57581: catalog headings customize the request_permissions
				// section and the auto-review introduction.
				RequestPermissions: messages.RequestPermissions,
				AutoReviewSuffix:   messages.AutoReviewSuffix,
			}
			if granular := messages.Granular; granular != nil {
				options.ApprovalMessages.Granular = &sandbox.PermissionPromptGranularApprovalMessages{
					Intro:              granular.Intro,
					PromptedCategories: granular.PromptedCategories,
					RejectedCategories: granular.RejectedCategories,
				}
			}
		}
		if messages := modelInfo.ModelMessages.Permissions; messages != nil {
			options.PermissionMessages = &sandbox.PermissionPromptPermissionMessages{
				DangerFullAccess: messages.DangerFullAccess,
				WorkspaceWrite:   messages.WorkspaceWrite,
				ReadOnly:         messages.ReadOnly,
				ApprovedPrefixes: messages.ApprovedPrefixes,
				WritableRoot:     messages.WritableRoot,
				WritableRoots:    messages.WritableRoots,
				OmittedPaths:     messages.OmittedPaths,
				DeniedReads:      messages.DeniedReads,
				// Rust #57599: heading for the saved command-prefix notice.
				ApprovedCommandPrefixSaved: messages.ApprovedCommandPrefixSaved,
			}
		}
	}
	return options
}

// permissionPromptExecutorContextForTurn returns the primary selected
// environment's filesystem facts when it is an executor. Rust #47867 renders
// remote permission paths in that environment's native form; a nil context
// keeps local filesystem resolution. Cached info is used so building
// instructions never opens a new connection.
func (r *RuntimeRouter) permissionPromptExecutorContextForTurn(params *turn.TurnStartParams) *sandbox.PermissionPromptExecutorContext {
	if r == nil || r.services.Environment == nil || params == nil {
		return nil
	}
	selections := params.Environments
	if len(selections) == 0 {
		defaultID, ok := r.services.Environment.DefaultEnvironmentID()
		if !ok {
			return nil
		}
		selections = []map[string]any{{"environmentId": defaultID}}
	}
	return r.permissionPromptExecutorContextForSelection(params, selections[0])
}

// permissionPromptExecutorContextForSelection resolves one environment's
// executor facts for its permissions description (Rust #47867/#61494). A local
// or starting attachment has no executor context, so host resolution applies.
func (r *RuntimeRouter) permissionPromptExecutorContextForSelection(params *turn.TurnStartParams, selected map[string]any) *sandbox.PermissionPromptExecutorContext {
	if r == nil || r.services.Environment == nil || params == nil {
		return nil
	}
	environmentID := strings.TrimSpace(selectionEnvironmentID(selected))
	if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
		return nil
	}
	state, _ := resolveEnvironmentConfig(selected, r.threadEnvironmentConfigForTurn(params))
	if state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed {
		return nil
	}
	context := &sandbox.PermissionPromptExecutorContext{
		CWD: firstNonEmpty(
			threadItemStringFromAnyMap(selected, "cwd"),
			threadItemStringFromAnyMap(selected, "CWD"),
		),
	}
	if info, ok := r.services.Environment.CachedInfo(environmentID); ok && info != nil {
		context.CWD = firstNonEmpty(context.CWD, ptrStringValue(info.CWD))
		context.UserHomeDir = strings.TrimSpace(info.UserHomeDir)
		context.PlatformOS = strings.TrimSpace(info.PlatformOS)
	}
	return context
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

// savedPrefixHeadingForModel resolves the catalog heading for the standalone
// saved-prefix notice (Rust #57599). Invalid values fall back to the bundled
// marker.
func savedPrefixHeadingForModel(modelInfo *model.ModelInfo) string {
	if modelInfo == nil || modelInfo.ModelMessages == nil || modelInfo.ModelMessages.Permissions == nil {
		return ""
	}
	return sandbox.ValidSavedPrefixHeading(modelInfo.ModelMessages.Permissions.ApprovedCommandPrefixSaved)
}

// permissionsWorldStateInputItem ports Rust PermissionsState: it returns the
// developer fragment when the instructions hash changed (or the section was
// absent or unknown), and updates the persisted snapshot.
func (r *RuntimeRouter) permissionsWorldStateInputItem(
	threadID string,
	params *turn.TurnStartParams,
	modelInfo *model.ModelInfo,
	cfg *config.Config,
) (any, error) {
	items, err := r.permissionsWorldStateInputItems(threadID, params, modelInfo, cfg)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return items[0], nil
}

// permissionsWorldStateInputItems ports Rust PermissionsState: it returns the
// developer fragments for every changed scope and updates the persisted
// snapshot. The scoped format can emit several descriptions (and withdrawals)
// in one turn.
func (r *RuntimeRouter) permissionsWorldStateInputItems(
	threadID string,
	params *turn.TurnStartParams,
	modelInfo *model.ModelInfo,
	cfg *config.Config,
) ([]any, error) {
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
		item, compactErr := r.compactPermissionsWorldStateUpdate(record, state, threadID, savedPrefixHeadingForModel(modelInfo))
		if compactErr != nil || item == nil {
			return nil, compactErr
		}
		return []any{item}, nil
	}
	options := r.permissionPromptOptionsForTurn(threadID, params, modelInfo, cfg)
	descriptions, perEnvironment := r.permissionDescriptionsForTurn(threadID, params, modelInfo, cfg, options)
	if perEnvironment {
		return r.scopedPermissionsWorldStateUpdates(record, state, descriptions, options.ApprovedCommandPrefixes, savedPrefixHeadingForModel(modelInfo))
	}
	description := descriptions[""]
	rendered := sandbox.RenderPermissionInstructions(description.instructions)
	current := permissionsWorldStateSnapshot{
		Instructions:            description.fingerprint,
		ApprovedCommandPrefixes: options.ApprovedCommandPrefixes,
	}

	decoded := decodePermissionsWorldState(state.PermissionInstructions)
	if decoded.Kind == "scoped" {
		// Switching back to the unscoped format withdraws every scoped
		// description before restating the thread-scoped rules, matching Rust.
		updates := scopedPermissionsWithdrawals(decoded.Scoped)
		if strings.TrimSpace(rendered) != "" {
			updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, rendered))
		}
		if err := r.persistPermissionsWorldState(record, state, current); err != nil {
			return nil, err
		}
		return updates, nil
	}
	previous := decoded.Current
	previousKind := decoded.Kind
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
				savedPrefixes = approvedCommandPrefixSavedText(added, savedPrefixHeadingForModel(modelInfo))
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

	if err := r.persistPermissionsWorldState(record, state, current); err != nil {
		return nil, err
	}
	if savedPrefixes != "" {
		return []any{modelInputTextMessage(contextfrag.RoleDeveloper, savedPrefixes)}, nil
	}
	if !emitFull || strings.TrimSpace(rendered) == "" {
		return nil, nil
	}
	return []any{modelInputTextMessage(contextfrag.RoleDeveloper, rendered)}, nil
}

// persistPermissionsWorldState stores the section snapshot when it changed.
func (r *RuntimeRouter) persistPermissionsWorldState(record *session.Record, state *session.WorldState, snapshot any) error {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if sameJSONValue(state.PermissionInstructions, encoded) {
		return nil
	}
	state.PermissionInstructions = encoded
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return err
	}
	return r.runtimeSaveThreadRecord(record)
}

// scopedPermissionsWorldStateUpdates applies Rust's scoped permissions diff:
// stale scopes are withdrawn, changed scopes restate their instructions, and
// newly saved prefixes produce one shared notice.
func (r *RuntimeRouter) scopedPermissionsWorldStateUpdates(
	record *session.Record,
	state *session.WorldState,
	descriptions map[string]permissionDescription,
	approvedPrefixes [][]string,
	heading string,
) ([]any, error) {
	current := permissionsScopedWorldStateSnapshot{
		Environments:            map[string]string{},
		ApprovedCommandPrefixes: canonicalCommandPrefixesOrEmpty(approvedPrefixes),
	}
	for environmentID, description := range descriptions {
		if environmentID == "" {
			current.ThreadDefaults = description.fingerprint
			continue
		}
		current.Environments[environmentID] = description.fingerprint
	}
	decoded := decodePermissionsWorldState(state.PermissionInstructions)
	updates := make([]any, 0, len(descriptions)+1)
	if decoded.Kind == "scoped" {
		previous := decoded.Scoped
		previousHashes := make(map[string]string, len(previous.Environments)+1)
		for environmentID, hash := range previous.Environments {
			previousHashes[environmentID] = hash
		}
		if strings.TrimSpace(previous.ThreadDefaults) != "" {
			previousHashes[""] = previous.ThreadDefaults
		}
		for _, environmentID := range sortedPermissionScopeIDs(previousHashes) {
			if _, ok := descriptions[environmentID]; !ok {
				updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, scopedPermissionsUpdateText(environmentID, nil)))
			}
		}
		// Switching models can make saved command approvals stop applying.
		// Additions get a short notice; removals restate each scope's rules.
		prefixesRemoved := !prefixSubset(previous.ApprovedCommandPrefixes, current.ApprovedCommandPrefixes)
		for _, environmentID := range sortedPermissionScopeIDs(descriptions) {
			description := descriptions[environmentID]
			previousHash, known := previousHashes[environmentID]
			if prefixesRemoved || !known || previousHash != description.fingerprint {
				instructions := description.instructions
				updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, scopedPermissionsUpdateText(environmentID, &instructions)))
			}
		}
		if !prefixesRemoved && !equalCommandPrefixSets(previous.ApprovedCommandPrefixes, current.ApprovedCommandPrefixes) {
			added := addedCommandPrefixes(current.ApprovedCommandPrefixes, previous.ApprovedCommandPrefixes)
			if text := approvedCommandPrefixSavedText(added, heading); text != "" {
				updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, text))
			}
		}
	} else {
		// An initially blank section keeps its checkpoint without emitting
		// empty fragments (Rust #62933); the next approved prefix still reports
		// through the saved-prefix notice.
		if permissionsDescriptionsHaveInstructions(descriptions) {
			for _, environmentID := range sortedPermissionScopeIDs(descriptions) {
				description := descriptions[environmentID]
				instructions := description.instructions
				updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, scopedPermissionsUpdateText(environmentID, &instructions)))
			}
		}
	}
	if err := r.persistPermissionsWorldState(record, state, current); err != nil {
		return nil, err
	}
	return updates, nil
}

// permissionsDescriptionsHaveInstructions reports whether any scope renders a
// non-blank permission description. Rust's PermissionsState::has_instructions
// (#62933) uses the same test to skip the initial fragment.
func permissionsDescriptionsHaveInstructions(descriptions map[string]permissionDescription) bool {
	for _, description := range descriptions {
		if strings.TrimSpace(description.instructions) != "" {
			return true
		}
	}
	return false
}

// scopedPermissionsWithdrawals restates every scope a previous scoped
// snapshot described as no longer current. Rust orders environment ids first,
// then the thread defaults.
func scopedPermissionsWithdrawals(previous permissionsScopedWorldStateSnapshot) []any {
	ids := make([]string, 0, len(previous.Environments)+1)
	for environmentID := range previous.Environments {
		ids = append(ids, environmentID)
	}
	sort.Strings(ids)
	updates := make([]any, 0, len(ids)+1)
	for _, environmentID := range ids {
		updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, scopedPermissionsUpdateText(environmentID, nil)))
	}
	if strings.TrimSpace(previous.ThreadDefaults) != "" {
		updates = append(updates, modelInputTextMessage(contextfrag.RoleDeveloper, scopedPermissionsUpdateText("", nil)))
	}
	return updates
}

// scopedPermissionsUpdateText labels one environment's permission rules, or
// the thread defaults when environmentID is empty. A nil instructions value
// withdraws the previous description for that scope.
func scopedPermissionsUpdateText(environmentID string, instructions *string) string {
	var text strings.Builder
	text.WriteString(sandbox.PermissionInstructionsOpenTag)
	if strings.TrimSpace(environmentID) == "" {
		text.WriteString("<thread_defaults>\n")
	} else {
		text.WriteString(`<environment id="`)
		text.WriteString(escapeEnvironmentXML(environmentID))
		text.WriteString("\">\n")
	}
	if instructions != nil {
		text.WriteString(*instructions)
	} else {
		text.WriteString("The previous permission description for this scope is no longer current.\n")
	}
	if strings.TrimSpace(environmentID) == "" {
		text.WriteString("</thread_defaults>")
	} else {
		text.WriteString("</environment>")
	}
	text.WriteString(sandbox.PermissionInstructionsCloseTag)
	return text.String()
}

func sortedPermissionScopeIDs[T any](values map[string]T) []string {
	ids := make([]string, 0, len(values))
	for environmentID := range values {
		ids = append(ids, environmentID)
	}
	sort.Strings(ids)
	return ids
}

func canonicalCommandPrefixesOrEmpty(prefixes [][]string) [][]string {
	canonical := execpolicy.CanonicalCommandPrefixes(prefixes)
	if canonical == nil {
		return [][]string{}
	}
	return canonical
}

func equalCommandPrefixSets(left [][]string, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]bool, len(left))
	for _, prefix := range left {
		seen[commandPrefixKey(prefix)] = true
	}
	for _, prefix := range right {
		if !seen[commandPrefixKey(prefix)] {
			return false
		}
	}
	return true
}

// compactPermissionsWorldStateUpdate ports Rust CompactPermissionsState: the
// persisted snapshot is the approved prefix set, an unknown/absent previous
// state emits nothing, and the newly added prefixes are reported.
func (r *RuntimeRouter) compactPermissionsWorldStateUpdate(
	record *session.Record,
	state *session.WorldState,
	threadID string,
	heading string,
) (any, error) {
	current := r.approvedCommandPrefixesForThread(threadID)
	previous, previousKind := decodeApprovedCommandPrefixes(state.ApprovedCommandPrefixes)
	emit := ""
	if previousKind == "current" {
		emit = approvedCommandPrefixSavedText(addedCommandPrefixes(current, previous), heading)
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

// decodedPermissionsWorldState distinguishes the three persisted formats
// Rust's untagged PermissionsSnapshot accepts: Current, Scoped, and the legacy
// bare hash older Go builds wrote.
type decodedPermissionsWorldState struct {
	Kind    string
	Current permissionsWorldStateSnapshot
	Scoped  permissionsScopedWorldStateSnapshot
}

func decodePermissionsWorldState(raw json.RawMessage) decodedPermissionsWorldState {
	if len(raw) == 0 {
		return decodedPermissionsWorldState{Kind: "absent"}
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err == nil {
		if _, ok := keys["instructions"]; ok {
			var current permissionsWorldStateSnapshot
			if err := json.Unmarshal(raw, &current); err == nil && strings.TrimSpace(current.Instructions) != "" {
				return decodedPermissionsWorldState{Kind: "current", Current: current}
			}
		}
		if _, ok := keys["environments"]; ok {
			var scoped permissionsScopedWorldStateSnapshot
			if err := json.Unmarshal(raw, &scoped); err == nil {
				if scoped.Environments == nil {
					scoped.Environments = map[string]string{}
				}
				scoped.ApprovedCommandPrefixes = canonicalCommandPrefixesOrEmpty(scoped.ApprovedCommandPrefixes)
				return decodedPermissionsWorldState{Kind: "scoped", Scoped: scoped}
			}
		}
	}
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err == nil && strings.TrimSpace(legacy) != "" {
		return decodedPermissionsWorldState{
			Kind:    "legacy",
			Current: permissionsWorldStateSnapshot{Instructions: strings.TrimSpace(legacy)},
		}
	}
	return decodedPermissionsWorldState{Kind: "unknown"}
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

// permissionsWorldStateSessionItemsForTurn persists every fragment emitted for
// one turn. Scoped descriptions can produce several messages with distinct
// scope labels, so their stored ids carry the emission index.
func permissionsWorldStateSessionItemsForTurn(turnID string, inputs []any, createdAt time.Time) []session.Item {
	if len(inputs) == 0 {
		return nil
	}
	items := make([]session.Item, 0, len(inputs))
	for index, input := range inputs {
		item, ok := permissionsWorldStateSessionItemForTurn(turnID, input, createdAt)
		if !ok {
			continue
		}
		if len(inputs) > 1 {
			item.ID = fmt.Sprintf("%s-%d", item.ID, index)
		}
		items = append(items, item)
	}
	return items
}
