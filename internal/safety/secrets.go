package safety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrInvalidSecret = errors.New("invalid secret")

type SecretName struct {
	value string
}

func NewSecretName(raw string) (*SecretName, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: secret name must not be empty", ErrInvalidSecret)
	}
	for _, char := range trimmed {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			continue
		}
		return nil, fmt.Errorf("%w: secret name must contain only A-Z, 0-9, or _", ErrInvalidSecret)
	}
	return &SecretName{value: trimmed}, nil
}

func (n *SecretName) String() string {
	if n == nil {
		return ""
	}
	return n.value
}

type SecretScopeKind string

const (
	SecretScopeGlobal      SecretScopeKind = "global"
	SecretScopeEnvironment SecretScopeKind = "environment"
)

type SecretScope struct {
	Kind          SecretScopeKind `json:"kind"`
	EnvironmentID string          `json:"environmentId,omitempty"`
}

func GlobalScope() SecretScope {
	return SecretScope{Kind: SecretScopeGlobal}
}

func EnvironmentScope(environmentID string) (SecretScope, error) {
	trimmed := strings.TrimSpace(environmentID)
	if trimmed == "" {
		return SecretScope{}, fmt.Errorf("%w: environment id must not be empty", ErrInvalidSecret)
	}
	return SecretScope{Kind: SecretScopeEnvironment, EnvironmentID: trimmed}, nil
}

func (s *SecretScope) CanonicalKey(name *SecretName) string {
	if s == nil || s.Kind == SecretScopeGlobal {
		return "global/" + name.String()
	}
	return "env/" + s.EnvironmentID + "/" + name.String()
}

type SecretListEntry struct {
	Scope SecretScope `json:"scope"`
	Name  SecretName  `json:"name"`
}

type SecretBackendKind string

const SecretBackendLocal SecretBackendKind = "local"

type SecretManager struct {
	backend SecretBackend
}

type SecretBackend interface {
	Set(scope *SecretScope, name *SecretName, value string) error
	Get(scope *SecretScope, name *SecretName) (string, bool, error)
	Delete(scope *SecretScope, name *SecretName) (bool, error)
	List(scopeFilter *SecretScope) ([]SecretListEntry, error)
}

func NewSecretManager(codexHome string, kind SecretBackendKind) *SecretManager {
	switch kind {
	case "", SecretBackendLocal:
		return &SecretManager{backend: NewLocalSecretBackend(codexHome, LocalNamespaceManaged)}
	default:
		return &SecretManager{backend: NewLocalSecretBackend(codexHome, LocalNamespaceManaged)}
	}
}

func NewSecretManagerWithBackend(backend SecretBackend) *SecretManager {
	return &SecretManager{backend: backend}
}

func (m *SecretManager) Set(scope *SecretScope, name *SecretName, value string) error {
	if m == nil || m.backend == nil {
		return fmt.Errorf("%w: backend is nil", ErrInvalidSecret)
	}
	return m.backend.Set(scope, name, value)
}

func (m *SecretManager) Get(scope *SecretScope, name *SecretName) (string, bool, error) {
	if m == nil || m.backend == nil {
		return "", false, fmt.Errorf("%w: backend is nil", ErrInvalidSecret)
	}
	return m.backend.Get(scope, name)
}

func (m *SecretManager) Delete(scope *SecretScope, name *SecretName) (bool, error) {
	if m == nil || m.backend == nil {
		return false, fmt.Errorf("%w: backend is nil", ErrInvalidSecret)
	}
	return m.backend.Delete(scope, name)
}

func (m *SecretManager) List(scopeFilter *SecretScope) ([]SecretListEntry, error) {
	if m == nil || m.backend == nil {
		return nil, fmt.Errorf("%w: backend is nil", ErrInvalidSecret)
	}
	return m.backend.List(scopeFilter)
}

type LocalNamespace string

const (
	LocalNamespaceManaged   LocalNamespace = "local.json"
	LocalNamespaceCodexAuth LocalNamespace = "codex_auth.json"
	LocalNamespaceMCPOAuth  LocalNamespace = "mcp_oauth.json"
)

type LocalBackend struct {
	codexHome string
	namespace LocalNamespace
}

func NewLocalSecretBackend(codexHome string, namespace LocalNamespace) *LocalBackend {
	if namespace == "" {
		namespace = LocalNamespaceManaged
	}
	return &LocalBackend{codexHome: codexHome, namespace: namespace}
}

func (b *LocalBackend) Set(scope *SecretScope, name *SecretName, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: secret value must not be empty", ErrInvalidSecret)
	}
	file, err := b.load()
	if err != nil {
		return err
	}
	file.Secrets[scope.CanonicalKey(name)] = value
	return b.save(file)
}

func (b *LocalBackend) Get(scope *SecretScope, name *SecretName) (string, bool, error) {
	file, err := b.load()
	if err != nil {
		return "", false, err
	}
	value, ok := file.Secrets[scope.CanonicalKey(name)]
	return value, ok, nil
}

func (b *LocalBackend) Delete(scope *SecretScope, name *SecretName) (bool, error) {
	file, err := b.load()
	if err != nil {
		return false, err
	}
	key := scope.CanonicalKey(name)
	_, existed := file.Secrets[key]
	delete(file.Secrets, key)
	if existed {
		if err := b.save(file); err != nil {
			return false, err
		}
	}
	return existed, nil
}

func (b *LocalBackend) List(scopeFilter *SecretScope) ([]SecretListEntry, error) {
	file, err := b.load()
	if err != nil {
		return nil, err
	}
	entries := make([]SecretListEntry, 0, len(file.Secrets))
	for key := range file.Secrets {
		entry, ok := parseCanonicalKey(key)
		if !ok {
			continue
		}
		if scopeFilter != nil && entry.Scope != *scopeFilter {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i int, j int) bool {
		return entries[i].Scope.CanonicalKey(&entries[i].Name) < entries[j].Scope.CanonicalKey(&entries[j].Name)
	})
	return entries, nil
}

type localFile struct {
	Version int               `json:"version"`
	Secrets map[string]string `json:"secrets"`
}

func (b *LocalBackend) path() string {
	return filepath.Join(b.codexHome, "secrets", string(b.namespace))
}

func (b *LocalBackend) load() (*localFile, error) {
	path := b.path()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &localFile{Version: 1, Secrets: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var file localFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version == 0 {
		file.Version = 1
	}
	if file.Secrets == nil {
		file.Secrets = map[string]string{}
	}
	return &file, nil
}

func (b *LocalBackend) save(file *localFile) error {
	path := b.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func EnvironmentIDFromCWD(cwd string) string {
	abs := cwd
	if resolved, err := filepath.Abs(cwd); err == nil {
		abs = resolved
	}
	if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
		abs = evaluated
	}
	if base := strings.TrimSpace(filepath.Base(abs)); base != "" {
		return base
	}
	sum := sha256.Sum256([]byte(abs))
	return "cwd-" + hex.EncodeToString(sum[:])[:12]
}

func ComputeKeyringAccount(codexHome string) string {
	abs := codexHome
	if resolved, err := filepath.Abs(codexHome); err == nil {
		abs = resolved
	}
	if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
		abs = evaluated
	}
	sum := sha256.Sum256([]byte(abs))
	return "secrets|" + hex.EncodeToString(sum[:])[:16]
}

func KeyringService() string {
	return "codex"
}

func parseCanonicalKey(key string) (SecretListEntry, bool) {
	parts := strings.Split(key, "/")
	switch {
	case len(parts) == 2 && parts[0] == "global":
		name, err := NewSecretName(parts[1])
		if err != nil {
			return SecretListEntry{}, false
		}
		return SecretListEntry{Scope: GlobalScope(), Name: *name}, true
	case len(parts) == 3 && parts[0] == "env":
		scope, err := EnvironmentScope(parts[1])
		if err != nil {
			return SecretListEntry{}, false
		}
		name, err := NewSecretName(parts[2])
		if err != nil {
			return SecretListEntry{}, false
		}
		return SecretListEntry{Scope: scope, Name: *name}, true
	default:
		return SecretListEntry{}, false
	}
}
