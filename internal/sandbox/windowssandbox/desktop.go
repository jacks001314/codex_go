package windowssandbox

type LaunchDesktop struct {
	Name        string
	StartupName string
	Handle      uintptr
	startupWide []uint16
}
