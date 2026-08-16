//go:build !windows

package doctor

// windowsDevDriveCheck is a stub outside Windows (the call site guards it with
// runtime.GOOS == "windows", so it never runs here); the Windows
// implementation lives in dev_drive_windows.go.
func windowsDevDriveCheck(cwd string) *DoctorCheck {
	return nil
}
