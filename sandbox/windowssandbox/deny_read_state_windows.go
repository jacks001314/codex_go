//go:build windows

package windowssandbox

// SyncPersistentDenyReadACLs applies the desired deny-read ACLs for one SID and
// records them so a later profile change can revoke what is no longer wanted.
// Malformed bookkeeping is rebuilt from the paths this call applies; unknown
// historical restrictions are never revoked because their paths are unknown
// (Rust #50940).
func SyncPersistentDenyReadACLs(codexHome string, paths []string, sid string) ([]string, error) {
	if codexHome == "" || sid == "" {
		return nil, ErrInvalidRequest
	}
	statePath := denyReadACLStatePath(codexHome)
	state, err := loadDenyReadACLState(statePath)
	if err != nil {
		return nil, err
	}
	previousPaths := cloneStrings(state.Principals[sid])
	appliedPaths, err := ApplyDenyReadACLs(paths, sid)
	if err != nil {
		return nil, err
	}
	desiredKeys := map[string]bool{}
	for _, path := range appliedPaths {
		desiredKeys[lexicalPathKey(path)] = true
	}
	for _, path := range previousPaths {
		if !desiredKeys[lexicalPathKey(path)] {
			_ = RevokeACE(ACLRequest{Path: path, SID: sid})
		}
	}
	if len(appliedPaths) == 0 {
		delete(state.Principals, sid)
	} else {
		state.Principals[sid] = cloneStrings(appliedPaths)
	}
	if err := storeDenyReadACLState(statePath, state); err != nil {
		return nil, err
	}
	return appliedPaths, nil
}
