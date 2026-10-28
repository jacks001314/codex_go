package config

// Trusted enrollment settings for a customer-hosted configuration server
// (Rust codex_config::ConfigServer, #56295/#56314).

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const configServerBundlePath = "codex/config/bundle"

// ConfigServerOAuth is the public-client OAuth configuration used to fetch a
// customer-hosted managed configuration bundle
// (Rust ConfigServerOAuth / OAuthSessionConfig).
type ConfigServerOAuth struct {
	AuthorizationURL       string
	TokenURL               string
	DeviceAuthorizationURL *string
	ClientID               string
	Resource               *string
	Scopes                 []string
	RedirectPort           *uint16
}

// ConfigServer is the trusted local managed requirement that selects a
// customer-hosted configuration server.
type ConfigServer struct {
	BaseURL string
	OAuth   *ConfigServerOAuth
}

// ValidatedBaseURL validates the trusted settings and returns the normalized
// base URL (Rust ConfigServer::validated_base_url).
func (s *ConfigServer) ValidatedBaseURL() (string, error) {
	if s == nil {
		return "", errors.New("config_server is not configured")
	}
	endpoint, err := validateConfigServerHTTPSURL(s.BaseURL)
	if err != nil {
		return "", err
	}
	if endpoint.RawQuery != "" {
		return "", errors.New("config_server.base_url must not contain a query")
	}
	if s.OAuth != nil {
		if _, err := validateConfigServerHTTPSURL(s.OAuth.AuthorizationURL); err != nil {
			return "", err
		}
		if _, err := validateConfigServerHTTPSURL(s.OAuth.TokenURL); err != nil {
			return "", err
		}
		if s.OAuth.DeviceAuthorizationURL != nil {
			if _, err := validateConfigServerHTTPSURL(*s.OAuth.DeviceAuthorizationURL); err != nil {
				return "", err
			}
		}
		if strings.TrimSpace(s.OAuth.ClientID) == "" || (s.OAuth.RedirectPort != nil && *s.OAuth.RedirectPort == 0) {
			return "", errors.New("config_server OAuth requires a client ID and a nonzero redirect port when specified")
		}
	}
	return endpoint.String(), nil
}

// BundleURL returns the fixed managed-configuration route under the configured
// base URL (Rust BackendClient::config_bundle_url for a config endpoint).
func (s *ConfigServer) BundleURL() (string, error) {
	baseURL, err := s.ValidatedBaseURL()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(baseURL, "/") + "/" + configServerBundlePath, nil
}

// validateConfigServerHTTPSURL mirrors Rust's
// `validate_endpoint_url(...).filter(scheme == https)`: the URL must parse,
// must not embed credentials or a fragment, and must use HTTPS.
func validateConfigServerHTTPSURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("config_server URLs must use HTTPS without userinfo or fragments")
	}
	return parsed, nil
}

// ConfigServerFromMap parses the `[config_server]` managed requirements table.
// Unknown fields are rejected like Rust's `deny_unknown_fields` input shape.
func ConfigServerFromMap(values map[string]any) (*ConfigServer, error) {
	for name := range values {
		switch name {
		case "base_url", "baseUrl", "oauth":
		default:
			return nil, fmt.Errorf("unknown field `%s`, expected `base_url` or `oauth`", name)
		}
	}
	baseURL, ok := stringAnyKey(values, "base_url", "baseUrl")
	if !ok || strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("missing field `base_url`")
	}
	server := &ConfigServer{BaseURL: baseURL}
	if nested, ok := mapAnyKey(values, "oauth"); ok {
		oauth, err := configServerOAuthFromMap(nested)
		if err != nil {
			return nil, err
		}
		server.OAuth = oauth
	}
	if _, err := server.ValidatedBaseURL(); err != nil {
		return nil, err
	}
	return server, nil
}

func configServerOAuthFromMap(values map[string]any) (*ConfigServerOAuth, error) {
	authorizationURL, ok := stringAnyKey(values, "authorization_url", "authorizationUrl")
	if !ok || strings.TrimSpace(authorizationURL) == "" {
		return nil, errors.New("missing field `authorization_url`")
	}
	tokenURL, ok := stringAnyKey(values, "token_url", "tokenUrl")
	if !ok || strings.TrimSpace(tokenURL) == "" {
		return nil, errors.New("missing field `token_url`")
	}
	clientID, _ := stringAnyKey(values, "client_id", "clientId")
	oauth := &ConfigServerOAuth{
		AuthorizationURL: authorizationURL,
		TokenURL:         tokenURL,
		ClientID:         clientID,
	}
	if value, ok := stringAnyKey(values, "device_authorization_url", "deviceAuthorizationUrl"); ok {
		oauth.DeviceAuthorizationURL = &value
	}
	if value, ok := stringAnyKey(values, "resource"); ok {
		oauth.Resource = &value
	}
	if scopes, ok := stringListAnyKey(values, "scopes"); ok {
		oauth.Scopes = append([]string(nil), scopes...)
	}
	if raw, ok := values["redirect_port"]; ok {
		port, err := configServerRedirectPort(raw)
		if err != nil {
			return nil, err
		}
		oauth.RedirectPort = &port
	} else if raw, ok := values["redirectPort"]; ok {
		port, err := configServerRedirectPort(raw)
		if err != nil {
			return nil, err
		}
		oauth.RedirectPort = &port
	}
	return oauth, nil
}

func configServerRedirectPort(value any) (uint16, error) {
	number, ok := value.(int64)
	if !ok {
		integer, isInt := value.(int)
		if !isInt {
			return 0, errors.New("invalid type for redirect_port: expected integer")
		}
		number = int64(integer)
	}
	if number < 0 || number > 65535 {
		return 0, errors.New("redirect_port is out of range")
	}
	return uint16(number), nil
}

func cloneConfigServer(server *ConfigServer) *ConfigServer {
	if server == nil {
		return nil
	}
	cloned := *server
	if server.OAuth != nil {
		oauth := *server.OAuth
		if server.OAuth.DeviceAuthorizationURL != nil {
			value := *server.OAuth.DeviceAuthorizationURL
			oauth.DeviceAuthorizationURL = &value
		}
		if server.OAuth.Resource != nil {
			value := *server.OAuth.Resource
			oauth.Resource = &value
		}
		oauth.Scopes = append([]string(nil), server.OAuth.Scopes...)
		if server.OAuth.RedirectPort != nil {
			port := *server.OAuth.RedirectPort
			oauth.RedirectPort = &port
		}
		cloned.OAuth = &oauth
	}
	return &cloned
}

// RequiresRemoteConfig reports whether local administration requires a
// remotely delivered managed bundle (Rust
// ConfigRequirementsToml::requires_remote_config).
func (r *ConfigRequirements) RequiresRemoteConfig() bool {
	return r != nil && r.ConfigServer != nil
}

// RequiresRemoteConfig reports whether local administration requires a
// remotely delivered managed bundle.
func (c *Config) RequiresRemoteConfig() bool {
	return c != nil && c.Requirements.RequiresRemoteConfig()
}
