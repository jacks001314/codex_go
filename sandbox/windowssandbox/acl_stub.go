//go:build !windows

package windowssandbox

func AddDenyReadACE(req ACLRequest) error {
	return unsupported("acl.add_deny_read_ace")
}

func AddDenyWriteACE(req ACLRequest) error {
	return unsupported("acl.add_deny_write_ace")
}

func EnsureAllowWriteACEs(req ACLRequest) error {
	return unsupported("acl.ensure_allow_write_aces")
}

func EnsureAllowMaskACEsWithInheritance(req ACLRequest, inheritance uint32) error {
	return unsupported("acl.ensure_allow_mask_aces_with_inheritance")
}

func GrantAllowMaskACEsWithInheritance(req ACLRequest, inheritance uint32) (bool, error) {
	return false, unsupported("acl.grant_allow_mask_aces_with_inheritance")
}

func GrantReadExecuteACEs(req ACLRequest, inheritance uint32) (bool, error) {
	return false, unsupported("acl.grant_read_execute_aces")
}

func PathMaskAllows(req ACLRequest, requireAllBits bool) (bool, error) {
	return false, unsupported("acl.path_mask_allows")
}

func PathMaskDenied(req ACLRequest) (bool, error) {
	return false, unsupported("acl.path_mask_denied")
}
