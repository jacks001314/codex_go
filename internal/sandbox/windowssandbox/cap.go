package windowssandbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	json "github.com/goccy/go-json"
	"github.com/google/uuid"
)

type CapabilitySIDs struct {
	Workspace          string            `json:"workspace"`
	Readonly           string            `json:"readonly"`
	WorkspaceByCWD     map[string]string `json:"workspace_by_cwd,omitempty"`
	WritableRootByPath map[string]string `json:"writable_root_by_path,omitempty"`
}

func CapSIDFile(codexHome string) string {
	return filepath.Join(codexHome, "cap_sid")
}

func LoadOrCreateCapabilitySIDs(codexHome string) (*CapabilitySIDs, error) {
	if codexHome == "" {
		return nil, ErrInvalidRequest
	}
	path := CapSIDFile(codexHome)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		caps, loaded, err := parseCapabilitySIDs(data)
		if err != nil {
			return nil, err
		}
		if loaded {
			ensureCapabilitySIDMaps(caps)
			if !isJSONObject(bytesTrimSpace(data)) {
				if err := persistCapabilitySIDs(path, caps); err != nil {
					return nil, err
				}
			}
			return caps, nil
		}
	case os.IsNotExist(err):
	default:
		return nil, err
	}

	caps, err := newCapabilitySIDs()
	if err != nil {
		return nil, err
	}
	if err := persistCapabilitySIDs(path, caps); err != nil {
		return nil, err
	}
	return caps, nil
}

func WorkspaceCapabilitySIDForCWD(codexHome string, cwd string) (string, error) {
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		return "", err
	}
	key := CanonicalPathKey(cwd)
	if sid := caps.WorkspaceByCWD[key]; sid != "" {
		return sid, nil
	}
	sid, err := makeRandomCapabilitySIDString()
	if err != nil {
		return "", err
	}
	caps.WorkspaceByCWD[key] = sid
	if err := persistCapabilitySIDs(CapSIDFile(codexHome), caps); err != nil {
		return "", err
	}
	return sid, nil
}

func WorkspaceWriteCapabilitySIDForRoot(codexHome string, root string) (string, error) {
	return WritableRootCapabilitySIDForPath(codexHome, root)
}

func WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome string, cwd string, root string) (string, error) {
	if CanonicalPathKey(cwd) == CanonicalPathKey(root) {
		return WorkspaceCapabilitySIDForCWD(codexHome, cwd)
	}
	return WritableRootCapabilitySIDForPath(codexHome, root)
}

func WritableRootCapabilitySIDForPath(codexHome string, root string) (string, error) {
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		return "", err
	}
	key := CanonicalPathKey(root)
	if sid := caps.WritableRootByPath[key]; sid != "" {
		return sid, nil
	}
	sid, err := makeRandomCapabilitySIDString()
	if err != nil {
		return "", err
	}
	caps.WritableRootByPath[key] = sid
	if err := persistCapabilitySIDs(CapSIDFile(codexHome), caps); err != nil {
		return "", err
	}
	return sid, nil
}

func WorkspaceWriteRootContainsPath(root string, path string) bool {
	rootKey := stringsTrimTrailingSlash(CanonicalPathKey(root))
	pathKey := stringsTrimTrailingSlash(CanonicalPathKey(path))
	return pathKey == rootKey || hasPathPrefix(pathKey, rootKey)
}

func WorkspaceWriteRootOverlapsPath(root string, path string) bool {
	return WorkspaceWriteRootContainsPath(root, path) || WorkspaceWriteRootContainsPath(path, root)
}

func WorkspaceWriteRootSpecificity(root string) int {
	key := stringsTrimTrailingSlash(CanonicalPathKey(root))
	if key == "" {
		return 0
	}
	return len(splitCanonicalPathKey(key))
}

func parseCapabilitySIDs(data []byte) (*CapabilitySIDs, bool, error) {
	trimmed := string(bytesTrimSpace(data))
	if trimmed == "" {
		return nil, false, nil
	}
	if isJSONObject([]byte(trimmed)) {
		var caps CapabilitySIDs
		if err := json.Unmarshal([]byte(trimmed), &caps); err != nil {
			return nil, false, err
		}
		ensureCapabilitySIDMaps(&caps)
		return &caps, true, nil
	}
	readonly, err := makeRandomCapabilitySIDString()
	if err != nil {
		return nil, false, err
	}
	caps := &CapabilitySIDs{
		Workspace:          trimmed,
		Readonly:           readonly,
		WorkspaceByCWD:     map[string]string{},
		WritableRootByPath: map[string]string{},
	}
	return caps, true, nil
}

func isJSONObject(data []byte) bool {
	return len(data) >= 2 && data[0] == '{' && data[len(data)-1] == '}'
}

func newCapabilitySIDs() (*CapabilitySIDs, error) {
	workspace, err := makeRandomCapabilitySIDString()
	if err != nil {
		return nil, err
	}
	readonly, err := makeRandomCapabilitySIDString()
	if err != nil {
		return nil, err
	}
	return &CapabilitySIDs{
		Workspace:          workspace,
		Readonly:           readonly,
		WorkspaceByCWD:     map[string]string{},
		WritableRootByPath: map[string]string{},
	}, nil
}

func persistCapabilitySIDs(path string, caps *CapabilitySIDs) error {
	if caps == nil {
		return ErrInvalidRequest
	}
	ensureCapabilitySIDMaps(caps)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(caps)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ensureCapabilitySIDMaps(caps *CapabilitySIDs) {
	if caps.WorkspaceByCWD == nil {
		caps.WorkspaceByCWD = map[string]string{}
	}
	if caps.WritableRootByPath == nil {
		caps.WritableRootByPath = map[string]string{}
	}
}

func makeRandomCapabilitySIDString() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	parts := [4]uint32{
		binary.LittleEndian.Uint32(id[0:4]),
		binary.LittleEndian.Uint32(id[4:8]),
		binary.LittleEndian.Uint32(id[8:12]),
		binary.LittleEndian.Uint32(id[12:16]),
	}
	return fmt.Sprintf("S-1-5-21-%d-%d-%d-%d", parts[0], parts[1], parts[2], parts[3]), nil
}

func bytesTrimSpace(data []byte) []byte {
	for len(data) > 0 && isASCIIWhitespace(data[0]) {
		data = data[1:]
	}
	for len(data) > 0 && isASCIIWhitespace(data[len(data)-1]) {
		data = data[:len(data)-1]
	}
	return data
}

func isASCIIWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func stringsTrimTrailingSlash(value string) string {
	for len(value) > 1 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}

func hasPathPrefix(pathKey string, rootKey string) bool {
	if rootKey == "" || pathKey == rootKey {
		return pathKey == rootKey
	}
	return len(pathKey) > len(rootKey) && pathKey[:len(rootKey)] == rootKey && pathKey[len(rootKey)] == '/'
}

func splitCanonicalPathKey(key string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] != '/' {
			continue
		}
		if i > start {
			parts = append(parts, key[start:i])
		}
		start = i + 1
	}
	if start < len(key) {
		parts = append(parts, key[start:])
	}
	return parts
}
