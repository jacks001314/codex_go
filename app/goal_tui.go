package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/doctor"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

const interactiveGoalConnectionID = "local-tui-goal"

type interactiveGoalRouter interface {
	Handle(request *appserver.Request) *appserver.Response
	Close() error
}

type interactiveGoalRouterFactory func() interactiveGoalRouter

func interactiveLocalGoalCallbacks(factory interactiveGoalRouterFactory) (codextea.GoalReaderFunc, codextea.GoalSetterFunc, codextea.GoalClearerFunc, codextea.GoalEditTextFunc, codextea.GoalDraftMaterializeFunc) {
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
		if err := initializeLocalTUIConnection(router.Handle, interactiveGoalConnectionID); err != nil {
			return nil, err
		}
		response, err := localGoalRequest(router, appserver.IntID(2), appserver.MethodThreadGoalGet, appserver.GoalGetParams{ThreadID: strings.TrimSpace(threadID)})
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
		codexHome, err := initializeLocalGoalConnection(router)
		if err != nil {
			return appserver.Goal{}, err
		}
		params := appserver.GoalSetParams{
			ThreadID:    strings.TrimSpace(threadID),
			Objective:   trimStringPtrRemote(objective),
			TokenBudget: cloneInt64PtrRemote(tokenBudget),
			Status:      cloneGoalStatusPtrRemote(status),
		}
		if tokenBudget != nil {
			params.TokenBudgetSet = true
		}
		if params.Objective != nil {
			// Materialize oversized objectives into managed goal files before
			// persisting, mirroring Rust goal_files::materialize_goal_draft.
			materialized, materializeErr := materializeOversizedGoalObjective(
				localGoalFS(router),
				codexHome,
				*params.Objective,
			)
			if materializeErr != nil {
				return appserver.Goal{}, materializeErr
			}
			params.Objective = &materialized
		}
		response, err := localGoalRequest(router, appserver.IntID(3), appserver.MethodThreadGoalSet, params)
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
		if err := initializeLocalTUIConnection(router.Handle, interactiveGoalConnectionID); err != nil {
			return false, err
		}
		response, err := localGoalRequest(router, appserver.IntID(4), appserver.MethodThreadGoalClear, appserver.GoalClearParams{ThreadID: strings.TrimSpace(threadID)})
		if err != nil {
			return false, err
		}
		result, ok := response.(*appserver.GoalClearResponse)
		if !ok || result == nil {
			return false, errors.New("thread/goal/clear failed in TUI: invalid response")
		}
		return result.Cleared, nil
	}
	editText := func(threadID string, objective string) (string, error) {
		router := factory()
		if router == nil {
			return objective, nil
		}
		defer router.Close()
		codexHome, err := initializeLocalGoalConnection(router)
		if err != nil {
			return objective, err
		}
		return resolveGoalObjectiveText(localGoalFS(router), codexHome, objective)
	}
	materialize := func(draft codextui.GoalDraft) (string, error) {
		router := factory()
		if router == nil {
			return "", errors.New("goal draft materialization failed in TUI: app-server is unavailable")
		}
		defer router.Close()
		codexHome, err := initializeLocalGoalConnection(router)
		if err != nil {
			return "", err
		}
		return materializeGoalDraft(localGoalFS(router), codexHome, draft)
	}
	return read, set, clear, editText, materialize
}

// initializeLocalGoalConnection performs the initialize handshake and returns
// the app-server's codex home so goal files materialize into the same home the
// thread store uses.
func initializeLocalGoalConnection(router interactiveGoalRouter) (string, error) {
	if router == nil {
		return "", errors.New("initialize failed in TUI: app-server is unavailable")
	}
	raw, err := json.Marshal(appserver.InitializeParams{
		ClientInfo: appserver.ClientInfo{
			Name:    "codex_go_tui",
			Version: doctor.Version(),
		},
		Capabilities: &appserver.InitializeCapabilities{
			ExperimentalAPI:                true,
			MCPServerOpenAIFormElicitation: true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("initialize failed in TUI: %w", err)
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(1),
		Method:       appserver.MethodInitialize,
		Params:       raw,
		ConnectionID: interactiveGoalConnectionID,
	})
	if response == nil {
		return "", errors.New("initialize failed in TUI: no response")
	}
	if response.Error != nil {
		return "", fmt.Errorf("initialize failed in TUI: %s", strings.TrimSpace(response.Error.Message))
	}
	result, ok := response.Result.(*appserver.InitializeResponse)
	if !ok || result == nil {
		return "", errors.New("initialize failed in TUI: invalid response")
	}
	return strings.TrimSpace(result.CodexHome), nil
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
