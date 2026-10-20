//go:build !unix

package rollout

import "os"

// preserveRolloutAccessMetadata copies permissions and mtime on platforms
// without Unix ownership metadata.
func preserveRolloutAccessMetadata(_ string, destinationPath string, sourceInfo os.FileInfo) error {
	if sourceInfo == nil {
		return nil
	}
	if err := os.Chmod(destinationPath, sourceInfo.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(destinationPath, sourceInfo.ModTime(), sourceInfo.ModTime())
}
