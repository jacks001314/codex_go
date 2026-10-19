package chatgptapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// SecuritySetupAction is the browser action attached to a security reminder.
type SecuritySetupAction struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// SecuritySetupNotice is the optional account-security reminder returned by
// `/wham/security-setup` (Rust #49715).
type SecuritySetupNotice struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Action      SecuritySetupAction `json:"action"`
}

type securitySetupResponse struct {
	Notice *SecuritySetupNotice `json:"notice"`
}

// Valid reports whether the server-owned copy and action are safe to render.
func (n SecuritySetupNotice) Valid() bool {
	if strings.TrimSpace(n.Title) == "" || len(n.Title) > 256 ||
		len(n.Description) > 2048 || strings.TrimSpace(n.Action.Label) == "" ||
		len(n.Action.Label) > 128 {
		return false
	}
	for _, value := range []string{n.Title, n.Description, n.Action.Label} {
		for _, r := range value {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(n.Action.URL))
	if err != nil || parsed.Scheme != "https" ||
		strings.ToLower(parsed.Hostname()) != "chatgpt.com" ||
		parsed.User != nil || parsed.Port() != "" {
		return false
	}
	return true
}

// GetSecuritySetup reads the optional reminder. A missing notice is not an
// error; unsupported/error responses produce no notice.
func (c *CloudClient) GetSecuritySetup(ctx context.Context) (*SecuritySetupNotice, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var response securitySetupResponse
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("security-setup"), nil, nil, &response); err != nil {
		return nil, err
	}
	if response.Notice == nil {
		return nil, nil
	}
	if !response.Notice.Valid() {
		return nil, nil
	}
	return response.Notice, nil
}
