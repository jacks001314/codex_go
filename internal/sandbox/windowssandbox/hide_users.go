package windowssandbox

const UserListRegistryPath = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`

func HideCurrentUserProfileDir(logBase string) error {
	return hideCurrentUserProfileDir(logBase)
}

func HideNewlyCreatedUsers(usernames []string, logBase string) error {
	if len(usernames) == 0 {
		return nil
	}
	return hideNewlyCreatedUsers(usernames, logBase)
}
