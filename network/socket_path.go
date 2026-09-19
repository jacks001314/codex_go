package network

import (
	"strings"

	"codex_go/utils"
)

// SocketPathIsAbsolute reports whether a unix-socket allowlist entry is an
// absolute path for the executor's platform, independent of the controller OS.
// It mirrors Rust network-proxy socket_path.rs (#46302/#46334): socket support
// and native path normalization remain executor runtime concerns, so this only
// parses the grammar.
func SocketPathIsAbsolute(platform utils.Platform, path string) bool {
	// Core also accepts Unix-style absolute paths on Windows, for portability.
	if strings.HasPrefix(path, "/") {
		return true
	}
	if convention, ok := platform.PathConvention(); ok && convention != utils.ConventionWindows {
		return false
	}
	// Windows, or legacy executors without OS metadata that validate against
	// their own platform at launch: accept either absolute syntax.
	return windowsPathIsAbsolute(path)
}

// windowsPathIsAbsolute matches the Windows absolute-path syntax without
// consulting the host. Non-drive prefixes have an implicit root, including
// device and verbatim paths.
func windowsPathIsAbsolute(path string) bool {
	bytes := []byte(path)
	if len(bytes) >= 3 && socketPathASCIIAlpha(bytes[0]) && bytes[1] == ':' && socketPathSeparator(bytes[2]) {
		return true
	}
	if len(bytes) < 2 || !socketPathSeparator(bytes[0]) || !socketPathSeparator(bytes[1]) {
		return false
	}
	rest := bytes[2:]
	if strings.HasPrefix(path, `\\?\`) {
		return true
	}
	if len(rest) >= 2 && rest[0] == '.' && socketPathSeparator(rest[1]) {
		return true
	}
	segments := socketPathSegments(rest)
	return len(segments) >= 2 && len(segments[0]) > 0 && len(segments[1]) > 0
}

// socketPathSegments splits on separators while preserving empty segments, the
// way Rust's `rest.split(|byte| ...)` does.
func socketPathSegments(b []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i := 0; i < len(b); i++ {
		if socketPathSeparator(b[i]) {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return append(out, b[start:])
}

func socketPathSeparator(b byte) bool {
	return b == '\\' || b == '/'
}

func socketPathASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
