package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/gofrs/flock"
)

const (
	OpenAIAPIKeyEnv     = "OPENAI_API_KEY"
	CodexAPIKeyEnv      = "CODEX_API_KEY"
	CodexAccessTokenEnv = "CODEX_ACCESS_TOKEN"
)

type AuthDotJSON struct {
	AuthMode            string         `json:"auth_mode,omitempty"`
	OpenAIAPIKey        string         `json:"OPENAI_API_KEY,omitempty"`
	Tokens              map[string]any `json:"tokens,omitempty"`
	LastRefresh         string         `json:"last_refresh,omitempty"`
	AgentIdentity       any            `json:"agent_identity,omitempty"`
	PersonalAccessToken string         `json:"personal_access_token,omitempty"`
	BedrockAPIKey       any            `json:"bedrock_api_key,omitempty"`
	BedrockAccessKeys   any            `json:"bedrock_access_keys,omitempty"`
}

type BedrockAPIKeyAuth struct {
	APIKey string `json:"api_key"`
	Region string `json:"region"`
}

// BedrockAccessKeysAuth is the managed AWS access-key credential stored for
// Amazon Bedrock login (Rust #40481).
type BedrockAccessKeysAuth struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
	Region          string `json:"region"`
}

// Format redacts both AWS secrets from derived debug formatting.
func (a *BedrockAccessKeysAuth) Format(f fmt.State, verb rune) {
	if a == nil {
		fmt.Fprint(f, "<nil>")
		return
	}
	const redacted = "<redacted>"
	switch verb {
	case '#':
		fmt.Fprintf(f, "auth.BedrockAccessKeysAuth{AccessKeyID:%q, SecretAccessKey:%q, SessionToken:%q, Region:%q}",
			redacted, redacted, redacted, a.Region)
	case 'v':
		if f.Flag('+') {
			fmt.Fprintf(f, "{AccessKeyID:%s SecretAccessKey:%s SessionToken:%s Region:%s}",
				redacted, redacted, redacted, a.Region)
		} else {
			fmt.Fprintf(f, "{%s %s %s %s}", redacted, redacted, redacted, a.Region)
		}
	default:
		fmt.Fprintf(f, "{%s %s %s %s}", redacted, redacted, redacted, a.Region)
	}
}

// Format redacts the managed Amazon Bedrock API key from derived debug
// formatting while retaining the region, mirroring Rust #40706 custom Debug
// for BedrockApiKeyAuth. Applying fmt.Formatter keeps every %v / %+v / %#v
// path from leaking the secret.
func (a *BedrockAPIKeyAuth) Format(f fmt.State, verb rune) {
	if a == nil {
		fmt.Fprint(f, "<nil>")
		return
	}
	const redacted = "<redacted>"
	switch verb {
	case '#':
		fmt.Fprintf(f, "auth.BedrockAPIKeyAuth{APIKey:%q, Region:%q}", redacted, a.Region)
	case 'v':
		if f.Flag('+') {
			fmt.Fprintf(f, "{APIKey:%s Region:%s}", redacted, a.Region)
		} else {
			fmt.Fprintf(f, "{%s %s}", redacted, a.Region)
		}
	default:
		fmt.Fprintf(f, "{APIKey:%s Region:%s}", redacted, a.Region)
	}
}

type AgentIdentityAuthRecord struct {
	AgentRuntimeID        string   `json:"agent_runtime_id"`
	AgentPrivateKey       string   `json:"agent_private_key"`
	AccountID             string   `json:"account_id"`
	ChatGPTUserID         string   `json:"chatgpt_user_id"`
	Email                 *string  `json:"email"`
	PlanType              PlanType `json:"plan_type"`
	ChatGPTAccountFedRAMP bool     `json:"chatgpt_account_is_fedramp"`
	TaskID                *string  `json:"task_id,omitempty"`
}

type Store struct {
	CodexHome string
	options   StoreOptions
}

type ResolvedAuth struct {
	Auth   AuthDotJSON
	Source string
}

type AuthCredentialsStoreMode string

const (
	AuthCredentialsStoreFile      AuthCredentialsStoreMode = "file"
	AuthCredentialsStoreKeyring   AuthCredentialsStoreMode = "keyring"
	AuthCredentialsStoreAuto      AuthCredentialsStoreMode = "auto"
	AuthCredentialsStoreEphemeral AuthCredentialsStoreMode = "ephemeral"
)

const authKeyringService = "Codex Auth"

type StoreOptions struct {
	Mode           AuthCredentialsStoreMode
	KeyringBackend KeyringBackendKind
	KeyringStore   *KeyringStore
	// Originator is the bounded client attribution carried by credential
	// storage observations (Rust AuthStorageOriginator).
	Originator string
	// WorkloadIdentity, when set, configures process-scoped workload identity
	// authentication selected from OPENAI_FEDERATION_RULE_ID /
	// OPENAI_IDENTITY_TOKEN_FILE (Rust 96c8be200c, #38188).
	WorkloadIdentity *WorkloadIdentityAuthOptions
}

type authStorageBackend interface {
	Load() (*AuthDotJSON, error)
	// LoadAccountGrant loads the credential for an account grant check, where an
	// unavailable secure store is an error instead of a fallback (Rust #55546).
	LoadAccountGrant() (*AuthDotJSON, error)
	Save(auth AuthDotJSON) error
	Delete() (bool, error)
	Source() string
}

func NewStore(codexHome string) *Store {
	return NewStoreWithOptions(codexHome, nil)
}

func NewStoreWithOptions(codexHome string, options *StoreOptions) *Store {
	store := &Store{CodexHome: codexHome}
	if options != nil {
		store.options = *options
	}
	if store.options.Mode == "" {
		store.options.Mode = AuthCredentialsStoreFile
	}
	if store.options.KeyringBackend == "" || store.options.KeyringBackend == KeyringBackendAuto {
		store.options.KeyringBackend = KeyringBackendDirect
	}
	store.options.Originator = normalizeStorageOriginator(store.options.Originator)
	return store
}

func StoreOptionsFromConfig(mode string, secretAuthStorageEnabled bool) *StoreOptions {
	storeMode := AuthCredentialsStoreMode(strings.TrimSpace(mode))
	switch storeMode {
	case "", AuthCredentialsStoreFile:
		storeMode = AuthCredentialsStoreFile
	case AuthCredentialsStoreKeyring, AuthCredentialsStoreAuto, AuthCredentialsStoreEphemeral:
	default:
		storeMode = AuthCredentialsStoreFile
	}
	return &StoreOptions{
		Mode:           storeMode,
		KeyringBackend: ResolveKeyringBackendFromSecretAuthStorage(secretAuthStorageEnabled),
	}
}

func DefaultCodexHome() string {
	if value := strings.TrimSpace(os.Getenv("GCODE_HOME")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".gcode"
	}
	return filepath.Join(home, ".gcode")
}

func (s *Store) Path() string {
	return filepath.Join(s.CodexHome, "auth.json")
}

// authStorageCommitMu serializes in-process auth storage reads and writes that
// participate in credential updates. It never spans a network request
// (Rust #55646 AUTH_STORAGE_COMMIT).
var authStorageCommitMu sync.Mutex

// authWriteGuard serializes a read/modify/write across every manager in this
// process and every cooperating process holding <codex home>/auth.lock
// (Rust #57514 AuthStorageBackend::lock_for_write). Ephemeral stores have no
// persistent home and therefore only take the process-wide mutex.
type authWriteGuard struct {
	lock *flock.Flock
}

func (s *Store) lockForWrite() (*authWriteGuard, error) {
	authStorageCommitMu.Lock()
	if s == nil || s.options.Mode == AuthCredentialsStoreEphemeral {
		return &authWriteGuard{}, nil
	}
	lock := flock.New(filepath.Join(s.CodexHome, "auth.lock"))
	if err := lock.Lock(); err != nil {
		authStorageCommitMu.Unlock()
		return nil, fmt.Errorf("failed to lock auth credentials: %w", err)
	}
	return &authWriteGuard{lock: lock}, nil
}

func (g *authWriteGuard) release() {
	if g == nil {
		return
	}
	if g.lock != nil {
		_ = g.lock.Unlock()
		g.lock = nil
	}
	authStorageCommitMu.Unlock()
}

// Load reads the stored credential.
func (s *Store) Load() (*AuthDotJSON, error) {
	authStorageCommitMu.Lock()
	defer authStorageCommitMu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (auth *AuthDotJSON, err error) {
	backend := s.backend()
	if _, ok := backend.(*autoAuthStorage); ok {
		return backend.Load()
	}
	attempt := newStorageAttempt(s.options.Mode, s.options.KeyringBackend, "load", s.options.Originator)
	defer attempt.finish()
	auth, err = backend.Load()
	attempt.recordLoadAttempt(storageTelemetryStoreName(backend), auth, err)
	if s.options.Mode == AuthCredentialsStoreKeyring && err != nil {
		attempt.recordSecureError(err)
	}
	return auth, err
}

func (s *Store) Resolve() (*ResolvedAuth, error) {
	// Workload identity markers are an explicit authentication selection, so
	// explicit env keys only take precedence when no marker is present. With a
	// marker configured, resolveWorkloadIdentity fails closed on invalid or
	// partial configuration (Rust #38424).
	if !workloadReadProcessEnv().hasMarker() {
		if apiKey := readNonEmptyEnv(OpenAIAPIKeyEnv); apiKey != "" {
			return &ResolvedAuth{Auth: FromAPIKey(apiKey), Source: OpenAIAPIKeyEnv}, nil
		}
		if apiKey := readNonEmptyEnv(CodexAPIKeyEnv); apiKey != "" {
			return &ResolvedAuth{Auth: FromAPIKey(apiKey), Source: CodexAPIKeyEnv}, nil
		}
		if accessToken := readNonEmptyEnv(CodexAccessTokenEnv); accessToken != "" {
			return &ResolvedAuth{Auth: FromCodexAccessToken(accessToken), Source: CodexAccessTokenEnv}, nil
		}
	}
	if resolved, err := s.resolveWorkloadIdentity(); err != nil {
		return nil, err
	} else if resolved != nil {
		return resolved, nil
	}
	authStorageCommitMu.Lock()
	loaded, err := s.loadLocked()
	authStorageCommitMu.Unlock()
	if err != nil || loaded == nil {
		return nil, err
	}
	return &ResolvedAuth{Auth: *loaded, Source: s.backend().Source()}, nil
}

// resolveWorkloadIdentity resolves process-configured workload identity when
// selected, failing closed on incomplete, conflicting, or unsupported
// configurations (Rust shared_from_auth_config).
func (s *Store) resolveWorkloadIdentity() (*ResolvedAuth, error) {
	env := workloadReadProcessEnv()
	if !env.hasMarker() {
		return nil, nil
	}
	opts := s.options.WorkloadIdentity
	config, err := resolveWorkloadIdentityConfig(
		opts.baseURL(),
		env,
		opts.chatgptLoginAllowed(),
	)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, nil
	}
	config.httpClient = opts.httpClient()
	session, err := workloadIdentityProcessRegistry.session(*config)
	if err != nil {
		return nil, err
	}
	auth, err := (&WorkloadIdentityAuth{session: session}).ResolveAuth(context.Background())
	if err != nil {
		return nil, err
	}
	return &ResolvedAuth{Auth: *auth, Source: WorkloadIdentitySource}, nil
}

// Save writes the credential atomically with other in-process commits.
func (s *Store) Save(auth AuthDotJSON) error {
	guard, err := s.lockForWrite()
	if err != nil {
		return err
	}
	defer guard.release()
	commit, err := s.lockCommit()
	if err != nil {
		return err
	}
	defer commit.release()
	return s.saveLocked(auth)
}

// ReplaceIfCurrent writes updated only while the stored account grant is still
// expected. The comparison and write happen under one write lock so a
// concurrent login or logout cannot be overwritten (Rust #57514
// replace_if_current).
func (s *Store) ReplaceIfCurrent(expected *AuthDotJSON, updated AuthDotJSON) error {
	guard, err := s.lockForWrite()
	if err != nil {
		return err
	}
	defer guard.release()
	commit, err := s.lockCommit()
	if err != nil {
		return err
	}
	defer commit.release()
	current, err := s.backend().LoadAccountGrant()
	if err != nil {
		return err
	}
	if !authDotJSONEqual(current, expected) {
		return errAccountCredentialsChanged
	}
	return s.saveLocked(updated)
}

// errAccountCredentialsChanged reports a conditional credential write that lost
// a race with another auth operation.
var errAccountCredentialsChanged = errors.New("Account credentials changed")

// IsAccountCredentialsChanged reports whether err is the conditional-write
// conflict raised by ReplaceIfCurrent.
func IsAccountCredentialsChanged(err error) bool {
	return errors.Is(err, errAccountCredentialsChanged)
}

func authDotJSONEqual(left *AuthDotJSON, right *AuthDotJSON) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.DeepEqual(*left, *right)
}

func (s *Store) saveLocked(auth AuthDotJSON) (err error) {
	backend := s.backend()
	if _, ok := backend.(*autoAuthStorage); ok {
		if err = backend.Save(auth); err != nil {
			return err
		}
		ClearPermanentRefreshFailure(s.CodexHome)
		return nil
	}
	attempt := newStorageAttempt(s.options.Mode, s.options.KeyringBackend, "save", s.options.Originator)
	defer attempt.finish()
	err = backend.Save(auth)
	attempt.recordSaveAttempt(storageTelemetryStoreName(backend), err)
	if s.options.Mode == AuthCredentialsStoreKeyring && err != nil {
		attempt.recordSecureError(err)
	}
	if err != nil {
		return err
	}
	ClearPermanentRefreshFailure(s.CodexHome)
	return nil
}

// saveRefreshedFallbackLocked persists rotated fallback tokens to the file
// store when the keyring could not be read, leaving a keyring generation whose
// contents are unknown untouched (Rust #58343 AutoAuthStorage::compare_and_save).
// Ordinary saves still prefer the keyring.
func (s *Store) saveRefreshedFallbackLocked(auth AuthDotJSON, keyringErr error) error {
	auto, ok := s.backend().(*autoAuthStorage)
	if !ok {
		return s.saveLocked(auth)
	}
	attempt := newStorageAttempt(AuthCredentialsStoreAuto, auto.keyring.backend, "save", auto.originator)
	defer attempt.finish()
	attempt.recordLoadAttempt(auto.keyring.telemetryStoreName(), nil, keyringErr)
	attempt.recordSecureError(keyringErr)
	err := auto.file.Save(auth)
	attempt.recordSaveAttempt("file", err)
	if err != nil {
		return err
	}
	ClearPermanentRefreshFailure(s.CodexHome)
	return nil
}

// Delete removes the stored credential atomically with other in-process
// commits.
func (s *Store) Delete() (bool, error) {
	// A missing file-store home contains no credentials to delete; never create
	// one just to leave a lock behind (Rust #58251).
	if s.options.Mode == AuthCredentialsStoreFile {
		if _, err := os.Stat(s.CodexHome); errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
	}
	guard, err := s.lockForWrite()
	if err != nil {
		return false, err
	}
	defer guard.release()
	commit, err := s.lockCommit()
	if err != nil {
		return false, err
	}
	defer commit.release()
	return s.deleteLocked()
}

func (s *Store) deleteLocked() (removed bool, err error) {
	backend := s.backend()
	if _, ok := backend.(*autoAuthStorage); ok {
		removed, err = backend.Delete()
		if err != nil {
			return false, err
		}
		ClearPermanentRefreshFailure(s.CodexHome)
		return removed, nil
	}
	attempt := newStorageAttempt(s.options.Mode, s.options.KeyringBackend, "delete", s.options.Originator)
	defer attempt.finish()
	removed, err = backend.Delete()
	actualStore := storageTelemetryStoreName(backend)
	if isSecureStorageStore(actualStore) {
		// The keyring backend also removes the plaintext fallback, so the
		// logical operation touches more than one store (Rust Store::Multiple)
		// and never attributes a secure outcome to a delete.
		actualStore = "multiple"
	}
	attempt.recordDeleteAttempt(actualStore, removed, err)
	if err != nil {
		return false, err
	}
	ClearPermanentRefreshFailure(s.CodexHome)
	return removed, nil
}

// LoadAccountGrant reads the credential for an identity-provider exchange.
// Unlike ordinary login loading, an unavailable keyring must not authorize an
// exchange using an older file fallback (Rust #55546/#55646).
func (s *Store) LoadAccountGrant() (*AuthDotJSON, error) {
	guard, err := s.lockForWrite()
	if err != nil {
		return nil, err
	}
	defer guard.release()
	return s.backend().LoadAccountGrant()
}

func (s *Store) backend() authStorageBackend {
	mode := s.options.Mode
	switch mode {
	case AuthCredentialsStoreKeyring:
		return newKeyringAuthStorage(s.CodexHome, s.options.KeyringBackend, s.options.KeyringStore, mode, s.options.Originator)
	case AuthCredentialsStoreAuto:
		keyringStorage := newKeyringAuthStorage(s.CodexHome, s.options.KeyringBackend, s.options.KeyringStore, mode, s.options.Originator)
		return &autoAuthStorage{
			keyring:    keyringStorage,
			file:       &fileAuthStorage{store: s},
			originator: s.options.Originator,
		}
	case AuthCredentialsStoreEphemeral:
		return &ephemeralAuthStorage{codexHome: s.CodexHome}
	default:
		return &fileAuthStorage{store: s}
	}
}

type fileAuthStorage struct {
	store *Store
}

func (s *fileAuthStorage) Load() (*AuthDotJSON, error) {
	data, err := readAuthFile(s.store.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeStoredAuthDotJSON("failed to deserialize CLI auth from file", data)
}

func (s *fileAuthStorage) LoadAccountGrant() (*AuthDotJSON, error) {
	return s.Load()
}

func (s *fileAuthStorage) Save(auth AuthDotJSON) error {
	if err := os.MkdirAll(s.store.CodexHome, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return writeAuthFileAtomically(s.store.Path(), append(data, '\n'))
}

func (s *fileAuthStorage) Delete() (bool, error) {
	err := os.Remove(s.store.Path())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *fileAuthStorage) Source() string {
	return "auth.json"
}

type keyringAuthStorage struct {
	codexHome  string
	keyring    *KeyringStore
	backend    KeyringBackendKind
	mode       AuthCredentialsStoreMode
	originator string
	// available reports whether the keyring can durably hold credentials: an
	// explicitly injected store is the test seam, otherwise this build has no OS
	// keyring.
	available bool
}

func newKeyringAuthStorage(codexHome string, backend KeyringBackendKind, store *KeyringStore, mode AuthCredentialsStoreMode, originator string) *keyringAuthStorage {
	injected := store != nil
	if store == nil {
		if OSKeyringAvailable {
			store = NewOSKeyringStore(backend)
		} else {
			store = NewKeyringStore(backend)
		}
	}
	return &keyringAuthStorage{
		codexHome:  codexHome,
		keyring:    store,
		backend:    backend,
		mode:       mode,
		originator: originator,
		available:  injected || OSKeyringAvailable,
	}
}

// keyringUnavailable reports the Rust "keyring when available, otherwise fail"
// outcome for this storage.
func (s *keyringAuthStorage) keyringUnavailable() error {
	if s == nil || s.available {
		return nil
	}
	return ErrKeyringUnavailable
}

func (s *keyringAuthStorage) Load() (*AuthDotJSON, error) {
	if err := s.keyringUnavailable(); err != nil {
		return nil, err
	}
	value, err := s.keyring.Get(authKeyringService, authStoreKey(s.codexHome))
	if errors.Is(err, ErrKeyringSecretNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, credentialStorageContext("failed to load CLI auth from keyring", err)
	}
	return decodeStoredAuthDotJSON("failed to deserialize CLI auth from keyring", []byte(value))
}

func (s *keyringAuthStorage) LoadAccountGrant() (*AuthDotJSON, error) {
	return s.Load()
}

func (s *keyringAuthStorage) Save(auth AuthDotJSON) error {
	if err := s.keyringUnavailable(); err != nil {
		return err
	}
	data, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	if err := s.keyring.Set(authKeyringService, authStoreKey(s.codexHome), string(data)); err != nil {
		err = credentialStorageContext("failed to write OAuth tokens to keyring", err)
		if s.mode == AuthCredentialsStoreKeyring {
			slog.Warn(err.Error())
		}
		return err
	}
	attempt := newStorageAttempt(s.mode, s.backend, "cleanup", s.originator)
	removed, cleanupErr := (&fileAuthStorage{store: NewStore(s.codexHome)}).Delete()
	attempt.recordDeleteAttempt("file", removed, cleanupErr)
	attempt.finish()
	if cleanupErr != nil {
		slog.Warn("failed to remove CLI auth fallback file", "error", cleanupErr)
	}
	return nil
}

func (s *keyringAuthStorage) Delete() (bool, error) {
	if err := s.keyringUnavailable(); err != nil {
		return false, err
	}
	keyringRemoved, err := s.keyring.Delete(authKeyringService, authStoreKey(s.codexHome))
	if err != nil {
		return false, credentialStorageContext("failed to delete auth from keyring", err)
	}
	fileRemoved, err := (&fileAuthStorage{store: NewStore(s.codexHome)}).Delete()
	if err != nil {
		return false, err
	}
	return keyringRemoved || fileRemoved, nil
}

func (s *keyringAuthStorage) Source() string {
	return "keyring"
}

func (s *keyringAuthStorage) telemetryStoreName() string {
	if s == nil {
		return "direct_keyring"
	}
	return keyringStoreName(s.backend)
}

type autoAuthStorage struct {
	keyring    *keyringAuthStorage
	file       authStorageBackend
	originator string
}

func (s *autoAuthStorage) Load() (*AuthDotJSON, error) {
	attempt := newStorageAttempt(AuthCredentialsStoreAuto, s.keyring.backend, "load", s.originator)
	defer attempt.finish()
	auth, err := s.keyring.Load()
	attempt.recordLoadAttempt(s.keyring.telemetryStoreName(), auth, err)
	if err == nil && auth != nil {
		return auth, nil
	}
	if err != nil {
		attempt.recordSecureError(err)
		slog.Warn("failed to load CLI auth from keyring, falling back to file storage", "error", err)
	}
	auth, err = s.file.Load()
	attempt.recordLoadAttempt("file", auth, err)
	return auth, err
}

// LoadAccountGrant propagates an unavailable keyring instead of falling back to
// an older file record, while ordinary Load keeps the fallback behavior.
func (s *autoAuthStorage) LoadAccountGrant() (*AuthDotJSON, error) {
	auth, err := s.keyring.Load()
	if err != nil {
		return nil, err
	}
	if auth != nil {
		return auth, nil
	}
	return s.file.Load()
}

func (s *autoAuthStorage) Save(auth AuthDotJSON) error {
	attempt := newStorageAttempt(AuthCredentialsStoreAuto, s.keyring.backend, "save", s.originator)
	defer attempt.finish()
	err := s.keyring.Save(auth)
	attempt.recordSaveAttempt(s.keyring.telemetryStoreName(), err)
	if err == nil {
		return nil
	}
	attempt.recordSecureError(err)
	slog.Warn("failed to save auth to keyring, falling back to file storage", "error", err)
	err = s.file.Save(auth)
	attempt.recordSaveAttempt("file", err)
	return err
}

func (s *autoAuthStorage) Delete() (bool, error) {
	attempt := newStorageAttempt(AuthCredentialsStoreAuto, s.keyring.backend, "delete", s.originator)
	defer attempt.finish()
	keyringRemoved, keyringErr := s.keyring.Delete()
	fileRemoved, fileErr := s.file.Delete()
	if keyringErr != nil && fileErr != nil {
		attempt.recordDeleteAttempt("multiple", false, keyringErr)
		return false, keyringErr
	}
	if fileErr != nil {
		attempt.recordDeleteAttempt("multiple", false, fileErr)
		return false, fileErr
	}
	if keyringErr != nil {
		attempt.recordDeleteAttempt("multiple", fileRemoved, nil)
		return fileRemoved, nil
	}
	attempt.recordDeleteAttempt("multiple", keyringRemoved || fileRemoved, nil)
	return keyringRemoved || fileRemoved, nil
}

func (s *autoAuthStorage) Source() string {
	return "auto"
}

func storageTelemetryStoreName(backend authStorageBackend) string {
	if named, ok := backend.(interface{ telemetryStoreName() string }); ok {
		return named.telemetryStoreName()
	}
	return authStorageBackendName(backend.Source())
}

var ephemeralAuthStore = struct {
	sync.Mutex
	values map[string]AuthDotJSON
}{values: map[string]AuthDotJSON{}}

type ephemeralAuthStorage struct {
	codexHome string
}

func (s *ephemeralAuthStorage) Load() (*AuthDotJSON, error) {
	ephemeralAuthStore.Lock()
	defer ephemeralAuthStore.Unlock()
	auth, ok := ephemeralAuthStore.values[authStoreKey(s.codexHome)]
	if !ok {
		return nil, nil
	}
	return cloneAuthDotJSON(&auth), nil
}

func (s *ephemeralAuthStorage) LoadAccountGrant() (*AuthDotJSON, error) {
	return s.Load()
}

func (s *ephemeralAuthStorage) Save(auth AuthDotJSON) error {
	ephemeralAuthStore.Lock()
	defer ephemeralAuthStore.Unlock()
	ephemeralAuthStore.values[authStoreKey(s.codexHome)] = *cloneAuthDotJSON(&auth)
	return nil
}

func (s *ephemeralAuthStorage) Delete() (bool, error) {
	ephemeralAuthStore.Lock()
	defer ephemeralAuthStore.Unlock()
	key := authStoreKey(s.codexHome)
	_, existed := ephemeralAuthStore.values[key]
	delete(ephemeralAuthStore.values, key)
	return existed, nil
}

func (s *ephemeralAuthStorage) Source() string {
	return "ephemeral"
}

func authStoreKey(codexHome string) string {
	cleaned := filepath.Clean(strings.TrimSpace(codexHome))
	if cleaned == "" {
		cleaned = "."
	}
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	sum := sha256.Sum256([]byte(cleaned))
	return fmt.Sprintf("cli|%x", sum[:8])
}

func (a *AuthDotJSON) Mode() string {
	if a == nil {
		return "unknown"
	}
	if mode := normalizedAuthMode(a.AuthMode); mode != "" {
		return mode
	}
	switch {
	case strings.TrimSpace(a.OpenAIAPIKey) != "":
		return "api-key"
	case strings.TrimSpace(a.PersonalAccessToken) != "":
		return "personal-access-token"
	case a.AgentIdentity != nil:
		return "agent-identity"
	case a.BedrockAPIKey != nil:
		return "bedrock-api-key"
	case a.BedrockAccessKeys != nil:
		return "bedrock-access-keys"
	case a.Tokens != nil:
		return "chatgpt"
	default:
		return "unknown"
	}
}

func normalizedAuthMode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	compact := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(value))
	switch compact {
	case "apikey":
		return "api-key"
	case "chatgpt":
		return "chatgpt"
	case "chatgptauthtokens":
		return "chatgptAuthTokens"
	case "agentidentity":
		return "agent-identity"
	case "personalaccesstoken":
		return "personal-access-token"
	case "bedrockapikey":
		return "bedrock-api-key"
	case "bedrockaccesskeys":
		return "bedrock-access-keys"
	case "unknown":
		return "unknown"
	default:
		return value
	}
}

func (a *AuthDotJSON) BackendMode() string {
	switch a.Mode() {
	case "chatgptAuthTokens":
		return "chatgpt"
	default:
		return a.Mode()
	}
}

func FromAPIKey(apiKey string) AuthDotJSON {
	return AuthDotJSON{
		AuthMode:     "apikey",
		OpenAIAPIKey: strings.TrimSpace(apiKey),
	}
}

func FromAccessToken(accessToken string) AuthDotJSON {
	return AuthDotJSON{
		PersonalAccessToken: strings.TrimSpace(accessToken),
	}
}

func FromCodexAccessToken(accessToken string) AuthDotJSON {
	token := strings.TrimSpace(accessToken)
	if strings.HasPrefix(token, "at-") {
		return FromAccessToken(token)
	}
	return FromAgentIdentityToken(token)
}

func FromPersonalAccessToken(accessToken string, metadata *PersonalAccessTokenMetadata) AuthDotJSON {
	auth := FromAccessToken(accessToken)
	if metadata == nil {
		return auth
	}
	auth.Tokens = map[string]any{
		"chatgpt_user_id":            strings.TrimSpace(metadata.ChatGPTUserID),
		"chatgpt_account_id":         strings.TrimSpace(metadata.ChatGPTAccountID),
		"chatgpt_plan_type":          strings.TrimSpace(metadata.ChatGPTPlanType),
		"chatgpt_account_is_fedramp": metadata.ChatGPTAccountFedRAMP,
	}
	if metadata.Email != nil && strings.TrimSpace(*metadata.Email) != "" {
		auth.Tokens["email"] = strings.TrimSpace(*metadata.Email)
	}
	return auth
}

func FromAgentIdentityToken(accessToken string) AuthDotJSON {
	return AuthDotJSON{
		AuthMode:      "agentIdentity",
		AgentIdentity: strings.TrimSpace(accessToken),
	}
}

func FromBedrockAPIKey(apiKey string, region string) AuthDotJSON {
	return AuthDotJSON{
		AuthMode: "bedrockApiKey",
		BedrockAPIKey: &BedrockAPIKeyAuth{
			APIKey: strings.TrimSpace(apiKey),
			Region: strings.TrimSpace(region),
		},
	}
}

func FromBedrockAccessKeys(accessKeyID string, secretAccessKey string, sessionToken string, region string) AuthDotJSON {
	return AuthDotJSON{
		AuthMode: "bedrock-access-keys",
		BedrockAccessKeys: &BedrockAccessKeysAuth{
			AccessKeyID:     strings.TrimSpace(accessKeyID),
			SecretAccessKey: strings.TrimSpace(secretAccessKey),
			SessionToken:    strings.TrimSpace(sessionToken),
			Region:          strings.TrimSpace(region),
		},
	}
}

func FromChatGPTPlaceholder(method string) AuthDotJSON {
	method = strings.TrimSpace(method)
	if method == "" {
		method = "browser"
	}
	return AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"source":  method,
			"offline": true,
		},
	}
}

func FromChatGPTTokens(idToken string, accessToken string, refreshToken string) AuthDotJSON {
	auth := AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":      strings.TrimSpace(idToken),
			"access_token":  strings.TrimSpace(accessToken),
			"refresh_token": strings.TrimSpace(refreshToken),
		},
	}
	if accountID := ChatGPTAccountIDFromJWT(idToken); accountID != "" {
		auth.Tokens["account_id"] = accountID
	}
	return auth
}

func FromChatGPTAuthTokens(accessToken string, accountID string, planType *string) AuthDotJSON {
	auth := AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens: map[string]any{
			"access_token": strings.TrimSpace(accessToken),
		},
	}
	claims := ChatGPTClaimsFromJWT(accessToken)
	if strings.TrimSpace(accountID) == "" {
		accountID = claims.AccountID
	}
	if strings.TrimSpace(accountID) != "" {
		auth.Tokens["account_id"] = strings.TrimSpace(accountID)
		auth.Tokens["chatgpt_account_id"] = strings.TrimSpace(accountID)
	}
	if strings.TrimSpace(claims.Email) != "" {
		auth.Tokens["email"] = strings.TrimSpace(claims.Email)
	}
	plan := ""
	if planType != nil {
		plan = strings.TrimSpace(*planType)
	}
	if plan == "" {
		plan = strings.TrimSpace(claims.PlanType)
	}
	if plan != "" {
		auth.Tokens["plan_type"] = plan
		auth.Tokens["chatgpt_plan_type"] = plan
	}
	if claims.FedRAMP {
		auth.Tokens["is_fedramp_account"] = true
	}
	return auth
}

func readNonEmptyEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func osEnvPresent(key string) bool {
	_, present := os.LookupEnv(key)
	return present
}

func SafeFormatSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) <= 8 {
		return "****"
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}
