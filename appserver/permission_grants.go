package appserver

import (
	"strings"

	"codex_go/sandbox"
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
