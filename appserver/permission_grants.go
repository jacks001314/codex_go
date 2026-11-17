package appserver

import (
	"strings"

	"codex_go/sandbox"
	"codex_go/turn"
)

type permissionGrantState struct {
	session map[string]*sandbox.AdditionalPermissionProfile
	turns   map[string]map[string]*sandbox.AdditionalPermissionProfile
}

func permissionGrantEnvironmentKey(environmentID string) string {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return "local"
	}
	return environmentID
}

func (r *RuntimeRouter) recordPermissionGrant(threadID, turnID, environmentID, scope string, profile *sandbox.AdditionalPermissionProfile) {
	if r == nil || profile == nil || profile.IsEmpty() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	environmentKey := permissionGrantEnvironmentKey(environmentID)
	r.permissionGrantsMu.Lock()
	defer r.permissionGrantsMu.Unlock()
	if r.permissionGrants == nil {
		r.permissionGrants = map[string]*permissionGrantState{}
	}
	state := r.permissionGrants[threadID]
	if state == nil {
		state = &permissionGrantState{}
		r.permissionGrants[threadID] = state
	}
	if strings.EqualFold(strings.TrimSpace(scope), string(PermissionGrantScopeSession)) {
		if state.session == nil {
			state.session = map[string]*sandbox.AdditionalPermissionProfile{}
		}
		state.session[environmentKey] = sandbox.MergePermissionProfiles(state.session[environmentKey], profile)
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	if state.turns == nil {
		state.turns = map[string]map[string]*sandbox.AdditionalPermissionProfile{}
	}
	turn := state.turns[turnID]
	if turn == nil {
		turn = map[string]*sandbox.AdditionalPermissionProfile{}
		state.turns[turnID] = turn
	}
	turn[environmentKey] = sandbox.MergePermissionProfiles(turn[environmentKey], profile)
}

func (r *RuntimeRouter) grantedPermissionsForEnvironment(threadID, turnID, environmentID string) *sandbox.AdditionalPermissionProfile {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	environmentKey := permissionGrantEnvironmentKey(environmentID)
	r.permissionGrantsMu.Lock()
	defer r.permissionGrantsMu.Unlock()
	state := r.permissionGrants[threadID]
	if state == nil {
		return nil
	}
	var granted *sandbox.AdditionalPermissionProfile
	if profile := state.session[environmentKey]; profile != nil {
		granted = sandbox.MergePermissionProfiles(granted, profile)
	}
	if turn := state.turns[strings.TrimSpace(turnID)]; turn != nil {
		granted = sandbox.MergePermissionProfiles(granted, turn[environmentKey])
	}
	if granted.IsEmpty() {
		return nil
	}
	return granted
}

// turnGrantedPermissionsForEnvironment returns only the turn-scope grants
// (Rust TurnContext::turn_grants). Skill catalog grant tracking must not count
// session-scope approvals: those do not expire with the turn, so the catalog
// would never have to tell the model to revalidate.
func (r *RuntimeRouter) turnGrantedPermissionsForEnvironment(threadID, turnID, environmentID string) *sandbox.AdditionalPermissionProfile {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return nil
	}
	r.permissionGrantsMu.Lock()
	defer r.permissionGrantsMu.Unlock()
	turn := r.permissionGrants[threadID]
	if turn == nil {
		return nil
	}
	profile := turn.turns[turnID][permissionGrantEnvironmentKey(environmentID)]
	if profile == nil || profile.IsEmpty() {
		return nil
	}
	return sandbox.MergePermissionProfiles(nil, profile)
}

// skillFilesystemGrantsForTurn mirrors Rust TurnContext::turn_skill_filesystem_grants:
// the turn environments whose turn-scoped approval carries a non-empty
// filesystem profile, plus the primary environment (the first one) that
// repository-scoped host skills inherit their grant from.
func (r *RuntimeRouter) skillFilesystemGrantsForTurn(threadID, turnID string, params *turn.TurnStartParams) (string, map[string]struct{}) {
	granted := map[string]struct{}{}
	environments := r.unifiedExecEnvironmentsForTurn(params)
	if len(environments) == 0 {
		// Rust's TurnEnvironmentSnapshot always carries the implicit local
		// environment, whose grants are keyed "local" here.
		if permissionsHaveFilesystem(r.turnGrantedPermissionsForEnvironment(threadID, turnID, localEnvironmentID)) {
			granted[localEnvironmentID] = struct{}{}
		}
		return localEnvironmentID, granted
	}
	primary := ""
	for _, environment := range environments {
		id := strings.TrimSpace(environment.ID)
		if id == "" {
			continue
		}
		if primary == "" {
			primary = id
		}
		if permissionsHaveFilesystem(r.turnGrantedPermissionsForEnvironment(threadID, turnID, id)) {
			granted[id] = struct{}{}
		}
	}
	return primary, granted
}

// permissionsHaveFilesystem reports a non-empty filesystem grant in either
// direction (Rust AdditionalPermissionProfile::file_system is non-empty).
func permissionsHaveFilesystem(profile *sandbox.AdditionalPermissionProfile) bool {
	if profile == nil {
		return false
	}
	return len(profile.FileSystem) > 0 || len(profile.ReadFileSystem) > 0
}

// sessionGrantedPermissionsForEnvironment returns only the session-scope
// grants (Rust Session::granted_permissions, #60461). Persistent consumers -
// plugin servers, hooks, and the session capability inventory - must never widen
// with a turn-scoped approval.
func (r *RuntimeRouter) sessionGrantedPermissionsForEnvironment(threadID, environmentID string) *sandbox.AdditionalPermissionProfile {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	environmentKey := permissionGrantEnvironmentKey(environmentID)
	r.permissionGrantsMu.Lock()
	defer r.permissionGrantsMu.Unlock()
	state := r.permissionGrants[threadID]
	if state == nil {
		return nil
	}
	profile := state.session[environmentKey]
	if profile == nil || profile.IsEmpty() {
		return nil
	}
	return sandbox.MergePermissionProfiles(nil, profile)
}

// skillDiscoveryGrantedPermissions mirrors Rust's discovery_sandbox (#60461): a
// discovery that belongs to a turn sees that turn's grants (session plus turn),
// while a turn-less discovery only sees session-scope grants. Approved
// filesystem access therefore makes executor skills discoverable for the life
// of the grant without letting a turn-scoped approval reach persistent plugin
// servers or hooks.
func (r *RuntimeRouter) skillDiscoveryGrantedPermissions(threadID, turnID, environmentID string) *sandbox.AdditionalPermissionProfile {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(turnID) == "" {
		return r.sessionGrantedPermissionsForEnvironment(threadID, environmentID)
	}
	return r.grantedPermissionsForEnvironment(threadID, turnID, environmentID)
}

func (r *RuntimeRouter) clearTurnPermissionGrants(threadID, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	r.permissionGrantsMu.Lock()
	defer r.permissionGrantsMu.Unlock()
	if state := r.permissionGrants[threadID]; state != nil {
		delete(state.turns, turnID)
	}
}

func (r *RuntimeRouter) forgetPermissionGrants(threadID string) {
	if r == nil {
		return
	}
	r.permissionGrantsMu.Lock()
	delete(r.permissionGrants, strings.TrimSpace(threadID))
	r.permissionGrantsMu.Unlock()
}
