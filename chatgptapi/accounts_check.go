package chatgptapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// Rust parity: codex-rs/backend-client/src/types.rs AccountsCheckResponse /
// AccountEntry. The routing fields drive the app-server's workspace routing
// (workspace_backend_origin + account_routing_override).

// AccountsCheckEntry is one ChatGPT account as reported by the accounts/check
// endpoint.
type AccountsCheckEntry struct {
	ID                     string  `json:"id"`
	PlanType               *string `json:"plan_type,omitempty"`
	WorkspaceBackendOrigin *string `json:"workspace_backend_origin,omitempty"`
	AccountRoutingOverride *string `json:"account_routing_override,omitempty"`
	Name                   *string `json:"name,omitempty"`
	ProfilePictureURL      *string `json:"profile_picture_url,omitempty"`
	Structure              string  `json:"structure,omitempty"`
}

// AccountsCheckResponse mirrors the accounts/check payload.
type AccountsCheckResponse struct {
	Accounts         []AccountsCheckEntry
	AccountOrdering  []string
	DefaultAccountID *string
}

// UnmarshalJSON mirrors Rust's RawAccountsCheckResponse: `accounts` is either a
// list of entries or a map keyed by account id (ordered by account_ordering).
func (r *AccountsCheckResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		Accounts         json.RawMessage `json:"accounts"`
		AccountOrdering  []string        `json:"account_ordering"`
		DefaultAccountID *string         `json:"default_account_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.AccountOrdering = raw.AccountOrdering
	r.DefaultAccountID = raw.DefaultAccountID
	r.Accounts = nil
	if len(raw.Accounts) == 0 {
		return nil
	}
	var list []AccountsCheckEntry
	if err := json.Unmarshal(raw.Accounts, &list); err == nil {
		r.Accounts = list
		return nil
	}
	var mapped map[string]struct {
		Account struct {
			AccountID         *string `json:"account_id"`
			PlanType          *string `json:"plan_type"`
			Name              *string `json:"name"`
			ProfilePictureURL *string `json:"profile_picture_url"`
			Structure         string  `json:"structure"`
		} `json:"account"`
	}
	if err := json.Unmarshal(raw.Accounts, &mapped); err != nil {
		return err
	}
	entries := make([]AccountsCheckEntry, 0, len(mapped))
	for _, accountID := range raw.AccountOrdering {
		entry, ok := mapped[accountID]
		if !ok || entry.Account.AccountID == nil || *entry.Account.AccountID == "" {
			continue
		}
		entries = append(entries, AccountsCheckEntry{
			ID:                *entry.Account.AccountID,
			PlanType:          entry.Account.PlanType,
			Name:              entry.Account.Name,
			ProfilePictureURL: entry.Account.ProfilePictureURL,
			Structure:         entry.Account.Structure,
		})
	}
	r.Accounts = entries
	return nil
}

// GetAccountsCheck fetches the account routing metadata for the current
// credential (Rust BackendClient::get_accounts_check).
func (c *CloudClient) GetAccountsCheck(ctx context.Context) (*AccountsCheckResponse, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var payload AccountsCheckResponse
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("accounts", "check"), nil, nil, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}
