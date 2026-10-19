package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/cli"
	"codex_go/config"
	codextea "codex_go/tui/tea"
)

// interactiveLocalSecuritySetupReader mirrors Rust #49715: only local ChatGPT
// accounts using the OpenAI provider are eligible, and the saved credentials
// must still match after the read.
func interactiveLocalSecuritySetupReader(root *cli.RootOptions) func() (string, *codextea.SecuritySetupNotice, error) {
	return func() (string, *codextea.SecuritySetupNotice, error) {
		loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
		if err != nil {
			return "", nil, err
		}
		store := auth.NewStore(auth.DefaultCodexHome())
		resolved, err := store.Resolve()
		if err != nil || resolved == nil || resolved.Auth.BackendMode() != "chatgpt" ||
			auth.IsFedrampAccount(&resolved.Auth) {
			return "", nil, nil
		}
		identity, token, ok := securitySetupIdentity(&resolved.Auth)
		if !ok {
			return "", nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		headers := http.Header{"Authorization": []string{"Bearer " + token}}
		if accountID := auth.AccountIDFromAuthForRestrictions(&resolved.Auth); accountID != "" {
			headers.Set("ChatGPT-Account-ID", accountID)
		}
		client := chatgptapi.NewCloudClientWithoutRedirects(&chatgptapi.CloudClientOptions{
			BaseURL: loaded.ChatGPTBaseURL(),
			Headers: headers,
		})
		notice, err := client.GetSecuritySetup(ctx)
		if err != nil || notice == nil {
			return identity, nil, nil
		}
		current, err := store.Resolve()
		if err != nil || current == nil {
			return "", nil, nil
		}
		currentIdentity, _, ok := securitySetupIdentity(&current.Auth)
		if !ok || currentIdentity != identity {
			return "", nil, nil
		}
		return identity, &codextea.SecuritySetupNotice{
			Title:       notice.Title,
			Description: notice.Description,
			ActionLabel: notice.Action.Label,
			ActionURL:   notice.Action.URL,
		}, nil
	}
}

func securitySetupIdentity(snapshot *auth.AuthDotJSON) (string, string, bool) {
	if snapshot == nil {
		return "", "", false
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	userID := strings.TrimSpace(auth.ChatGPTUserIDFromAuth(snapshot))
	token, _ := snapshot.Tokens["access_token"].(string)
	token = strings.TrimSpace(token)
	if accountID == "" || userID == "" || token == "" {
		return "", "", false
	}
	return accountID + "|" + userID, token, true
}
