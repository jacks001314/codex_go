package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	mcpOAuthFallbackFilename = ".credentials.json"
	mcpOAuthServerType       = "http"
	mcpOAuthRefreshSkew      = 30 * time.Second
)

type OAuthTokenSet struct {
	ServerName      string   `json:"server_name"`
	ServerURL       string   `json:"server_url"`
	ClientID        string   `json:"client_id"`
	ClientSecret    string   `json:"client_secret,omitempty"`
	AccessToken     string   `json:"access_token"`
	RefreshToken    string   `json:"refresh_token,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	ExpiresAtMillis *int64   `json:"expires_at,omitempty"`
}

type OAuthStore struct {
	CodexHome string
}

type oauthFallbackEntry struct {
	ServerName      string   `json:"server_name"`
	ServerURL       string   `json:"server_url"`
	ClientID        string   `json:"client_id"`
	ClientSecret    *string  `json:"client_secret,omitempty"`
	AccessToken     string   `json:"access_token"`
	ExpiresAtMillis *int64   `json:"expires_at,omitempty"`
	RefreshToken    *string  `json:"refresh_token,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
}

func NewOAuthStore(codexHome string) *OAuthStore {
	return &OAuthStore{CodexHome: codexHome}
}

func (s *OAuthStore) Load(serverName string, serverURL string) (*OAuthTokenSet, error) {
	if s == nil {
		return nil, errors.New("MCP OAuth store is nil")
	}
	file, err := s.readFallbackFile()
	if err != nil || len(file) == 0 {
		return nil, err
	}
	key, err := computeMCPOAuthStoreKey(serverName, serverURL)
	if err != nil {
		return nil, err
	}
	if entry := file[key]; entry != nil {
		return oauthTokenSetFromFallbackEntry(entry), nil
	}
	for _, entry := range file {
		if entry == nil {
			continue
		}
		entryKey, err := computeMCPOAuthStoreKey(entry.ServerName, entry.ServerURL)
		if err != nil {
			return nil, err
		}
		if entryKey == key {
			return oauthTokenSetFromFallbackEntry(entry), nil
		}
	}
	return nil, nil
}

func (s *OAuthStore) Save(tokens *OAuthTokenSet) error {
	if s == nil {
		return errors.New("MCP OAuth store is nil")
	}
	if tokens == nil {
		return errors.New("MCP OAuth tokens are required")
	}
	if strings.TrimSpace(tokens.ServerName) == "" || strings.TrimSpace(tokens.ServerURL) == "" {
		return errors.New("MCP OAuth server name and URL are required")
	}
	if strings.TrimSpace(tokens.ClientID) == "" {
		return errors.New("MCP OAuth client ID is required")
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return errors.New("MCP OAuth access token is required")
	}
	file, err := s.readFallbackFile()
	if err != nil {
		return err
	}
	if file == nil {
		file = map[string]*oauthFallbackEntry{}
	}
	key, err := computeMCPOAuthStoreKey(tokens.ServerName, tokens.ServerURL)
	if err != nil {
		return err
	}
	file[key] = oauthFallbackEntryFromTokenSet(tokens)
	return s.writeFallbackFile(file)
}

func (s *OAuthStore) Delete(serverName string, serverURL string) (bool, error) {
	if s == nil {
		return false, errors.New("MCP OAuth store is nil")
	}
	file, err := s.readFallbackFile()
	if err != nil || len(file) == 0 {
		return false, err
	}
	key, err := computeMCPOAuthStoreKey(serverName, serverURL)
	if err != nil {
		return false, err
	}
	removed := false
	if _, ok := file[key]; ok {
		delete(file, key)
		removed = true
	}
	for entryKey, entry := range file {
		if entry == nil {
			continue
		}
		computed, err := computeMCPOAuthStoreKey(entry.ServerName, entry.ServerURL)
		if err != nil {
			return false, err
		}
		if computed == key {
			delete(file, entryKey)
			removed = true
		}
	}
	if !removed {
		return false, nil
	}
	return true, s.writeFallbackFile(file)
}

func (s *OAuthStore) AuthStatus(serverName string, config *ServerConfig) (MCPAuthStatus, error) {
	if config == nil || strings.TrimSpace(config.URL) == "" {
		return MCPAuthUnsupported, nil
	}
	if strings.TrimSpace(config.BearerTokenEnvVar) != "" {
		return MCPAuthBearerToken, nil
	}
	tokens, err := s.Load(serverName, config.URL)
	if err != nil {
		return MCPAuthUnsupported, err
	}
	if tokens == nil {
		if strings.TrimSpace(config.OAuthClientID) != "" || strings.TrimSpace(config.OAuthResource) != "" {
			return MCPAuthNotLoggedIn, nil
		}
		return MCPAuthUnsupported, nil
	}
	if tokens.Usable(time.Now()) {
		return MCPAuthOAuth, nil
	}
	return MCPAuthNotLoggedIn, nil
}

func (t *OAuthTokenSet) Usable(now time.Time) bool {
	if t == nil || strings.TrimSpace(t.ClientID) == "" {
		return false
	}
	if !tokenNeedsRefresh(t.ExpiresAtMillis, now) {
		return strings.TrimSpace(t.AccessToken) != ""
	}
	return strings.TrimSpace(t.RefreshToken) != ""
}

func (t *OAuthTokenSet) AccessTokenForRequest(now time.Time) string {
	if t == nil || strings.TrimSpace(t.AccessToken) == "" {
		return ""
	}
	if tokenNeedsRefresh(t.ExpiresAtMillis, now) {
		return ""
	}
	return strings.TrimSpace(t.AccessToken)
}

func (s *OAuthStore) path() string {
	return filepath.Join(s.CodexHome, mcpOAuthFallbackFilename)
}

func (s *OAuthStore) readFallbackFile() (map[string]*oauthFallbackEntry, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]*oauthFallbackEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	file := map[string]*oauthFallbackEntry{}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *OAuthStore) writeFallbackFile(file map[string]*oauthFallbackEntry) error {
	path := s.path()
	if len(file) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func oauthTokenSetFromFallbackEntry(entry *oauthFallbackEntry) *OAuthTokenSet {
	if entry == nil {
		return nil
	}
	tokens := &OAuthTokenSet{
		ServerName:      entry.ServerName,
		ServerURL:       entry.ServerURL,
		ClientID:        entry.ClientID,
		AccessToken:     entry.AccessToken,
		Scopes:          append([]string(nil), entry.Scopes...),
		ExpiresAtMillis: cloneInt64Pointer(entry.ExpiresAtMillis),
	}
	if entry.ClientSecret != nil {
		tokens.ClientSecret = strings.TrimSpace(*entry.ClientSecret)
	}
	if entry.RefreshToken != nil {
		tokens.RefreshToken = *entry.RefreshToken
	}
	return tokens
}

func oauthFallbackEntryFromTokenSet(tokens *OAuthTokenSet) *oauthFallbackEntry {
	entry := &oauthFallbackEntry{
		ServerName:      strings.TrimSpace(tokens.ServerName),
		ServerURL:       strings.TrimSpace(tokens.ServerURL),
		ClientID:        strings.TrimSpace(tokens.ClientID),
		AccessToken:     strings.TrimSpace(tokens.AccessToken),
		Scopes:          append([]string(nil), tokens.Scopes...),
		ExpiresAtMillis: cloneInt64Pointer(tokens.ExpiresAtMillis),
	}
	if refresh := strings.TrimSpace(tokens.RefreshToken); refresh != "" {
		entry.RefreshToken = &refresh
	}
	if secret := strings.TrimSpace(tokens.ClientSecret); secret != "" {
		entry.ClientSecret = &secret
	}
	return entry
}

func computeMCPOAuthStoreKey(serverName string, serverURL string) (string, error) {
	serverName = strings.TrimSpace(serverName)
	serverURL = strings.TrimSpace(serverURL)
	if serverName == "" || serverURL == "" {
		return "", fmt.Errorf("MCP OAuth server name and URL are required")
	}
	payload := map[string]any{
		"type":    mcpOAuthServerType,
		"url":     serverURL,
		"headers": map[string]string{},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return serverName + "|" + hex.EncodeToString(sum[:])[:16], nil
}

func tokenNeedsRefresh(expiresAtMillis *int64, now time.Time) bool {
	if expiresAtMillis == nil {
		return false
	}
	expiresAt := time.UnixMilli(*expiresAtMillis)
	return !now.Add(mcpOAuthRefreshSkew).Before(expiresAt)
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
