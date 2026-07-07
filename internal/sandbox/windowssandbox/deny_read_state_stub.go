//go:build !windows

package windowssandbox

func SyncPersistentDenyReadACLs(codexHome string, paths []string, sid string) ([]string, error) {
	return nil, unsupported("deny_read_state.sync_persistent_deny_read_acls")
}
