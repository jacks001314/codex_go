//go:build linux

package linuxsandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Rust parity: codex-rs/linux-sandbox/src/daemon_mounts.rs (#45984, #46125).
//
// Mount roots describe filesystem identity, so canonical paths alone miss bind
// mounts. The opened socket directory's mount ID, when available, is followed
// towards the namespace root: mounts below that branch are hidden behind the
// branch, while mounts above it cover it. Without a mount ID every possible
// containing mount must agree on the backing location.

var errDaemonSocketIsolation = errors.New("cannot establish app-server socket mount isolation")

type daemonMount struct {
	id          string
	parent      string
	device      string
	root        string
	rootIsPath  bool
	destination string
}

// validateDaemonSocketMountIsolation rejects host mount layouts that would
// expose the app-server rendezvous directory beyond the paths bwrap masks.
//
// device is the socket directory's mount device as "major:minor" and mountID
// the mount ID read from the opened directory (empty when the kernel cannot
// report one). maskedRoot is a subtree the sandbox masks separately, so an
// alias there does not count as exposed.
func validateDaemonSocketMountIsolation(directory string, device string, mountID string, mountinfo []byte, maskedRoot string) error {
	directory = filepath.Clean(directory)
	if directory == "." || directory == "/" {
		return errDaemonSocketIsolation
	}
	mounts, err := parseDaemonMounts(mountinfo)
	if err != nil {
		return err
	}

	var location string
	containingID := ""
	containingDestination := ""
	if mountID != "" {
		selected := -1
		for index := range mounts {
			if mounts[index].id == mountID {
				selected = index
				break
			}
		}
		if selected < 0 {
			return errDaemonSocketIsolation
		}
		mount := mounts[selected]
		if mount.device != device || !mount.rootIsPath {
			return errDaemonSocketIsolation
		}
		relative, ok := relativeWithin(directory, mount.destination)
		if !ok {
			return errDaemonSocketIsolation
		}
		if err := validateDaemonMountAncestry(mounts, selected, directory); err != nil {
			return err
		}
		location = joinMountPath(mount.root, relative)
		containingID = mountID
		containingDestination = mount.destination
	} else {
		// Without a mount ID, require every possible containing mount to agree on
		// the backing location and do not assume any alias is hidden.
		locations := map[string]bool{}
		for index := range mounts {
			mount := mounts[index]
			if mount.device != device || !mount.rootIsPath {
				continue
			}
			relative, ok := relativeWithin(directory, mount.destination)
			if !ok {
				continue
			}
			locations[joinMountPath(mount.root, relative)] = true
		}
		if len(locations) != 1 {
			return errDaemonSocketIsolation
		}
		for value := range locations {
			location = value
		}
	}

	for index := range mounts {
		mount := mounts[index]
		// Nested mounts can introduce another filesystem (or an individual
		// socket) under the mask.
		nested := mount.destination != directory && pathWithin(mount.destination, directory)
		aliasPath := ""
		aliasSet := false
		// Rust only derives aliases between mounts of the socket's own device:
		// another filesystem mounted at an overlapping path is a different
		// backing store, not a visible duplicate of the socket directory.
		if mount.device == device && mount.rootIsPath {
			if relative, ok := relativeWithin(location, mount.root); ok {
				aliasPath = joinMountPath(mount.destination, relative)
				aliasSet = true
			} else if pathWithin(mount.root, location) {
				aliasPath = mount.destination
				aliasSet = true
			}
		}
		if aliasSet {
			// An ancestor's path beneath this mount is hidden by it; keep
			// checking other mounts, including aliases mounted beneath it.
			hidden := containingID != "" &&
				mount.id != containingID &&
				pathWithin(containingDestination, mount.destination) &&
				pathWithin(aliasPath, containingDestination)
			if pathWithin(aliasPath, directory) ||
				(maskedRoot != "" && pathWithin(aliasPath, filepath.Clean(maskedRoot))) ||
				hidden {
				aliasSet = false
			}
		}
		if nested || aliasSet {
			return daemonSocketMountError(mount.destination)
		}
	}
	return nil
}

// validateDaemonMountAncestry walks the selected mount towards the namespace
// root. A mount on that branch hides the branch path beneath it, so a sibling
// mount covers the selected mount; a branch-mounted child is the visible one.
func validateDaemonMountAncestry(mounts []daemonMount, selected int, directory string) error {
	visited := map[string]bool{}
	visibleChild := ""
	hasVisibleChild := false
	current := selected
	for {
		mount := mounts[current]
		if visited[mount.id] {
			return errDaemonSocketIsolation
		}
		visited[mount.id] = true
		for index := range mounts {
			child := mounts[index]
			if child.id == mount.id || child.parent != mount.id {
				continue
			}
			if !pathWithin(directory, child.destination) {
				continue
			}
			if hasVisibleChild && pathWithin(child.destination, visibleChild) {
				continue
			}
			return errDaemonSocketIsolation
		}
		if mount.id == mount.parent {
			return nil
		}
		visibleChild = mount.destination
		hasVisibleChild = true
		next := -1
		for index := range mounts {
			if mounts[index].id == mount.parent {
				next = index
				break
			}
		}
		if next < 0 {
			return nil
		}
		current = next
	}
}

func parseDaemonMounts(mountinfo []byte) ([]daemonMount, error) {
	mounts := []daemonMount{}
	for _, line := range strings.Split(string(mountinfo), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return nil, errDaemonSocketIsolation
		}
		destination, err := mountinfoPath(fields[4])
		if err != nil {
			return nil, errDaemonSocketIsolation
		}
		root, rootErr := mountinfoPath(fields[3])
		mounts = append(mounts, daemonMount{
			id:          fields[0],
			parent:      fields[1],
			device:      fields[2],
			root:        root,
			rootIsPath:  rootErr == nil,
			destination: destination,
		})
	}
	return mounts, nil
}

func daemonSocketMountError(path string) error {
	return fmt.Errorf("app-server socket directory has an unsupported host mount at %s; remove the bind-mount alias or nested mount before starting the sandbox", path)
}

// pathWithin reports whether candidate is root itself or a descendant of root.
// Both arguments must be cleaned absolute paths.
func pathWithin(candidate string, root string) bool {
	if root == "" {
		return false
	}
	if candidate == root {
		return true
	}
	if root == "/" {
		return strings.HasPrefix(candidate, "/")
	}
	return strings.HasPrefix(candidate, root+"/")
}

// relativeWithin returns the part of candidate below root, and false when
// candidate is not root or a descendant.
func relativeWithin(candidate string, root string) (string, bool) {
	if root == "" {
		return "", false
	}
	if candidate == root {
		return "", true
	}
	if root == "/" {
		if strings.HasPrefix(candidate, "/") {
			return strings.TrimPrefix(candidate, "/"), true
		}
		return "", false
	}
	if strings.HasPrefix(candidate, root+"/") {
		return candidate[len(root)+1:], true
	}
	return "", false
}

// joinMountPath appends a relative remainder to a mount root or destination.
func joinMountPath(root string, relative string) string {
	if relative == "" {
		return root
	}
	return strings.TrimSuffix(root, "/") + "/" + relative
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
