//go:build !unix && !windows

package doctor

func platformAvailableDiskSpace(path string) (uint64, bool) {
	return 0, false
}
