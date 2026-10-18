//go:build windows

package sandbox

func sharedDaemonSocketDirectory() (string, bool) {
	return "", false
}
