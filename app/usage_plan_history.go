package app

import (
	"context"
	"net/http"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/tui/analytics"
)

// interactivePlanHistoryReader reads seven days of consumer allowance history
// for /usage plan (Rust #45766). Non-ChatGPT accounts and absent routes yield
// no report; the TUI renders the unavailable line instead of failing.
func interactivePlanHistoryReader(root *cli.RootOptions) func() (*analytics.Report, error) {
	return func() (*analytics.Report, error) {
		loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
		if err != nil {
			return nil, err
		}
		store := auth.NewStore(auth.DefaultCodexHome())
		resolved, err := store.Resolve()
		if err != nil || resolved == nil || resolved.Auth.Mode() != "chatgpt" {
			return nil, nil
		}
		_, token, ok := securitySetupIdentity(&resolved.Auth)
		if !ok {
			return nil, nil
		}
		headers := http.Header{"Authorization": []string{"Bearer " + token}}
		if id := auth.AccountIDFromAuthForRestrictions(&resolved.Auth); id != "" {
			headers.Set("ChatGPT-Account-ID", id)
		}
		client := chatgptapi.NewCloudClientWithoutRedirects(&chatgptapi.CloudClientOptions{
			BaseURL: loaded.ChatGPTBaseURL(),
			Headers: headers,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		history, err := client.GetPlanLimitHistory(ctx)
		if err != nil {
			return nil, err
		}
		return analytics.ParsePlanHistory(history)
	}
}
