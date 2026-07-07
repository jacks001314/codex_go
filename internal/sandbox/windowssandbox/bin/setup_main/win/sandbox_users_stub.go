//go:build !windows

package win

import (
	"io"

	"codex_go/internal/sandbox/windowssandbox"
)

func EnsureLocalUser(name string, password string, log io.Writer) error {
	return windowssandbox.Unsupported("bin.setup_main.win.sandbox_users.ensure_local_user")
}

func EnsureLocalGroup(name string, comment string, log io.Writer) error {
	return windowssandbox.Unsupported("bin.setup_main.win.sandbox_users.ensure_local_group")
}

func EnsureLocalGroupMember(groupName string, memberName string) error {
	return windowssandbox.Unsupported("bin.setup_main.win.sandbox_users.ensure_local_group_member")
}

func ResolveSandboxUsersGroupSID() ([]byte, error) {
	return nil, windowssandbox.Unsupported("bin.setup_main.win.sandbox_users.resolve_group_sid")
}

func ResolveSID(name string) ([]byte, error) {
	return nil, windowssandbox.Unsupported("bin.setup_main.win.sandbox_users.resolve_sid")
}

func LookupAccountNameForSID(sidString string) (string, error) {
	return "", windowssandbox.Unsupported("bin.setup_main.win.sandbox_users.lookup_account_name_for_sid")
}
