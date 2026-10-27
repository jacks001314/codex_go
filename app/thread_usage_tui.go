package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	codextea "codex_go/tui/tea"
)

const interactiveThreadUsageConnectionID = "local-tui-thread-usage"

func interactiveLocalThreadUsageReader() codextea.ThreadUsageReaderFunc {
	return func(threadID string) (codextea.ThreadUsageReadResult, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return codextea.ThreadUsageReadResult{}, errors.New("thread usage requires a thread id")
		}
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		defer router.Close()
		if err := initializeLocalTUIConnection(router.Handle, interactiveThreadUsageConnectionID); err != nil {
			return codextea.ThreadUsageReadResult{}, err
		}
		params, err := json.Marshal(auth.GetAccountTokenUsageParams{ThreadID: &threadID})
		if err != nil {
			return codextea.ThreadUsageReadResult{}, err
		}
		response := router.Handle(&appserver.Request{
			JSONRPC:      "2.0",
			ID:           appserver.IntID(2),
			Method:       appserver.MethodGetAccountTokenUsage,
			Params:       params,
			ConnectionID: interactiveThreadUsageConnectionID,
		})
		if response == nil {
			return codextea.ThreadUsageReadResult{}, errors.New("account/usage/read returned no response")
		}
		if response.Error != nil {
			return codextea.ThreadUsageReadResult{}, errors.New(strings.TrimSpace(response.Error.Message))
		}
		result, ok := response.Result.(*auth.GetAccountTokenUsageResponse)
		if !ok || result == nil {
			return codextea.ThreadUsageReadResult{}, errors.New("account/usage/read returned an invalid response")
		}
		return threadUsageReadResultFromAuth(result), nil
	}
}

func interactiveRemoteThreadUsageReader(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ThreadUsageReaderFunc {
	return func(threadID string) (codextea.ThreadUsageReadResult, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return codextea.ThreadUsageReadResult{}, errors.New("thread usage requires a thread id")
		}
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return codextea.ThreadUsageReadResult{}, err
		}
		defer client.close()
		var response auth.GetAccountTokenUsageResponse
		params := auth.GetAccountTokenUsageParams{ThreadID: &threadID}
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodGetAccountTokenUsage, params, &response); err != nil {
			return codextea.ThreadUsageReadResult{}, err
		}
		return threadUsageReadResultFromAuth(&response), nil
	}
}

func threadUsageReadResultFromAuth(response *auth.GetAccountTokenUsageResponse) codextea.ThreadUsageReadResult {
	if response == nil || response.ThreadUsage == nil {
		return codextea.ThreadUsageReadResult{Outcome: codextea.ThreadUsageDisabled}
	}
	usage := response.ThreadUsage
	out := codextea.ThreadUsageEstimate{
		ThreadID:                    strings.TrimSpace(usage.ThreadID),
		EstimatedUsageCreditsMicros: usage.EstimatedUsageCreditsMicros,
	}
	if usage.EstimatedUsageUSDMicros != nil {
		value := *usage.EstimatedUsageUSDMicros
		out.EstimatedUsageUSDMicros = &value
	}
	if usage.NativeUsageUSDMicros != nil {
		value := *usage.NativeUsageUSDMicros
		out.NativeUsageUSDMicros = &value
	}
	if len(usage.Groups) > 0 {
		out.Groups = make([]codextea.ThreadUsageGroup, 0, len(usage.Groups))
		for _, group := range usage.Groups {
			out.Groups = append(out.Groups, threadUsageGroupFromAuth(group))
		}
	}
	return codextea.ThreadUsageReadResult{Outcome: codextea.ThreadUsageAvailable, Usage: out}
}

func threadUsageGroupFromAuth(group auth.ThreadUsageBreakdownGroup) codextea.ThreadUsageGroup {
	out := codextea.ThreadUsageGroup{
		Model:                       cloneStringPointer(group.Model),
		ReasoningEffort:             cloneStringPointer(group.ReasoningEffort),
		Speed:                       cloneStringPointer(group.Speed),
		EstimatedUsageCreditsMicros: group.EstimatedUsageCreditsMicros,
		CachedInputTokens:           cloneInt64Pointer(group.CachedInputTokens),
		InputTokens:                 cloneInt64Pointer(group.InputTokens),
		OutputTokens:                cloneInt64Pointer(group.OutputTokens),
	}
	return out
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
