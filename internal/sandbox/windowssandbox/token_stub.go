//go:build !windows

package windowssandbox

func ConvertStringSIDToSID(value string) (*LocalSID, error) {
	if value == "" {
		return nil, ErrInvalidRequest
	}
	return nil, unsupported("token.convert_string_sid_to_sid")
}

func GetCurrentTokenForRestriction() (uintptr, error) {
	return 0, unsupported("token.get_current_token_for_restriction")
}

func CreateReadonlyTokenWithCapsFrom(token uintptr, caps []string) (uintptr, error) {
	return 0, unsupported("token.create_readonly_token_with_caps_from")
}

func CreateWorkspaceWriteTokenWithCapsFrom(token uintptr, caps []string) (uintptr, error) {
	return 0, unsupported("token.create_workspace_write_token_with_caps_from")
}

func CreateReadonlyTokenWithCapsAndUserFrom(token uintptr, caps []string) (uintptr, error) {
	return 0, unsupported("token.create_readonly_token_with_caps_and_user_from")
}

func CreateWorkspaceWriteTokenWithCapsAndUserFrom(token uintptr, caps []string) (uintptr, error) {
	return 0, unsupported("token.create_workspace_write_token_with_caps_and_user_from")
}

func CloseTokenHandle(token uintptr) error {
	if token == 0 {
		return nil
	}
	return unsupported("token.close_token_handle")
}
