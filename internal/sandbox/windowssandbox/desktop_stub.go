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

func (d *LaunchDesktop) StartupInfoDesktop() *uint16 {
	return nil
}

func (d *LaunchDesktop) Close() error {
	return nil
}
