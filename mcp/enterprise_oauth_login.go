package mcp

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Rust parity: codex-rs/rmcp-client/src/enterprise_oauth_login.rs (#43844).
// Enterprise OIDC policy plus staged, credential-lock-guarded commits. Browser
// completion never persists a grant by itself; its owner revalidates authority
// under the same lock used by logout.

// EnterpriseOAuthLoginRequest describes a host-registered enterprise IdP login.
// The flow always uses OpenID Connect, strict issuer validation, and the
// credential store's private persistence.
type EnterpriseOAuthLoginRequest struct {
	CredentialName string
	Issuer         string
	ClientID       string
	CallbackPort   *uint16
	CallbackURL    string
	Timeout        time.Duration
}

// enterpriseCallbackSettings mirrors Rust enterprise_callback_settings: an
// enterprise login requires a registered client ID and an HTTP loopback
// callback, and the URL and listener must not disagree on the port.
func enterpriseCallbackSettings(issuer string, clientID string, callbackURL string, callbackPort *uint16) (net.IP, *uint16, error) {
	if err := validateMCPEMAOAuthEndpoint(issuer, "enterprise IdP issuer"); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(clientID) == "" {
		return nil, nil, errors.New("enterprise IdP login requires its registered client ID")
	}
	ip := net.IPv4(127, 0, 0, 1)
	var registeredPort *uint16
	if callbackURL = strings.TrimSpace(callbackURL); callbackURL != "" {
		if err := validateMCPEMAOAuthEndpoint(callbackURL, "enterprise IdP callback URL"); err != nil {
			return nil, nil, err
		}
		parsed, err := url.Parse(callbackURL)
		if err != nil {
			return nil, nil, errors.New("enterprise IdP callback URL must use an HTTP loopback address")
		}
		if parsed.Scheme != "http" {
			return nil, nil, errors.New("enterprise IdP callback URL must use an HTTP loopback address")
		}
		host := strings.TrimSpace(parsed.Hostname())
		if strings.EqualFold(host, "localhost") {
			ip = net.IPv4(127, 0, 0, 1)
		} else {
			parsedIP := net.ParseIP(host)
			if parsedIP == nil || !parsedIP.IsLoopback() {
				return nil, nil, errors.New("enterprise IdP callback URL must use an HTTP loopback address")
			}
			ip = parsedIP
		}
		if portText := parsed.Port(); portText != "" {
			value, err := strconv.ParseUint(portText, 10, 16)
			if err != nil {
				return nil, nil, errors.New("enterprise IdP callback URL has an invalid port")
			}
			port := uint16(value)
			registeredPort = &port
		}
	}
	if callbackPort != nil && registeredPort != nil && *callbackPort != *registeredPort {
		return nil, nil, errors.New("enterprise IdP callback URL and listener specify different ports")
	}
	if callbackPort != nil {
		return ip, callbackPort, nil
	}
	return ip, registeredPort, nil
}

// enterpriseAuthorizationURL rewrites an authorization URL for the independent
// OIDC login: MCP resource indicators are dropped (the IdP issuer is not a
// protected-resource audience) and consent is always requested.
func enterpriseAuthorizationURL(authURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(authURL))
	if err != nil {
		return "", err
	}
	pairs := make([]string, 0, 4)
	if parsed.RawQuery != "" {
		for _, segment := range strings.Split(parsed.RawQuery, "&") {
			if segment == "" {
				continue
			}
			key := segment
			value := ""
			if index := strings.Index(segment, "="); index >= 0 {
				key = segment[:index]
				value = segment[index+1:]
			}
			decodedKey, err := url.QueryUnescape(key)
			if err != nil {
				return "", err
			}
			if decodedKey == "resource" || decodedKey == "prompt" {
				continue
			}
			decodedValue, err := url.QueryUnescape(value)
			if err != nil {
				return "", err
			}
			pairs = append(pairs, url.QueryEscape(decodedKey)+"="+url.QueryEscape(decodedValue))
		}
	}
	pairs = append(pairs, "prompt=consent")
	parsed.RawQuery = strings.Join(pairs, "&")
	return parsed.String(), nil
}

// validateEnterpriseOAuthCredentials requires a refresh token and a valid OIDC
// identity assertion before a grant may be staged (Rust
// validate_enterprise_credentials). Error text never includes credential data.
func validateEnterpriseOAuthCredentials(tokens *OAuthTokenSet) error {
	if tokens == nil || strings.TrimSpace(tokens.RefreshToken) == "" {
		return errors.New("enterprise IdP login did not return a refresh token")
	}
	assertion := strings.TrimSpace(tokens.IDToken)
	if assertion == "" {
		return errors.New("enterprise IdP login did not return an OIDC identity assertion")
	}
	expectedIssuer := strings.TrimSpace(tokens.Issuer)
	if expectedIssuer == "" {
		expectedIssuer = strings.TrimSpace(tokens.ServerURL)
	}
	if err := ValidateMCPEMAOIDCIdentityAssertion(assertion, expectedIssuer, strings.TrimSpace(tokens.ClientID)); err != nil {
		return errors.New("enterprise IdP returned an invalid OIDC identity assertion")
	}
	return nil
}

// EnterpriseOAuthCredentialGuard is an exclusive credential mutation guard.
// Hold it through primary account logout so a competing process cannot commit
// between deleting the grant and signing out. Coordination, like ordinary OAuth
// refresh, is scoped to the same CODEX_HOME.
type EnterpriseOAuthCredentialGuard struct {
	store          *OAuthStore
	credentialName string
	issuer         string
	generationFile *mcpOAuthEnterpriseGenerationFile
	lock           *mcpOAuthCredentialLock
}

// AcquireEnterpriseOAuthCredentialGuard acquires the credential lock and opens
// the generation file for one enterprise credential.
func AcquireEnterpriseOAuthCredentialGuard(codexHome string, credentialName string, issuer string) (*EnterpriseOAuthCredentialGuard, error) {
	lock, err := acquireMCPOAuthCredentialLockForServer(codexHome, credentialName, issuer)
	if err != nil {
		return nil, errors.New("failed to lock enterprise credentials")
	}
	generationFile, err := openMCPOAuthEnterpriseGenerationFile(codexHome, credentialName, issuer)
	if err != nil {
		lock.Release()
		return nil, errors.New("failed to open enterprise login generation")
	}
	return &EnterpriseOAuthCredentialGuard{
		store:          NewOAuthStore(codexHome),
		credentialName: strings.TrimSpace(credentialName),
		issuer:         strings.TrimSpace(issuer),
		generationFile: generationFile,
		lock:           lock,
	}, nil
}

// DeleteTokens invalidates pending logins and deletes the grant, if present. A
// false return means no grant was stored; earlier login attempts are still
// invalidated. The generation is persisted before deletion so no successful
// logout can admit an earlier login.
func (g *EnterpriseOAuthCredentialGuard) DeleteTokens() (bool, error) {
	if g == nil || g.generationFile == nil {
		return false, errors.New("failed to invalidate pending enterprise sign-ins")
	}
	if _, err := g.generationFile.replace(); err != nil {
		return false, errors.New("failed to invalidate pending enterprise sign-ins")
	}
	removed, err := g.store.deleteWithLockHeld(g.credentialName, g.issuer)
	if err != nil {
		return false, errors.New("failed to delete enterprise credentials")
	}
	return removed, nil
}

// Close releases the generation file and the credential lock.
func (g *EnterpriseOAuthCredentialGuard) Close() {
	if g == nil {
		return
	}
	_ = g.generationFile.close()
	g.lock.Release()
}

// DeleteEnterpriseOAuthTokens invalidates earlier enterprise logins across
// processes, then deletes any stored grant.
func DeleteEnterpriseOAuthTokens(codexHome string, credentialName string, issuer string) (bool, error) {
	guard, err := AcquireEnterpriseOAuthCredentialGuard(codexHome, credentialName, issuer)
	if err != nil {
		return false, err
	}
	defer guard.Close()
	return guard.DeleteTokens()
}

// CommitEnterpriseOAuthCredentials revalidates a staged attempt under the same
// exclusive lock used by logout, then persists the grant and returns the
// caller's authority proof. isCurrent returns a pointer to the proof, or nil
// when the attempt no longer matches the active account or configuration (Rust
// EnterpriseOAuthCredentials::commit_if).
func CommitEnterpriseOAuthCredentials[T any](codexHome string, tokens *OAuthTokenSet, generation mcpOAuthEnterpriseGeneration, isCurrent func() *T) (T, error) {
	var zero T
	if tokens == nil {
		return zero, errors.New("failed to store enterprise credentials")
	}
	guard, err := AcquireEnterpriseOAuthCredentialGuard(codexHome, tokens.ServerName, tokens.ServerURL)
	if err != nil {
		return zero, err
	}
	defer guard.Close()
	current, ok, err := guard.generationFile.current()
	if err != nil {
		return zero, errors.New("failed to read enterprise login generation")
	}
	if !ok || current != generation {
		return zero, errors.New("enterprise sign-in was invalidated by logout")
	}
	authority := isCurrent()
	if authority == nil {
		return zero, errors.New("enterprise sign-in no longer matches the active account or configuration")
	}
	// No intervening await: the generation check, authority check and store all
	// happen while the credential lock is held.
	if err := guard.store.saveWithLockHeld(tokens); err != nil {
		return zero, errors.New("failed to store enterprise credentials")
	}
	return *authority, nil
}
