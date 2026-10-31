//go:build !windows

package windowssandbox

// PrepareWindowsSandboxPrivateDesktopName is Windows-only: other platforms
// cannot create Win32 desktops, so the wrapper command line stays unchanged.
func PrepareWindowsSandboxPrivateDesktopName(req WindowsSandboxCommandArgsRequest) (string, error) {
	return "", nil
}
