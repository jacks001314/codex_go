package windowssandbox

import (
	"path/filepath"
	"strings"
)

// legacyPathLimit mirrors the threshold in the Go standard library's
// `os.addExtendedPrefix`: Windows caps ordinary paths at MAX_PATH (260), and
// 260-12 = 248 is the accepted bound for a path that still has room to append
// an 8.3 child name.
const legacyPathLimit = 248

// extendedACLPath returns the extended-length (`\\?\`) spelling of a path when
// the Windows security APIs need one. Rust's `acl` module gets this for free
// because it opens ACL targets with `std::fs::OpenOptions`; the Go security
// wrappers hand the name straight to the syscall, so a runtime path nested
// beyond the legacy limit has to be spelled here before `CreateFile` sees it.
//
// Already-extended and device paths are returned unchanged, short paths keep
// the caller's spelling, and a long absolute path is normalized before the
// prefix is added: the extended form turns off the object manager's own `.` and
// `..` handling, and `os.addExtendedPrefix` likewise normalizes through
// `GetFullPathNameW` first.
func extendedACLPath(path string) string {
	if len(path) >= 4 {
		switch {
		case path[:4] == `\??\`,
			isPathSeparator(path[0]) && isPathSeparator(path[1]) && path[2] == '?' && isPathSeparator(path[3]):
			// Already spelled in extended form.
			return path
		case isPathSeparator(path[0]) && isPathSeparator(path[1]) && path[2] == '.' && isPathSeparator(path[3]):
			// An extended prefix would change the meaning of a device path.
			return path
		}
	}
	if len(path) < legacyPathLimit || !isAbsACLPath(path) {
		// A verbatim path must be absolute; shorter and relative paths keep the
		// caller's spelling.
		return path
	}
	normalized := filepath.Clean(path)
	if strings.HasPrefix(normalized, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(normalized, `\\`)
	}
	return `\\?\` + normalized
}

func isPathSeparator(b byte) bool { return b == '\\' || b == '/' }

// isAbsACLPath reports a Windows-absolute path without consulting the host
// platform, so the spelling rules above stay reviewable (and testable) off
// Windows.
func isAbsACLPath(path string) bool {
	if len(path) >= 2 && isPathSeparator(path[0]) && isPathSeparator(path[1]) {
		return true
	}
	if len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && isPathSeparator(path[2]) {
		return true
	}
	return len(path) > 0 && isPathSeparator(path[0])
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
