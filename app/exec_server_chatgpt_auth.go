package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	codexnetwork "codex_go/network"
)

const execServerChatGPTPolicyTimeout = 15 * time.Second

var (
	execServerInvalidChatGPTToken     = errors.New("invalid ChatGPT access token")
	execServerInvalidChatGPTAccountID = errors.New("invalid ChatGPT account ID")
	execServerChatGPTAccountMissing   = errors.New("token has no ChatGPT account ID; supply --chatgpt-account-id")
)

type execServerChatGPTClaims struct {
	Auth *struct {
		AccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

func execServerChatGPTAuthFromToken(token string, accountID string, accountIDSet bool) (*auth.AuthDotJSON, error) {
	claims, err := parseExecServerChatGPTClaims(token)
	if err != nil {
		return nil, execServerInvalidChatGPTToken
	}
	if accountIDSet && !isExecServerGraphicToken(accountID) {
		return nil, execServerInvalidChatGPTAccountID
	}
	claimedID := claims.AuthAccountID()
	if accountIDSet && claimedID != "" && accountID != claimedID {
		return nil, errors.New("--chatgpt-account-id does not match the token")
	}
	var planOverride *string
	if claimedID == "" {
		// Without an account claim the token cannot establish the supplied
		// account's plan, so policy lookup must treat it as unknown.
		unknown := string(auth.PlanUnknown)
		planOverride = &unknown
	}
	if !accountIDSet {
		accountID = claimedID
	}
	if accountID == "" {
		return nil, execServerChatGPTAccountMissing
	}
	if !isExecServerGraphicToken(accountID) {
		return nil, execServerInvalidChatGPTAccountID
	}
	snapshot := auth.FromChatGPTAuthTokens(token, accountID, planOverride)
	return &snapshot, nil
}

func (c *execServerChatGPTClaims) AuthAccountID() string {
	if c == nil || c.Auth == nil {
		return ""
	}
	return c.Auth.AccountID
}

func parseExecServerChatGPTClaims(token string) (*execServerChatGPTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, execServerInvalidChatGPTToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, execServerInvalidChatGPTToken
	}
	var claims execServerChatGPTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, execServerInvalidChatGPTToken
	}
	return &claims, nil
}

func isExecServerGraphicToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7E {
			return false
		}
	}
	return true
}

// validateExecServerCredentialHTTPSURL mirrors Rust's
// validate_https_endpoint_url: credential-bearing endpoints must be HTTPS with
// a host and without userinfo or a fragment. Errors never echo the URL.
func validateExecServerCredentialHTTPSURL(value string, field string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Scheme != "https" {
		return fmt.Errorf("%s URLs must use HTTPS without userinfo or fragments", field)
	}
	return nil
}

func execServerChatGPTAuthAllowed(cfg *config.Config, snapshot *auth.AuthDotJSON) bool {
	if cfg == nil || snapshot == nil || !cfg.IsLoginMethodAllowed(config.ForcedLoginMethodChatGPT) {
		return false
	}
	workspaces, restricted := cfg.ManagedAuthPolicy().EffectiveChatGPTWorkspaces(cfg.ForcedChatGPTWorkspaceIDs())
	if !restricted {
		return true
	}
	return auth.EnsureWorkspaceAccountAllowed(workspaces, auth.AccountIDFromAuthForRestrictions(snapshot)) == nil
}

func execServerPlanRequiresManagedConfig(plan auth.PlanType) bool {
	return plan.IsBusinessLike() ||
		plan.IsEducationLike() ||
		plan == auth.PlanEnterprise ||
		plan == auth.PlanSci ||
		plan == auth.PlanSciPlus
}

// managedConfigBundleLoaderForSuppliedAuth mirrors Rust #59569's
// managed_config_bundle_loader_for_auth. A configured customer-hosted policy
// server keeps its own OAuth credentials; otherwise only a known personal plan
// may skip the ChatGPT policy lookup. Missing and unknown plans must query the
// supplied account's policy.
func managedConfigBundleLoaderForSuppliedAuth(ctx context.Context, codexHome string, bootstrap *config.Config, snapshot *auth.AuthDotJSON) (*config.CloudConfigLoader, error) {
	return managedConfigBundleLoaderForSuppliedAuthWithClient(ctx, codexHome, bootstrap, snapshot, nil)
}

func managedConfigBundleLoaderForSuppliedAuthWithClient(ctx context.Context, codexHome string, bootstrap *config.Config, snapshot *auth.AuthDotJSON, client *http.Client) (*config.CloudConfigLoader, error) {
	if bootstrap == nil {
		return nil, nil
	}
	if bootstrap.RequiresRemoteConfig() {
		return configServerCloudConfigBundleLoaderForRun(ctx, codexHome, bootstrap, bootstrap.Requirements.ConfigServer)
	}
	if snapshot == nil || snapshot.BackendMode() != "chatgpt" {
		return nil, errors.New("ChatGPT authentication is required for the supplied token")
	}
	rawPlan := auth.ChatGPTPlanTypeRawFromAuth(snapshot)
	plan := auth.PlanTypeFromString(rawPlan)
	if strings.TrimSpace(rawPlan) != "" && plan != auth.PlanUnknown && !execServerPlanRequiresManagedConfig(plan) {
		return nil, nil
	}
	if err := validateExecServerCredentialHTTPSURL(bootstrap.ChatGPTBaseURL(), "ChatGPT policy endpoint"); err != nil {
		return nil, err
	}
	authHeaders, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil, err
	}
	var fallback config.CloudConfigHTTPDoer
	if client == nil {
		client = codexnetwork.NewHTTPClient(bootstrap.RespectSystemProxyEnabled(), 0)
		fallback = bootstrapFallbackHTTPClient(bootstrap, 0)
	}
	return config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		loadCtx, cancel := context.WithTimeout(ctx, execServerChatGPTPolicyTimeout)
		defer cancel()
		policy := publishApplicationNetworkPolicy(bootstrap)
		return config.LoadCloudConfigBundle(loadCtx, config.CloudConfigFetchOptions{
			CodexHome:          codexHome,
			BaseURL:            bootstrap.ChatGPTBaseURL(),
			ChatGPTUserID:      auth.ChatGPTUserIDFromAuth(snapshot),
			AccountID:          auth.AccountIDFromAuthForRestrictions(snapshot),
			HTTPClient:         client,
			FallbackHTTPClient: fallback,
			NetworkPolicy:      &policy,
			NoCache:            true,
			Authorize: func(requestCtx context.Context, request *http.Request) error {
				return authHeaders.Apply(requestCtx, request, nil)
			},
		})
	}), nil
}

func monitorExecServerStdinClose(reader io.Reader) <-chan struct{} {
	closed := make(chan struct{})
	go func() {
		if reader != nil {
			_, _ = io.Copy(io.Discard, reader)
		}
		close(closed)
	}()
	return closed
}

func execServerContextWithStdinClose(ctx context.Context, closed <-chan struct{}) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-closed:
			cancel()
		case <-runCtx.Done():
		}
	}()
	return runCtx, cancel
}

func execServerStdinClosed(closed <-chan struct{}) bool {
	if closed == nil {
		return false
	}
	select {
	case <-closed:
		return true
	default:
		return false
	}
}
