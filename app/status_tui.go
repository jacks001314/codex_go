package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

const interactiveStatusConnectionID = "local-tui-status"

func interactiveLocalRateLimitsReader() func() ([]codextui.RateLimitStatus, error) {
	return func() ([]codextui.RateLimitStatus, error) {
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		defer router.Close()
		raw, err := json.Marshal(map[string]any{})
		if err != nil {
			return nil, err
		}
		response := router.Handle(&appserver.Request{
			JSONRPC:      "2.0",
			ID:           appserver.IntID(1),
			Method:       appserver.MethodGetAccountRateLimits,
			Params:       raw,
			ConnectionID: interactiveStatusConnectionID,
		})
		if response == nil {
			return nil, errors.New("account/rateLimits/read returned no response")
		}
		if response.Error != nil {
			return nil, errors.New(strings.TrimSpace(response.Error.Message))
		}
		result, ok := response.Result.(*auth.GetAccountRateLimitsResponse)
		if !ok || result == nil {
			return nil, errors.New("account/rateLimits/read returned an invalid response")
		}
		return interactiveRateLimitStatuses(result), nil
	}
}

func interactiveRemoteRateLimitsReader(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func() ([]codextui.RateLimitStatus, error) {
	return func() ([]codextui.RateLimitStatus, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		var response auth.GetAccountRateLimitsResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodGetAccountRateLimits, map[string]any{}, &response); err != nil {
			return nil, err
		}
		return interactiveRateLimitStatuses(&response), nil
	}
}

func interactiveRateLimitStatuses(response *auth.GetAccountRateLimitsResponse) []codextui.RateLimitStatus {
	if response == nil {
		return nil
	}
	snapshot := response.RateLimits
	statuses := make([]codextui.RateLimitStatus, 0, 2)
	if snapshot.Primary != nil {
		statuses = append(statuses, codextui.RateLimitStatus{
			Label:       chatwidget.LimitLabelForWindow(snapshot.Primary.WindowDurationMins, false),
			UsedPercent: float64(snapshot.Primary.UsedPercent),
		})
	}
	if snapshot.Secondary != nil {
		statuses = append(statuses, codextui.RateLimitStatus{
			Label:       chatwidget.LimitLabelForWindow(snapshot.Secondary.WindowDurationMins, true),
			UsedPercent: float64(snapshot.Secondary.UsedPercent),
		})
	}
	return statuses
}
