//go:build !windows

package windowssandbox

func hideNewlyCreatedUsers(usernames []string, logBase string) error {
	return unsupported("hide_users.hide_newly_created_users")
}

func hideCurrentUserProfileDir(logBase string) error {
	return unsupported("hide_users.hide_current_user_profile_dir")
}

func HideDirectory(path string) (bool, error) {
	return false, unsupported("hide_users.hide_directory")
}
