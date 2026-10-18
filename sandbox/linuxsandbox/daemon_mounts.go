//go:build linux

package linuxsandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// validateDaemonSocketMountIsolation rejects host mount aliases and nested
// mounts that would expose the fixed app-server rendezvous directory beyond
// the canonical path bwrap masks (Rust #45984).
func validateDaemonSocketMountIsolation(directory string, mountinfo []byte) error {
	directory = filepath.Clean(directory)
	if directory == "." || directory == "/" {
		return errors.New("cannot establish app-server socket mount isolation")
	}
	for _, line := range strings.Split(string(mountinfo), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return errors.New("cannot establish app-server socket mount isolation")
		}
		root := unescapeMountinfoPath(fields[3])
		destination := unescapeMountinfoPath(fields[4])
		if !filepath.IsAbs(root) || !filepath.IsAbs(destination) {
			continue
		}
		if destination == directory || strings.HasPrefix(destination, directory+string(filepath.Separator)) {
			return daemonSocketMountError(destination)
		}
		if (root == directory || strings.HasPrefix(root, directory+string(filepath.Separator))) &&
			!strings.HasPrefix(destination, "/mnt/wslg/distro") {
			return daemonSocketMountError(destination)
		}
	}
	return nil
}

func daemonSocketMountError(path string) error {
	return fmt.Errorf("app-server socket directory has an unsupported host mount at %s; remove the bind-mount alias or nested mount before starting the sandbox", path)
}

func unescapeMountinfoPath(value string) string {
	replacer := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	)
	return replacer.Replace(value)
}
