package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	codextea "codex_go/tui/tea"
)

const interactiveGoalConnectionID = "local-tui-goal"

type interactiveGoalRouter interface {
	Handle(request *appserver.Request) *appserver.Response
	Close() error
}

type interactiveGoalRouterFactory func() interactiveGoalRouter

func interactiveLocalGoalCallbacks(factory interactiveGoalRouterFactory) (codextea.GoalReaderFunc, codextea.GoalSetterFunc, codextea.GoalClearerFunc) {
	if factory == nil {
		factory = func() interactiveGoalRouter {
			return appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		}
	}
	read := func(threadID string) (*appserver.Goal, error) {
		router := factory()
		if router == nil {
			return nil, errors.New("thread/goal/get failed in TUI: app-server is unavailable")
		}
		defer router.Close()
		response, err := localGoalRequest(router, appserver.IntID(1), appserver.MethodThreadGoalGet, appserver.GoalGetParams{ThreadID: strings.TrimSpace(threadID)})
		if err != nil {
			return nil, err
		}
		result, ok := response.(*appserver.GoalGetResponse)
		if !ok || result == nil {
			return nil, errors.New("thread/goal/get failed in TUI: invalid response")
		}
		return result.Goal, nil
	}
	set := func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
		router := factory()
		if router == nil {
			return appserver.Goal{}, errors.New("thread/goal/set failed in TUI: app-server is unavailable")
		}
		defer router.Close()
		params := appserver.GoalSetParams{
			ThreadID:    strings.TrimSpace(threadID),
			Objective:   trimStringPtrRemote(objective),
			TokenBudget: cloneInt64PtrRemote(tokenBudget),
			Status:      cloneGoalStatusPtrRemote(status),
		}
		if tokenBudget != nil {
			params.TokenBudgetSet = true
		}
		response, err := localGoalRequest(router, appserver.IntID(2), appserver.MethodThreadGoalSet, params)
		if err != nil {
			return appserver.Goal{}, err
		}
		result, ok := response.(*appserver.GoalSetResponse)
		if !ok || result == nil {
			return appserver.Goal{}, errors.New("thread/goal/set failed in TUI: invalid response")
		}
		return result.Goal, nil
	}
	clear := func(threadID string) (bool, error) {
		router := factory()
		if router == nil {
			return false, errors.New("thread/goal/clear failed in TUI: app-server is unavailable")
		}
		defer router.Close()
		response, err := localGoalRequest(router, appserver.IntID(3), appserver.MethodThreadGoalClear, appserver.GoalClearParams{ThreadID: strings.TrimSpace(threadID)})
		if err != nil {
			return false, err
		}
		result, ok := response.(*appserver.GoalClearResponse)
		if !ok || result == nil {
			return false, errors.New("thread/goal/clear failed in TUI: invalid response")
		}
		return result.Cleared, nil
	}
	return read, set, clear
}

func localGoalRequest(router interactiveGoalRouter, id appserver.RequestID, method appserver.Method, params any) (any, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%s failed in TUI: %w", method, err)
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           id,
		Method:       method,
		Params:       raw,
		ConnectionID: interactiveGoalConnectionID,
	})
	if response == nil {
		return nil, fmt.Errorf("%s failed in TUI: no response", method)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s failed in TUI: %s", method, strings.TrimSpace(response.Error.Message))
	}
	return response.Result, nil
}
