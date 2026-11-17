package appserver

import (
	"errors"
	"strings"
	"time"

	"codex_go/shell"
)

// This file carries Rust #61841's command-approval lifecycle. A background
// command can request approval after its originating turn has completed; the
// approval opens the command item, and a later turn transition cancels the
// waiting request. Rust completes such an item with `declined` so it does not
// stay `inProgress` forever, preserving the original turn id and start time.

// openCommandApprovalItem publishes the command item's `item/started` when the
// approval itself is the first thing to open it, and reports whether this call
// opened it. It mirrors Rust's start_command_execution_item call from
// apply_bespoke_event_handling: only a plain command approval (no approval id,
// no network approval context) starts a command item, and the first observed
// start wins over the core item that arrives later.
func (r *RuntimeRouter) openCommandApprovalItem(threadID string, turnID string, params *CommandExecutionRequestApprovalParams) bool {
	if r == nil || params == nil {
		return false
	}
	threadID = strings.TrimSpace(firstNonEmpty(threadID, params.ThreadID))
	turnID = strings.TrimSpace(firstNonEmpty(turnID, params.TurnID))
	itemID := strings.TrimSpace(params.ItemID)
	if threadID == "" || turnID == "" || itemID == "" {
		return false
	}
	if params.Kind.orDefault() != ExecApprovalKindCommand {
		return false
	}
	if params.ApprovalID != nil || params.NetworkApprovalContext != nil {
		return false
	}
	if r.commandItemStartedAtMS(threadID, turnID, itemID) != 0 {
		return false
	}
	startedAtMS := int64(params.StartedAtMS)
	if startedAtMS <= 0 {
		startedAtMS = time.Now().UTC().UnixMilli()
	}
	item := commandApprovalThreadItem(params, itemID, CommandExecutionInProgress)
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		Item:        threadItemPayload(item),
		ThreadID:    threadID,
		TurnID:      turnID,
		StartedAtMS: startedAtMS,
	})
	return r.commandItemStartedAtMS(threadID, turnID, itemID) != 0
}

// completeCommandApprovalItemDeclined closes a command item the approval opened
// with `declined`, preserving the original turn id and start time. Rust skips
// the completion when no start was recorded, so an item core never announced is
// never invented here either.
func (r *RuntimeRouter) completeCommandApprovalItemDeclined(threadID string, turnID string, params *CommandExecutionRequestApprovalParams) bool {
	if r == nil || params == nil {
		return false
	}
	threadID = strings.TrimSpace(firstNonEmpty(threadID, params.ThreadID))
	turnID = strings.TrimSpace(firstNonEmpty(turnID, params.TurnID))
	itemID := strings.TrimSpace(params.ItemID)
	if threadID == "" || turnID == "" || itemID == "" {
		return false
	}
	// Rust only builds a completion item for a plain command approval: managed
	// human checkpoints have none, network approvals complete through their own
	// flow, and a subcommand approval (approval id) never owns the parent item.
	if params.Kind.orDefault() == ExecApprovalKindHumanCommand || params.ApprovalID != nil || params.NetworkApprovalContext != nil {
		return false
	}
	if r.commandItemCompleted(threadID, turnID, itemID) {
		return false
	}
	startedAtMS := r.takeItemStartedTime(threadID, turnID, itemID)
	if startedAtMS == 0 {
		return false
	}
	item := commandApprovalThreadItem(params, itemID, CommandExecutionDeclined)
	payload := threadItemPayload(item)
	// The completion hook reads the start time back off the payload once it has
	// been consumed here, so keep Rust's original timestamp on the item itself.
	payload["startedAtMs"] = startedAtMS
	r.notify(NotificationItemCompleted, &ItemCompletedNotification{
		Item:          payload,
		ThreadID:      threadID,
		TurnID:        turnID,
		CompletedAtMS: time.Now().UTC().UnixMilli(),
	})
	r.markCommandItemCompleted(threadID, turnID, itemID)
	return true
}

// commandApprovalThreadItem is the command payload an approval reports when it
// owns the item lifecycle: the same fields Rust's CommandExecutionCompletionItem
// carries (command, cwd, parsed actions, agent source).
func commandApprovalThreadItem(params *CommandExecutionRequestApprovalParams, itemID string, status CommandExecutionStatus) ThreadItem {
	command := ""
	if params != nil && params.Command != nil {
		command = strings.TrimSpace(*params.Command)
	}
	cwd := ""
	if params != nil && params.CWD != nil {
		cwd = strings.TrimSpace(*params.CWD)
	}
	actions := []map[string]any{}
	if params != nil && len(params.CommandActions) > 0 {
		actions = append(actions, params.CommandActions...)
	} else if command != "" {
		actions = commandExecutionActions(shell.SplitCommandLine(command), command, cwd)
	}
	data := map[string]any{
		"command":        command,
		"cwd":            cwd,
		"source":         string(CommandExecutionSourceAgent),
		"status":         string(status),
		"commandActions": actions,
	}
	return ThreadItem{
		ID:        itemID,
		Type:      "commandExecution",
		Data:      data,
		CreatedAt: time.Now().UTC().UnixMilli(),
	}
}

// abortThreadPendingServerRequests cancels every request the thread still has
// pending, exactly like Rust's ThreadScopedOutgoingMessageSender::abort_pending_server_requests
// does on TurnStarted and on the latest turn's terminal event.
func (r *RuntimeRouter) abortThreadPendingServerRequests(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" || r.services.ServerRequests == nil {
		return
	}
	r.services.ServerRequests.CancelThreadPending(threadID, ErrServerRequestTurnTransition)
}

// approvalTurnTransitioned reports whether an approval wait ended because the
// thread moved on, which callers translate into the declined command status
// instead of a tool failure.
func approvalTurnTransitioned(err error) bool {
	return err != nil && errors.Is(err, ErrServerRequestTurnTransition)
}
