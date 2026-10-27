package app

import (
	"context"
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #57365: continue a safety block with a safer rewrite in a verified fork
// before the blocked turn. The fork keeps the source's draft and model settings
// and the rewrite is submitted with the safety_recourse_follow_up trigger.

// interactiveContinueSafetyRecourse forks a local saved thread before the
// blocked turn and prepares the chosen rewrite's chat.
func interactiveContinueSafetyRecourse(root *cli.RootOptions) codextea.SafetyRecourseContinueFunc {
	return func(threadID, turnID, prompt string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			threadID = strings.TrimSpace(threadID)
			turnID = strings.TrimSpace(turnID)
			prompt = strings.TrimSpace(prompt)
			if threadID == "" || turnID == "" || prompt == "" {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the blocked turn is unavailable"),
				}
			}
			store := newSessionStore()
			record, err := store.Read(session.ThreadID(threadID), true, true)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			if record == nil || record.Archived {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the blocked turn is no longer the latest turn"),
				}
			}
			if !latestTurnIsFailed(record.Metadata.RolloutTurns, turnID) {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the blocked turn is no longer the latest turn"),
				}
			}
			fork, err := store.Fork(session.ThreadID(threadID), session.ForkOptions{
				Mode:         session.ForkAll,
				BeforeTurnID: turnID,
			})
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			if fork == nil || fork.ForkedFromID != session.ThreadID(threadID) || forkIncludesBlockedTurn(fork.Metadata.RolloutTurns, turnID) {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the fork boundary could not be verified"),
				}
			}
			response, err := interactiveResumeSessionHandler(root)(codextui.SessionSelection{
				Kind: codextui.SessionSelectionResume,
				Target: codextui.SessionTarget{
					BackendID: codextui.ProcessBackendID(),
					ThreadID:  string(fork.ID),
				},
			})
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			return codextea.SafetyRecourseRewriteReadyMsg{
				SourceThreadID: threadID,
				Prompt:         prompt,
				Response:       response,
			}
		}
	}
}

func latestTurnIsFailed(turns []session.TurnSnapshot, turnID string) bool {
	if len(turns) == 0 {
		return false
	}
	latest := turns[len(turns)-1]
	return strings.TrimSpace(latest.ID) == strings.TrimSpace(turnID) &&
		strings.EqualFold(strings.TrimSpace(latest.Status), "failed")
}

// forkIncludesBlockedTurn reports whether the fork still carries the blocked
// turn at its end, which means the boundary was not applied.
func forkIncludesBlockedTurn(turns []session.TurnSnapshot, turnID string) bool {
	if len(turns) == 0 {
		return false
	}
	return strings.TrimSpace(turns[len(turns)-1].ID) == strings.TrimSpace(turnID)
}

// interactiveRemoteContinueSafetyRecourse forks through the app server before
// the blocked turn, verifies the boundary, and resumes the fork for display.
func interactiveRemoteContinueSafetyRecourse(
	ctx context.Context,
	endpoint *appserverdaemon.RemoteAppServerEndpoint,
	root *cli.RootOptions,
	state *codextui.State,
	backendID codextui.BackendID,
) codextea.SafetyRecourseContinueFunc {
	return func(threadID, turnID, prompt string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			threadID = strings.TrimSpace(threadID)
			turnID = strings.TrimSpace(turnID)
			prompt = strings.TrimSpace(prompt)
			if threadID == "" || turnID == "" || prompt == "" {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the blocked turn is unavailable"),
				}
			}
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			defer client.close()
			source, err := remoteTUIReadThread(ctx, client, threadID, false)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			if source.Ephemeral {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the blocked turn is no longer the latest turn"),
				}
			}
			sourceTurns, err := remoteThreadTurnsPage(ctx, client, threadID, 2)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			if !remoteBlockedTurnIsLatest(sourceTurns, turnID) {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the blocked turn is no longer the latest turn"),
				}
			}
			forkParams, err := remoteSafetyRecourseForkParams(root, state, threadID, turnID)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			var forkResponse appserver.ThreadForkResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadFork, forkParams, &forkResponse); err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			forkID := ""
			if forkResponse.Thread != nil {
				forkID = strings.TrimSpace(forkResponse.Thread.ID)
			}
			if forkID == "" || forkResponse.Thread.ForkedFromID == nil ||
				strings.TrimSpace(*forkResponse.Thread.ForkedFromID) != threadID {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the fork boundary could not be verified"),
				}
			}
			forkTurns, err := remoteThreadTurnsPage(ctx, client, forkID, 1)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			latestTurns, err := remoteThreadTurnsPage(ctx, client, threadID, 1)
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			if !remoteBlockedTurnIsLatest(latestTurns, turnID) || !remoteForkBoundaryExcludesTurn(forkTurns, sourceTurns, turnID) {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the fork boundary could not be verified"),
				}
			}
			response, err := interactiveRemoteResumeSessionHandler(ctx, endpoint, root, backendID)(codextui.SessionSelection{
				Kind: codextui.SessionSelectionResume,
				Target: codextui.SessionTarget{
					BackendID: backendID,
					ThreadID:  forkID,
				},
			})
			if err != nil {
				return codextea.SafetyRecourseRewriteFailedMsg{SourceThreadID: threadID, Prompt: prompt, Err: err}
			}
			if response.ReadOnly {
				return codextea.SafetyRecourseRewriteFailedMsg{
					SourceThreadID: threadID,
					Prompt:         prompt,
					Err:            errors.New("the fork is unavailable"),
				}
			}
			return codextea.SafetyRecourseRewriteReadyMsg{
				SourceThreadID: threadID,
				Prompt:         prompt,
				Response:       response,
			}
		}
	}
}

func remoteThreadTurnsPage(ctx context.Context, client *remoteAppServerTUIClient, threadID string, limit int) (appserver.TurnsPage, error) {
	params := appserver.ThreadTurnsListParams{
		ThreadID:  strings.TrimSpace(threadID),
		Limit:     &limit,
		ItemsView: appserver.TurnItemsSummary,
	}
	var page appserver.TurnsPage
	if err := remoteSessionRequest(ctx, client, appserver.MethodThreadTurnsList, params, &page); err != nil {
		return appserver.TurnsPage{}, err
	}
	return page, nil
}

func remoteBlockedTurnIsLatest(page appserver.TurnsPage, turnID string) bool {
	if len(page.Data) == 0 {
		return false
	}
	latest := page.Data[0]
	return strings.TrimSpace(latest.ID) == strings.TrimSpace(turnID) && latest.Status == appserver.TurnStatusFailed
}

// remoteForkBoundaryExcludesTurn verifies the fork ends at the source's turn
// immediately before the blocked one.
func remoteForkBoundaryExcludesTurn(fork appserver.TurnsPage, source appserver.TurnsPage, turnID string) bool {
	if len(fork.Data) == 0 && fork.NextCursor != nil {
		return false
	}
	if len(source.Data) < 2 {
		return len(fork.Data) == 0
	}
	if len(fork.Data) == 0 {
		return false
	}
	return strings.TrimSpace(fork.Data[0].ID) == strings.TrimSpace(source.Data[1].ID) &&
		strings.TrimSpace(fork.Data[0].ID) != strings.TrimSpace(turnID)
}

func remoteSafetyRecourseForkParams(root *cli.RootOptions, state *codextui.State, threadID, turnID string) (appserver.ThreadForkParams, error) {
	shared := remoteSharedOptions(root, state)
	configValues, err := remoteConfigValues(root, shared)
	if err != nil {
		return appserver.ThreadForkParams{}, err
	}
	if state != nil {
		if effort := strings.TrimSpace(state.EffectiveReasoningEffort()); effort != "" {
			configValues["model_reasoning_effort"] = effort
		}
	}
	params := appserver.ThreadForkParams{
		ThreadID:     threadID,
		HistoryMode:  session.ForkAll,
		BeforeTurnID: turnID,
		Config:       configValues,
		// Rust ForkGoalContinuation::StartIfIdle copies the source's current goal
		// beyond the turn cutoff and starts it once the fork is idle.
		DeferGoalContinuation: true,
	}
	if cwd := strings.TrimSpace(shared.CWD); cwd != "" {
		params.CWD = &cwd
	}
	if model := strings.TrimSpace(shared.Model); model != "" {
		params.Model = &model
	}
	if state != nil {
		if provider := strings.TrimSpace(state.Provider); provider != "" {
			params.ModelProvider = &provider
		}
	}
	if len(configValues) == 0 {
		params.Config = nil
	}
	return params, nil
}
