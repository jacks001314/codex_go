//go:build !windows

package windowssandbox

func CreateLaunchDesktop(name string) (*LaunchDesktop, error) {
	return nil, unsupported("desktop.launch_desktop")
}

func PrepareLaunchDesktop(usePrivateDesktop bool) (*LaunchDesktop, error) {
	if usePrivateDesktop {
		return nil, unsupported("desktop.launch_desktop")
	}
	return &LaunchDesktop{Name: "Default", StartupName: `Winsta0\Default`}, nil
}

func ValidatePrivateDesktopName(name string) error {
	return ValidatePrivateDesktopNameFormat(name)
}

func OpenPrivateLaunchDesktop(name string) (*LaunchDesktop, error) {
	return nil, unsupported("desktop.open_private")
}

func SharedPrivateDesktopForUser(username string, policy DesktopPolicy) (string, error) {
	return "", unsupported("desktop.shared_private_desktop_for_user")
}

func PrepareLegacyLaunchDesktop(security *LegacySessionSecurity, permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string, additionalDenyWritePaths []string, logsBaseDir string) (*LaunchDesktop, error) {
	return nil, unsupported("desktop.prepare_legacy")
}

func SharedLegacyPrivateDesktopName(security *LegacySessionSecurity, permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string, additionalDenyWritePaths []string, logsBaseDir string) (string, error) {
	return "", unsupported("desktop.shared_legacy_name")
}

func (d *LaunchDesktop) StartupInfoDesktop() *uint16 {
	return nil
}

func (d *LaunchDesktop) Close() error {
	return nil
}
