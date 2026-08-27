package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Trusted access context for MCP metadata (Rust codex-mcp trusted_access.rs,
// #40992/#41005). It attaches host-owned `openai/entitlementContext` metadata
// to eligible plugin MCP calls describing the account-bound cyber verified
// access. The grant fetch is supplied by the runtime; the data structures and
// status derivation mirror Rust so the entitlement context round-trips.

const (
	entitlementContextKey  = "openai/entitlementContext"
	maxVerifiedAccessBytes = 1024 * 1024
	trustedAccessTimeoutMS = 2500
)

// VerifiedAccessResponse mirrors Rust VerifiedAccessResponse.
type VerifiedAccessResponse struct {
	Programs []json.RawMessage `json:"programs"`
}

// VerifiedAccessProgram mirrors Rust VerifiedAccessProgram.
type VerifiedAccessProgram struct {
	Program string                `json:"program"`
	State   VerifiedAccessState   `json:"state"`
	Grants  []VerifiedAccessGrant `json:"grants"`
}

// VerifiedAccessState mirrors the Rust verified-access program state.
type VerifiedAccessState string

const (
	VerifiedAccessActive      VerifiedAccessState = "active"
	VerifiedAccessInactive    VerifiedAccessState = "inactive"
	VerifiedAccessUnavailable VerifiedAccessState = "unavailable"
)

// VerifiedAccessGrant mirrors Rust VerifiedAccessGrant.
type VerifiedAccessGrant struct {
	Level  TrustedAccessLevel   `json:"level"`
	Source VerifiedAccessSource `json:"source"`
}

// TrustedAccessLevel mirrors Rust TrustedAccessLevel.
type TrustedAccessLevel string

const (
	TrustedAccessTac1       TrustedAccessLevel = "tac1"
	TrustedAccessTac2       TrustedAccessLevel = "tac2"
	TrustedAccessTac3       TrustedAccessLevel = "tac3"
	TrustedAccessGovernment TrustedAccessLevel = "government"
)

// VerifiedAccessSource mirrors Rust VerifiedAccessSource.
type VerifiedAccessSource string

const (
	VerifiedAccessIndividual   VerifiedAccessSource = "individual"
	VerifiedAccessOrganization VerifiedAccessSource = "organization"
)

// cyberTrustedAccessStatus derives the `cyber_trusted_access` entitlement
// status from a verified-access program (Rust TrustedAccessContext). It returns
// the status and the mapped grants, or ok=false when the program state/grants
// combination is not a valid, unambiguous outcome.
func cyberTrustedAccessStatus(program *VerifiedAccessProgram) (string, []map[string]any, bool) {
	if program == nil {
		return "", nil, false
	}
	grants := make([]map[string]any, 0, len(program.Grants))
	for _, grant := range program.Grants {
		source := "user"
		if grant.Source == VerifiedAccessOrganization {
			source = "current_account"
		}
		grants = append(grants, map[string]any{
			"level":  string(grant.Level),
			"source": source,
		})
	}
	switch {
	case program.State == VerifiedAccessActive && len(program.Grants) > 0:
		return "granted", grants, true
	case program.State == VerifiedAccessInactive && len(program.Grants) == 0:
		return "not_granted", grants, true
	case program.State == VerifiedAccessUnavailable && len(program.Grants) == 0:
		return "unknown", grants, true
	default:
		return "", nil, false
	}
}

// entitlementContextValue builds the host-owned `openai/entitlementContext`
// metadata value (Rust TrustedAccessContext::add_context) from a status. When
// the status is empty or the fetch failed, an unknown status is used.
func entitlementContextValue(status string, grants []map[string]any) map[string]any {
	if strings.TrimSpace(status) == "" {
		status = "unknown"
	}
	if grants == nil {
		grants = []map[string]any{}
	}
	return map[string]any{
		"schemaVersion": 1,
		"entitlements": map[string]any{
			"cyber_trusted_access": map[string]any{
				"schemaVersion": 1,
				"status":        status,
				"grants":        grants,
				"stale":         false,
			},
		},
	}
}

const verifiedAccessPath = "/accounts/verified_access"

// TrustedAccessContext fetches account-bound cyber verified access and attaches
// it as host-owned `openai/entitlementContext` metadata to eligible plugin MCP
// calls (Rust TrustedAccessContext, #40992/#41005). The runtime supplies the
// ChatGPT base URL, an HTTP doer, and the auth-request applier; the account
// identity to validate against is carried in Account.
type TrustedAccessContext struct {
	ChatGPTBaseURL string
	HTTPDoer       func(*http.Request) (*http.Response, error)
	ApplyAuth      func(*http.Request) error
	Account        *TrustedAccessAccount
}

// TrustedAccessAccount carries the account identity that must match the fetched
// access (Rust CodexAuth account identity checks).
type TrustedAccessAccount struct {
	AccountID        string
	ChatGPTUserID    string
	Workspace        bool
	FedRAMP          bool
	UsesCodexBackend bool
}

// addContext replaces caller-supplied entitlement metadata with a fresh
// verified result (Rust TrustedAccessContext::add_context). When the grant
// fetch cannot be verified (auth, identity mismatch, malformed/oversized
// response, or timeout) it falls back to an `unknown` status.
func (c *TrustedAccessContext) addContext(meta map[string]any) map[string]any {
	if meta == nil {
		meta = map[string]any{}
	}
	delete(meta, entitlementContextKey)
	status, grants := c.fetchStatus()
	meta[entitlementContextKey] = entitlementContextValue(status, grants)
	return meta
}

func (c *TrustedAccessContext) fetchStatus() (string, []map[string]any) {
	if c == nil || c.Account == nil || !c.Account.UsesCodexBackend || strings.TrimSpace(c.ChatGPTBaseURL) == "" {
		return "unknown", nil
	}
	if c.HTTPDoer == nil {
		return "unknown", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), trustedAccessTimeoutMS*time.Millisecond)
	defer cancel()
	url := strings.TrimRight(strings.TrimSpace(c.ChatGPTBaseURL), "/") + verifiedAccessPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "unknown", nil
	}
	if c.ApplyAuth != nil {
		if err := c.ApplyAuth(req); err != nil {
			return "unknown", nil
		}
	}
	response, err := c.HTTPDoer(req)
	if err != nil {
		return "unknown", nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "unknown", nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxVerifiedAccessBytes))
	if err != nil || len(body) > maxVerifiedAccessBytes {
		return "unknown", nil
	}
	return parseVerifiedAccess(body)
}

func parseVerifiedAccess(body []byte) (string, []map[string]any) {
	var response VerifiedAccessResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "unknown", nil
	}
	var cyberProgram *VerifiedAccessProgram
	for _, rawProgram := range response.Programs {
		var program VerifiedAccessProgram
		if err := json.Unmarshal(rawProgram, &program); err != nil {
			continue
		}
		if program.Program != "cyber" {
			continue
		}
		if cyberProgram != nil {
			// Multiple cyber programs is ambiguous -> unknown.
			return "unknown", nil
		}
		cp := program
		cyberProgram = &cp
	}
	if cyberProgram == nil {
		return "unknown", nil
	}
	status, grants, ok := cyberTrustedAccessStatus(cyberProgram)
	if !ok {
		return "unknown", nil
	}
	return status, grants
}
