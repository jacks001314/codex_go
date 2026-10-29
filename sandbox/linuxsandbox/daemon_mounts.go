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
		// Every destination is validated because it decides nested mounts and
		// aliases. Only a path root can alias the socket directory: namespace
		// mounts have roots such as mnt:[inode] or net:[inode] and are checked
		// by destination alone (Rust #46535).
		destination, err := mountinfoPath(fields[4])
		if err != nil {
			return errors.New("cannot establish app-server socket mount isolation")
		}
		root, rootErr := mountinfoPath(fields[3])
		if rootErr != nil {
			root = ""
		}
		if destination == directory || strings.HasPrefix(destination, directory+string(filepath.Separator)) {
			return daemonSocketMountError(destination)
		}
		if root != "" && (root == directory || strings.HasPrefix(root, directory+string(filepath.Separator))) &&
			!strings.HasPrefix(destination, "/mnt/wslg/distro") {
			return daemonSocketMountError(destination)
		}
	}
	return nil
}

func daemonSocketMountError(path string) error {
	return fmt.Errorf("app-server socket directory has an unsupported host mount at %s; remove the bind-mount alias or nested mount before starting the sandbox", path)
}

// mountinfoPath decodes one mountinfo path field and requires an absolute
// result (Rust `mount_path`): only the four standard escapes are accepted, so a
// malformed destination rejects sandbox startup instead of being skipped.
func mountinfoPath(value string) (string, error) {
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			decoded.WriteByte(value[index])
			continue
		}
		if index+3 >= len(value) {
			return "", errInvalidMountinfoPath
		}
		switch value[index+1 : index+4] {
		case "040":
			decoded.WriteByte(' ')
		case "011":
			decoded.WriteByte('\t')
		case "012":
			decoded.WriteByte('\n')
		case "134":
			decoded.WriteByte('\\')
		default:
			return "", errInvalidMountinfoPath
		}
		index += 3
	}
	path := decoded.String()
	if !filepath.IsAbs(path) {
		return "", errInvalidMountinfoPath
	}
	return path, nil
}

var errInvalidMountinfoPath = errors.New("invalid mountinfo path")
