package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
}

type BedrockAPIKeyAuth struct {
	APIKey string `json:"api_key"`
	Region string `json:"region"`
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
	// WorkloadIdentity, when set, configures process-scoped workload identity
	// authentication selected from OPENAI_FEDERATION_RULE_ID /
	// OPENAI_IDENTITY_TOKEN_FILE (Rust 96c8be200c, #38188).
	WorkloadIdentity *WorkloadIdentityAuthOptions
}

type authStorageBackend interface {
	Load() (*AuthDotJSON, error)
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
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func (s *Store) Path() string {
	return filepath.Join(s.CodexHome, "auth.json")
}

func (s *Store) Load() (*AuthDotJSON, error) {
	return s.backend().Load()
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
	backend := s.backend()
	loaded, err := backend.Load()
	if err != nil || loaded == nil {
		return nil, err
	}
	return &ResolvedAuth{Auth: *loaded, Source: backend.Source()}, nil
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

func (s *Store) Save(auth AuthDotJSON) error {
	if err := s.backend().Save(auth); err != nil {
		return err
	}
	ClearPermanentRefreshFailure(s.CodexHome)
	return nil
}

func (s *Store) Delete() (bool, error) {
	removed, err := s.backend().Delete()
	if err != nil {
		return false, err
	}
	ClearPermanentRefreshFailure(s.CodexHome)
	return removed, nil
}

func (s *Store) backend() authStorageBackend {
	mode := s.options.Mode
	switch mode {
	case AuthCredentialsStoreKeyring:
		return newKeyringAuthStorage(s.CodexHome, s.options.KeyringBackend, s.options.KeyringStore)
	case AuthCredentialsStoreAuto:
		return &autoAuthStorage{
			keyring: newKeyringAuthStorage(s.CodexHome, s.options.KeyringBackend, s.options.KeyringStore),
			file:    &fileAuthStorage{store: s},
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
	data, err := os.ReadFile(s.store.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var auth AuthDotJSON
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}

func (s *fileAuthStorage) Save(auth AuthDotJSON) error {
	if err := os.MkdirAll(s.store.CodexHome, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.store.Path(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	return nil
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
	codexHome string
	keyring   *KeyringStore
}

func newKeyringAuthStorage(codexHome string, backend KeyringBackendKind, store *KeyringStore) *keyringAuthStorage {
	if store == nil {
		store = NewKeyringStore(backend)
	}
	return &keyringAuthStorage{codexHome: codexHome, keyring: store}
}

func (s *keyringAuthStorage) Load() (*AuthDotJSON, error) {
	value, err := s.keyring.Get(authKeyringService, authStoreKey(s.codexHome))
	if errors.Is(err, ErrKeyringSecretNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load CLI auth from keyring: %w", err)
	}
	var auth AuthDotJSON
	if err := json.Unmarshal([]byte(value), &auth); err != nil {
		return nil, fmt.Errorf("failed to deserialize CLI auth from keyring: %w", err)
	}
	return &auth, nil
}

func (s *keyringAuthStorage) Save(auth AuthDotJSON) error {
	data, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	if err := s.keyring.Set(authKeyringService, authStoreKey(s.codexHome), string(data)); err != nil {
		return fmt.Errorf("failed to write OAuth tokens to keyring: %w", err)
	}
	if _, err := (&fileAuthStorage{store: NewStore(s.codexHome)}).Delete(); err != nil {
		return err
	}
	return nil
}

func (s *keyringAuthStorage) Delete() (bool, error) {
	keyringRemoved, err := s.keyring.Delete(authKeyringService, authStoreKey(s.codexHome))
	if err != nil {
		return false, fmt.Errorf("failed to delete auth from keyring: %w", err)
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

type autoAuthStorage struct {
	keyring authStorageBackend
	file    authStorageBackend
}

func (s *autoAuthStorage) Load() (*AuthDotJSON, error) {
	auth, err := s.keyring.Load()
	if err == nil && auth != nil {
		return auth, nil
	}
	return s.file.Load()
}

func (s *autoAuthStorage) Save(auth AuthDotJSON) error {
	if err := s.keyring.Save(auth); err == nil {
		return nil
	}
	return s.file.Save(auth)
}

func (s *autoAuthStorage) Delete() (bool, error) {
	keyringRemoved, keyringErr := s.keyring.Delete()
	fileRemoved, fileErr := s.file.Delete()
	if keyringErr != nil && fileErr != nil {
		return false, keyringErr
	}
	if fileErr != nil {
		return false, fileErr
	}
	if keyringErr != nil {
		return fileRemoved, nil
	}
	return keyringRemoved || fileRemoved, nil
}

func (s *autoAuthStorage) Source() string {
	return "auto"
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
