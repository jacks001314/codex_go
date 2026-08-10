//go:build !windows

package appserver

func processWorkingSetBytes() (uint64, bool) {
	return 0, false
}
