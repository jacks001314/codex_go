//go:build !windows

package execserver

import "os"

func noFollowComponentIsReparse(string, os.FileInfo) bool {
	return false
}
