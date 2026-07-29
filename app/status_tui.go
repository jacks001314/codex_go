package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	statusui "codex_go/tui/status"
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
	capturedAt := time.Now()
	snapshots := make([]auth.RateLimitSnapshot, 0, max(1, len(response.RateLimitsByLimitID)))
	if len(response.RateLimitsByLimitID) == 0 {
		snapshots = append(snapshots, response.RateLimits)
	} else {
		keys := make([]string, 0, len(response.RateLimitsByLimitID))
		for key := range response.RateLimitsByLimitID {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			snapshots = append(snapshots, response.RateLimitsByLimitID[key])
		}
	}

	statuses := make([]codextui.RateLimitStatus, 0, len(snapshots)*3)
	for _, snapshot := range snapshots {
		limitName := "codex"
		if snapshot.LimitName != nil && strings.TrimSpace(*snapshot.LimitName) != "" {
			limitName = strings.TrimSpace(*snapshot.LimitName)
		} else if snapshot.LimitID != nil && strings.TrimSpace(*snapshot.LimitID) != "" {
			limitName = strings.TrimSpace(*snapshot.LimitID)
		}
		windowCount := 0
		if snapshot.Primary != nil {
			windowCount++
		}
		if snapshot.Secondary != nil {
			windowCount++
		}
		showPrefix := !strings.EqualFold(limitName, "codex")
		combinePrefix := showPrefix && windowCount == 1
		if showPrefix && !combinePrefix {
			statuses = append(statuses, codextui.RateLimitStatus{Label: limitName + " limit", IsText: true, CapturedAt: capturedAt})
		}
		if snapshot.Primary != nil {
			label := capitalizeStatusLabel(chatwidget.LimitLabelForWindow(snapshot.Primary.WindowDurationMins, false)) + " limit"
			if combinePrefix {
				label = limitName + " " + label
			}
			statuses = append(statuses, interactiveRateLimitWindow(label, snapshot.Primary, capturedAt))
		}
		if snapshot.Secondary != nil {
			label := capitalizeStatusLabel(chatwidget.LimitLabelForWindow(snapshot.Secondary.WindowDurationMins, true)) + " limit"
			if combinePrefix {
				label = limitName + " " + label
			}
			statuses = append(statuses, interactiveRateLimitWindow(label, snapshot.Secondary, capturedAt))
		}
		if credits := snapshot.Credits; credits != nil && credits.HasCredits {
			if credits.Unlimited {
				statuses = append(statuses, codextui.RateLimitStatus{Label: "Credits", Text: "Unlimited", IsText: true, CapturedAt: capturedAt})
			} else if credits.Balance != nil {
				if balance, ok := statusui.FormatCreditBalance(*credits.Balance); ok {
					statuses = append(statuses, codextui.RateLimitStatus{Label: "Credits", Text: balance + " credits", IsText: true, CapturedAt: capturedAt})
				}
			}
		}
		if individual := snapshot.IndividualLimit; individual != nil {
			reset := time.Unix(individual.ResetsAt, 0)
			statuses = append(statuses, codextui.RateLimitStatus{
				Label: "Monthly credit limit", UsedPercent: 100 - float64(individual.RemainingPercent),
				ResetsAt: &reset, CapturedAt: capturedAt,
				Details: individual.Used + " of " + individual.Limit + " credits used",
			})
		}
	}
	return statuses
}

func interactiveRateLimitWindow(label string, window *auth.RateLimitWindow, capturedAt time.Time) codextui.RateLimitStatus {
	status := codextui.RateLimitStatus{Label: label, UsedPercent: float64(window.UsedPercent), CapturedAt: capturedAt}
	if window.ResetsAt != nil {
		reset := time.Unix(*window.ResetsAt, 0)
		status.ResetsAt = &reset
	}
	return status
}

func capitalizeStatusLabel(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}
