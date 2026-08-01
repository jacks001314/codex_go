package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/config"
	"codex_go/state"
)

const rustRemoteControlAppServerClientNameNone = ""

func appServerStateDBPath(codexHome string) string {
	return filepath.Join(state.ResolveSQLiteHome(codexHome), state.StateSQLiteFilename)
}

func appServerStateDBAvailable(codexHome string) bool {
	info, err := os.Stat(appServerStateDBPath(codexHome))
	return err == nil && !info.IsDir()
}

func appServerPersistedRemoteControlEnabled(ctx context.Context, codexHome string, loadedConfig *config.Config) (bool, error) {
	dbPath := appServerStateDBPath(codexHome)
	info, err := os.Stat(dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	accountID, ok, err := appServerRemoteControlAccountID(codexHome)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	websocketURL, err := appServerRemoteControlWebSocketURL(loadedConfig.ChatGPTBaseURL())
	if err != nil {
		return false, err
	}
	sqliteConfig, err := state.NewSqliteConfig(filepath.Dir(dbPath))
	if err != nil {
		return false, err
	}
	db, err := sqliteConfig.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	if ctx == nil {
		ctx = context.Background()
	}
	var enabled sql.NullInt64
	err = db.QueryRowContext(ctx, `
SELECT remote_control_enabled
FROM remote_control_enrollments
WHERE websocket_url = ?
  AND account_id = ?
  AND app_server_client_name = ?
LIMIT 1
`, websocketURL, accountID, rustRemoteControlAppServerClientNameNone).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table: remote_control_enrollments") {
			return false, nil
		}
		return false, err
	}
	return enabled.Valid && enabled.Int64 == 1, nil
}

func appServerRemoteControlAccountID(codexHome string) (string, bool, error) {
	resolved, err := auth.NewStore(codexHome).Resolve()
	if err != nil {
		return "", false, err
	}
	if resolved == nil {
		return "", false, nil
	}
	snapshot := &resolved.Auth
	if !appServerAuthUsesCodexBackend(snapshot) {
		return "", false, nil
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	if accountID == "" {
		return "", false, nil
	}
	return accountID, true, nil
}

func appServerAuthUsesCodexBackend(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "agent-identity", "agentIdentity", "personal-access-token", "personalAccessToken":
		return true
	default:
		return false
	}
}

func appServerRemoteControlWebSocketURL(remoteControlURL string) (string, error) {
	base, err := appServerNormalizeRemoteControlBaseURL(remoteControlURL)
	if err != nil {
		return "", err
	}
	websocketURL := base.ResolveReference(&url.URL{Path: "wham/remote/control/server"})
	if base.Scheme == "https" {
		websocketURL.Scheme = "wss"
	} else {
		websocketURL.Scheme = "ws"
	}
	return websocketURL.String(), nil
}

func appServerNormalizeRemoteControlBaseURL(remoteControlURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(remoteControlURL))
	if err != nil {
		return nil, fmt.Errorf("invalid remote control URL `%s`: %w", remoteControlURL, err)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	} else if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	host := parsed.Hostname()
	localhost := appServerIsLocalhost(host)
	allowedChatGPT := appServerIsAllowedRemoteControlChatGPTHost(host)
	switch parsed.Scheme {
	case "https":
		if localhost || allowedChatGPT {
			return parsed, nil
		}
	case "http":
		if localhost {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid remote control URL `%s`; expected HTTPS URL for chatgpt.com or chatgpt-staging.com, or HTTP/HTTPS URL for localhost", remoteControlURL)
}

func appServerIsAllowedRemoteControlChatGPTHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "chatgpt.com" ||
		host == "chatgpt-staging.com" ||
		strings.HasSuffix(host, ".chatgpt.com") ||
		strings.HasSuffix(host, ".chatgpt-staging.com")
}

func appServerIsLocalhost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runAppServerRemoteControlOnly(ctx context.Context, codexHome string, runtimeOptions *appserver.RuntimeRouterOptions) error {
	return appserver.ServeRemoteControlOnly(ctx, &appserver.RemoteControlOnlyOptions{
		CodexHome:      codexHome,
		RuntimeOptions: runtimeOptions,
	})
}
