package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// Rust parity: codex-rs/tui/src/backend_banners.rs wiring. The backend owns
// eligibility and CTA copy through the existing account usage read; the app
// validates the payload, resolves each CTA, and hands the TUI a render-ready
// banner view.

// interactivePlanType reads the signed-in account's plan type, which CTA
// resolution needs for workspace/pricing destinations (Rust startup's
// bootstrap.plan_type).
func interactivePlanType() auth.PlanType {
	resolved, err := auth.NewStore(auth.DefaultCodexHome()).Resolve()
	if err != nil || resolved == nil {
		return auth.PlanUnknown
	}
	account := auth.AccountFromAuth(&resolved.Auth)
	if account == nil {
		return auth.PlanUnknown
	}
	return account.PlanType
}

// interactiveBackendBannerView converts an account usage read into the TUI's
// inline banner view (Rust BackendBanner::actionable_banner). Unsupported or
// unrenderable payloads yield nil so the previous surface is left alone.
func interactiveBackendBannerView(response *auth.GetAccountRateLimitsResponse, planType auth.PlanType, now time.Time) *codextea.BackendBannerView {
	if response == nil || len(response.RateLimitUpsell) == 0 {
		return nil
	}
	if trimmed := strings.TrimSpace(string(response.RateLimitUpsell)); trimmed == "" || trimmed == "null" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(response.RateLimitUpsell, &raw); err != nil {
		return nil
	}
	banner, ok := ParseBackendBanner(raw)
	if !ok {
		return nil
	}
	// The account identity comes from the usage read, not the payload.
	if response.AccountID != nil {
		banner.AccountID = strings.TrimSpace(*response.AccountID)
	}
	banner.PlanType = planType
	view := &codextea.BackendBannerView{
		Title:       banner.BannerCopy(banner.Title, now),
		Description: banner.BannerCopy(banner.Description, now),
		Dismissible: banner.Dismissible(),
	}
	for _, action := range banner.ActionableActions() {
		view.Actions = append(view.Actions, codextea.BackendBannerAction{
			Label:      action.Label,
			Kind:       backendBannerActionKind(action.Action.Kind),
			URL:        action.Action.URL,
			CreditType: action.Action.CreditType,
		})
	}
	return view
}

func backendBannerActionKind(kind BannerActionKind) codextea.BackendBannerActionKind {
	switch kind {
	case BannerActionOpenURL:
		return codextea.BannerActionOpenURL
	case BannerActionNotifyOwner:
		return codextea.BannerActionNotifyOwner
	case BannerActionResetUsage:
		return codextea.BannerActionResetUsage
	default:
		return ""
	}
}

func interactiveLocalBackendBannerReader() func() (*codextea.BackendBannerView, error) {
	return func() (*codextea.BackendBannerView, error) {
		response, err := interactiveLocalUsageRead()
		if err != nil {
			return nil, err
		}
		return interactiveBackendBannerView(response, interactivePlanType(), time.Now()), nil
	}
}

func interactiveRemoteBackendBannerReader(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func() (*codextea.BackendBannerView, error) {
	return func() (*codextea.BackendBannerView, error) {
		response, err := interactiveRemoteUsageRead(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		return interactiveBackendBannerView(response, interactivePlanType(), time.Now()), nil
	}
}

// interactiveBackendBannerActionHandler dispatches a selected CTA. Open-URL
// actions open the validated destination; notify-owner actions send the
// credits nudge email. The reset-usage CTA is handled inside the TUI model.
func interactiveBackendBannerActionHandler(sendNudge func(chatwidget.AddCreditsNudgeCreditType) error) func(codextea.BackendBannerAction) bubbletea.Cmd {
	return func(action codextea.BackendBannerAction) bubbletea.Cmd {
		switch action.Kind {
		case codextea.BannerActionOpenURL:
			target := strings.TrimSpace(action.URL)
			if target == "" {
				return nil
			}
			return func() bubbletea.Msg {
				if err := auth.OpenBrowser(target); err != nil {
					return codextea.StatusMsg{Status: "Could not open the link: " + err.Error()}
				}
				return nil
			}
		case codextea.BannerActionNotifyOwner:
			if sendNudge == nil {
				return nil
			}
			creditType := action.CreditType
			return func() bubbletea.Msg {
				if err := sendNudge(creditType); err != nil {
					return codextea.StatusMsg{Status: "Could not notify the workspace owner: " + err.Error()}
				}
				return codextea.StatusMsg{Status: "Requested a workspace owner notification."}
			}
		}
		return nil
	}
}

func interactiveLocalAddCreditsNudgeSender() func(chatwidget.AddCreditsNudgeCreditType) error {
	return func(creditType chatwidget.AddCreditsNudgeCreditType) error {
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		defer router.Close()
		if err := initializeLocalTUIConnection(router.Handle, interactiveStatusConnectionID); err != nil {
			return err
		}
		raw, err := json.Marshal(auth.SendAddCreditsNudgeEmailParams{
			CreditType: auth.AddCreditsNudgeCreditType(creditType),
		})
		if err != nil {
			return err
		}
		response := router.Handle(&appserver.Request{
			JSONRPC:      "2.0",
			ID:           appserver.IntID(3),
			Method:       appserver.MethodSendAddCreditsNudgeEmail,
			Params:       raw,
			ConnectionID: interactiveStatusConnectionID,
		})
		if response == nil {
			return errors.New("account/sendAddCreditsNudgeEmail returned no response")
		}
		if response.Error != nil {
			return errors.New(strings.TrimSpace(response.Error.Message))
		}
		return nil
	}
}

func interactiveRemoteAddCreditsNudgeSender(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func(chatwidget.AddCreditsNudgeCreditType) error {
	return func(creditType chatwidget.AddCreditsNudgeCreditType) error {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		params := auth.SendAddCreditsNudgeEmailParams{CreditType: auth.AddCreditsNudgeCreditType(creditType)}
		var response auth.SendAddCreditsNudgeEmailResponse
		return remoteSessionRequest(reqCtx, client, appserver.MethodSendAddCreditsNudgeEmail, params, &response)
	}
}
