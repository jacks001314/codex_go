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

func PathMaskAllows(req ACLRequest, requireAllBits bool) (bool, error) {
	return false, unsupported("acl.path_mask_allows")
}
