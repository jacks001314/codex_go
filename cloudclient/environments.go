package cloudclient

// Rust #61956: the Cloud picker reads the bound account's runnable
// environment configurations. Go mirrors the native client's authenticated
// REST read so a saved environment can be selected when a Cloud thread starts.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	environmentCatalogTimeout  = 10 * time.Second
	environmentCatalogMaxBytes = 1024 * 1024
	environmentCatalogPath     = "/v1/environment-configs"
	// DefaultEnvironmentConfigID is the hosted service's default managed
	// environment. It is never offered as a saved choice (Rust #61956).
	DefaultEnvironmentConfigID = "asenvcfg_base"

	environmentCatalogScopeUser      = "user"
	environmentCatalogScopeWorkspace = "workspace"
)

// ErrEnvironmentCatalogUnavailable reports a failed, oversized, or malformed
// saved environment catalog read. The outcome carries no partial page.
var ErrEnvironmentCatalogUnavailable = errors.New("saved environment catalog is unavailable")

// SavedEnvironment is one saved environment available to the current Cloud
// account (Rust #61956 SavedEnvironment).
type SavedEnvironment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// WorkspaceShared reports an environment shared with the workspace; it is
	// derived from the queried scope rather than the response body.
	WorkspaceShared bool `json:"-"`
}

type environmentCatalogPage struct {
	Data       []SavedEnvironment `json:"data"`
	NextCursor *string            `json:"next_cursor"`
}

// ListSavedEnvironments reads the personal then the workspace scope through
// the authenticated picker API. Both scopes share one deadline, and a repeated
// cursor stops the read instead of looping (Rust #61956).
func (c *Client) ListSavedEnvironments(ctx context.Context) ([]SavedEnvironment, error) {
	if c == nil || c.origin == nil || c.httpClient == nil {
		return nil, ErrClientBuild
	}
	runCtx, cancel := context.WithTimeout(ctx, environmentCatalogTimeout)
	defer cancel()
	environments, err := c.listSavedEnvironmentScope(runCtx, environmentCatalogScopeUser, nil)
	if err != nil {
		return nil, err
	}
	return c.listSavedEnvironmentScope(runCtx, environmentCatalogScopeWorkspace, environments)
}

func (c *Client) listSavedEnvironmentScope(ctx context.Context, scope string, environments []SavedEnvironment) ([]SavedEnvironment, error) {
	var cursor *string
	seenCursors := make(map[string]struct{})
	for {
		endpoint := *c.origin
		endpoint.Path = environmentCatalogPath
		endpoint.RawQuery = environmentCatalogQuery(scope, cursor)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, ErrEnvironmentCatalogUnavailable
		}
		request.Header.Set("Authorization", "Bearer "+c.credentials.BearerToken)
		request.Header.Set("chatgpt-account-id", c.credentials.AccountID)
		request.Header.Set("x-openai-product-sku", "codex")
		response, err := c.httpClient.Do(request)
		if err != nil {
			return nil, ErrEnvironmentCatalogUnavailable
		}
		body, readErr := readEnvironmentCatalogBody(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode < 200 || response.StatusCode > 299 {
			return nil, ErrEnvironmentCatalogUnavailable
		}
		var page environmentCatalogPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, ErrEnvironmentCatalogUnavailable
		}
		for _, config := range page.Data {
			if config.ID == "" || config.ID == DefaultEnvironmentConfigID || strings.TrimSpace(config.Name) == "" {
				continue
			}
			config.WorkspaceShared = scope == environmentCatalogScopeWorkspace
			environments = append(environments, config)
		}
		if page.NextCursor == nil {
			return environments, nil
		}
		if _, repeated := seenCursors[*page.NextCursor]; repeated {
			return nil, ErrEnvironmentCatalogUnavailable
		}
		seenCursors[*page.NextCursor] = struct{}{}
		next := *page.NextCursor
		cursor = &next
	}
}

// environmentCatalogQuery mirrors the native picker query, including its
// parameter order, so a recorded request stays byte-compatible.
func environmentCatalogQuery(scope string, cursor *string) string {
	query := "scope=" + url.QueryEscape(scope) + "&limit=100&omitDraft=true&view=picker"
	if cursor != nil {
		query += "&cursor=" + url.QueryEscape(*cursor)
	}
	return query
}

func readEnvironmentCatalogBody(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, environmentCatalogMaxBytes+1))
	if err != nil || len(raw) > environmentCatalogMaxBytes {
		return nil, ErrEnvironmentCatalogUnavailable
	}
	return raw, nil
}
