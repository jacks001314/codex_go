//go:build !windows

package windowssandbox

func ApplyDenyReadACLs(paths []string, sid string) ([]string, error) {
	return nil, unsupported("deny_read_acl.apply_deny_read_acls")
}
