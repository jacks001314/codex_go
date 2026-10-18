//go:build windows

package integrity

import "os"

func multipleHardLinksTag(os.FileInfo) string {
	return "unknown"
}
