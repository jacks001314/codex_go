package appserver

import (
	"strings"

	"codex_go/auth"
)

// authStorageOriginatorForRequest mirrors Rust's per-request
// AuthStorageOriginator scope: credential-storage attribution comes from the
// connection's initialized client name instead of process-global state, so
// concurrent app-server clients never borrow each other's identity.
func (r *RuntimeRouter) authStorageOriginatorForRequest(request *Request) string {
	if r == nil || request == nil {
		return ""
	}
	return strings.TrimSpace(r.clientInfoForConnection(request.normalizedConnectionID()).Name)
}

func (r *RuntimeRouter) authStoreOptionsForOriginator(originator string) *auth.StoreOptions {
	options := r.authStoreOptions()
	options.Originator = strings.TrimSpace(originator)
	return options
}

func (r *RuntimeRouter) authStoreOptionsForRequest(request *Request) *auth.StoreOptions {
	return r.authStoreOptionsForOriginator(r.authStorageOriginatorForRequest(request))
}

func (r *RuntimeRouter) authStoreForRequest(codexHome string, request *Request) *auth.Store {
	return auth.NewStoreWithOptions(codexHome, r.authStoreOptionsForRequest(request))
}

// accountOAuthOptionsForOriginator keeps the caller-supplied OAuth policy while
// binding credential storage to one client identity.
func (r *RuntimeRouter) accountOAuthOptionsForOriginator(originator string) *auth.OAuthOptions {
	options := r.accountOAuthOptions()
	storeOptions := auth.StoreOptions{}
	if options.StoreOptions != nil {
		storeOptions = *options.StoreOptions
	}
	storeOptions.Originator = strings.TrimSpace(originator)
	options.StoreOptions = &storeOptions
	return options
}

func (r *RuntimeRouter) accountOAuthOptionsForRequest(request *Request) *auth.OAuthOptions {
	return r.accountOAuthOptionsForOriginator(r.authStorageOriginatorForRequest(request))
}
