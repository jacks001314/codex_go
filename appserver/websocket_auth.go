package appserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultWebSocketMaxClockSkewSeconds uint64 = 30
	MinSignedBearerSecretBytes                 = 32
)

type WebSocketAuthMode string

const (
	WebSocketAuthCapabilityToken   WebSocketAuthMode = "capability-token"
	WebSocketAuthSignedBearerToken WebSocketAuthMode = "signed-bearer-token"
)

type WebSocketAuthSettings struct {
	Mode                WebSocketAuthMode
	TokenFile           string
	TokenSHA256         string
	SharedSecretFile    string
	Issuer              string
	Audience            string
	MaxClockSkewSeconds uint64
}

type WebSocketAuthPolicy struct {
	mode                WebSocketAuthMode
	tokenSHA256         [32]byte
	sharedSecret        []byte
	issuer              string
	audience            string
	maxClockSkewSeconds int64
}

type WebSocketAuthError struct {
	StatusCode int
	Message    string
}

func (e *WebSocketAuthError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewWebSocketAuthPolicy(settings *WebSocketAuthSettings) (*WebSocketAuthPolicy, error) {
	if settings == nil || strings.TrimSpace(string(settings.Mode)) == "" {
		return &WebSocketAuthPolicy{}, nil
	}
	switch settings.Mode {
	case WebSocketAuthCapabilityToken:
		return newCapabilityTokenPolicy(settings)
	case WebSocketAuthSignedBearerToken:
		return newSignedBearerTokenPolicy(settings)
	default:
		return nil, fmt.Errorf("unknown websocket auth mode %q", settings.Mode)
	}
}

func (p *WebSocketAuthPolicy) IsConfigured() bool {
	return p != nil && p.mode != ""
}

func (p *WebSocketAuthPolicy) IsUnauthenticatedNonLoopbackListener(addr net.Addr) bool {
	if p != nil && p.IsConfigured() {
		return false
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp == nil {
		return false
	}
	return !tcp.IP.IsLoopback()
}

func (p *WebSocketAuthPolicy) Authorize(headers http.Header, now time.Time) error {
	if p == nil || !p.IsConfigured() {
		return nil
	}
	token, err := bearerTokenFromHeaders(headers)
	if err != nil {
		return err
	}
	switch p.mode {
	case WebSocketAuthCapabilityToken:
		actual := sha256.Sum256([]byte(token))
		if hmac.Equal(p.tokenSHA256[:], actual[:]) {
			return nil
		}
		return unauthorizedWebSocket("invalid websocket bearer token")
	case WebSocketAuthSignedBearerToken:
		return p.verifySignedBearerToken(token, now)
	default:
		return unauthorizedWebSocket("invalid websocket auth policy")
	}
}

func newCapabilityTokenPolicy(settings *WebSocketAuthSettings) (*WebSocketAuthPolicy, error) {
	tokenFile := strings.TrimSpace(settings.TokenFile)
	tokenSHA := strings.TrimSpace(settings.TokenSHA256)
	if tokenFile != "" && tokenSHA != "" {
		return nil, errors.New("`--ws-token-file` and `--ws-token-sha256` are mutually exclusive")
	}
	var digest [32]byte
	switch {
	case tokenFile != "":
		token, err := readTrimmedWebSocketSecret(tokenFile)
		if err != nil {
			return nil, err
		}
		digest = sha256.Sum256([]byte(token))
	case tokenSHA != "":
		decoded, err := parseWebSocketSHA256Digest(tokenSHA)
		if err != nil {
			return nil, err
		}
		digest = decoded
	default:
		return nil, errors.New("`--ws-token-file` or `--ws-token-sha256` is required when `--ws-auth capability-token` is set")
	}
	return &WebSocketAuthPolicy{mode: WebSocketAuthCapabilityToken, tokenSHA256: digest}, nil
}

func newSignedBearerTokenPolicy(settings *WebSocketAuthSettings) (*WebSocketAuthPolicy, error) {
	secretFile := strings.TrimSpace(settings.SharedSecretFile)
	if secretFile == "" {
		return nil, errors.New("`--ws-shared-secret-file` is required when `--ws-auth signed-bearer-token` is set")
	}
	secret, err := readTrimmedWebSocketSecret(secretFile)
	if err != nil {
		return nil, err
	}
	if len(secret) < MinSignedBearerSecretBytes {
		return nil, fmt.Errorf("signed websocket bearer secret %s must be at least %d bytes", secretFile, MinSignedBearerSecretBytes)
	}
	skew := settings.MaxClockSkewSeconds
	if skew == 0 {
		skew = DefaultWebSocketMaxClockSkewSeconds
	}
	return &WebSocketAuthPolicy{
		mode:                WebSocketAuthSignedBearerToken,
		sharedSecret:        []byte(secret),
		issuer:              strings.TrimSpace(settings.Issuer),
		audience:            strings.TrimSpace(settings.Audience),
		maxClockSkewSeconds: int64(skew),
	}, nil
}

func bearerTokenFromHeaders(headers http.Header) (string, error) {
	header := headers.Get("Authorization")
	if strings.TrimSpace(header) == "" {
		return "", unauthorizedWebSocket("missing websocket bearer token")
	}
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", unauthorizedWebSocket("invalid authorization header")
	}
	return strings.TrimSpace(token), nil
}

func (p *WebSocketAuthPolicy) verifySignedBearerToken(token string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return unauthorizedWebSocket("invalid websocket jwt")
	}
	signingInput := parts[0] + "." + parts[1]
	expected := hmac.New(sha256.New, p.sharedSecret)
	_, _ = expected.Write([]byte(signingInput))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, expected.Sum(nil)) {
		return unauthorizedWebSocket("invalid websocket jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return unauthorizedWebSocket("invalid websocket jwt")
	}
	var claims websocketJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return unauthorizedWebSocket("invalid websocket jwt")
	}
	return p.validateJWTClaims(&claims, now)
}

type websocketJWTClaims struct {
	Exp int64           `json:"exp"`
	Nbf *int64          `json:"nbf,omitempty"`
	Iss string          `json:"iss,omitempty"`
	Aud json.RawMessage `json:"aud,omitempty"`
}

func (p *WebSocketAuthPolicy) validateJWTClaims(claims *websocketJWTClaims, now time.Time) error {
	if claims == nil || claims.Exp == 0 {
		return unauthorizedWebSocket("invalid websocket jwt")
	}
	nowUnix := now.UTC().Unix()
	if nowUnix > claims.Exp+p.maxClockSkewSeconds {
		return unauthorizedWebSocket("expired websocket jwt")
	}
	if claims.Nbf != nil && nowUnix < *claims.Nbf-p.maxClockSkewSeconds {
		return unauthorizedWebSocket("websocket jwt is not valid yet")
	}
	if p.issuer != "" && claims.Iss != p.issuer {
		return unauthorizedWebSocket("websocket jwt issuer mismatch")
	}
	if p.audience != "" && !websocketAudienceMatches(claims.Aud, p.audience) {
		return unauthorizedWebSocket("websocket jwt audience mismatch")
	}
	return nil
}

func websocketAudienceMatches(raw json.RawMessage, expected string) bool {
	if len(raw) == 0 {
		return false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == expected
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err == nil {
		for _, audience := range multiple {
			if audience == expected {
				return true
			}
		}
	}
	return false
}

func readTrimmedWebSocketSecret(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read websocket auth secret %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", fmt.Errorf("websocket auth secret %s must not be empty", path)
	}
	return trimmed, nil
}

func parseWebSocketSHA256Digest(value string) ([32]byte, error) {
	var digest [32]byte
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 64 {
		return digest, errors.New("--ws-token-sha256 must be a 64-character hex SHA-256 digest")
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil || len(decoded) != 32 {
		return digest, errors.New("--ws-token-sha256 must be a 64-character hex SHA-256 digest")
	}
	copy(digest[:], decoded)
	return digest, nil
}

func unauthorizedWebSocket(message string) error {
	return &WebSocketAuthError{StatusCode: http.StatusUnauthorized, Message: message}
}
