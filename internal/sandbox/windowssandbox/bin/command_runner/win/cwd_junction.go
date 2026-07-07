package win

import (
	"fmt"
	"hash/fnv"
	"path/filepath"

	"codex_go/internal/sandbox/windowssandbox"
)

func JunctionNameForPath(path string) string {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(path))
	return fmt.Sprintf("%x", hasher.Sum64())
}

func JunctionRootForUserProfile(userprofile string) string {
	return filepath.Join(userprofile, ".codex", ".sandbox", "cwd")
}

func CreateCWDJunction(cwd string) (string, error) {
	return CreateCWDJunctionWithLogDir(cwd, "")
}

func CreateCWDJunctionWithLogDir(cwd string, logDir string) (string, error) {
	if cwd == "" {
		return "", windowssandbox.ErrInvalidRequest
	}
	return createCWDJunction(cwd, logDir)
}
