package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	codexexec "codex_go/exec"
	"codex_go/prompt"
	"codex_go/protocol"
	"codex_go/turn"

	"github.com/google/uuid"
)

// Rust #61707: `codex exec --remote <ADDR>` runs one noninteractive turn on an
// existing app server, using that server's configuration and credentials.
// Remote options are accepted before or after `exec` and `exec resume`.

type remoteExecTurn struct {
	threadID string
	turnID   string
	// ephemeral and historyMode carry Rust's ExecTurn fields into the
	// turn-completion backfill decision (maybe_backfill_turn_completed_items).
	ephemeral   bool
	historyMode string
}

// remoteExecState tracks the items this turn started so the completion
// notification can reconcile any item whose item/completed went missing,
// matching Rust's reconcile_unfinished_started_items.
type remoteExecState struct {
	started           map[string]bool
	lastCriticalError string
}

// runRemoteExecRequest returns (false, nil) when no remote endpoint applies so
// the caller can keep the local exec path.
func runRemoteExecRequest(ctx context.Context, parsed *cli.Parsed, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if parsed == nil {
		return false, nil
	}
	remote := strings.TrimSpace(parsed.Exec.Remote)
	remoteAuthEnv := strings.TrimSpace(parsed.Exec.RemoteAuthEnv)
	if remote == "" && remoteAuthEnv == "" {
		return false, nil
	}
	return true, runRemoteExec(ctx, parsed, stdin, stdout, stderr)
}

func runRemoteExec(ctx context.Context, parsed *cli.Parsed, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := validateRemoteExecOptions(parsed); err != nil {
		return err
	}
	model, effort, err := resolveRemoteExecOverrides(parsed)
	if err != nil {
		return err
	}
	sessionID, promptText, err := resolveRemoteExecTurn(parsed, stdin, stderr)
	if err != nil {
		return err
	}
	endpoint, err := resolveInteractiveRemoteEndpoint(&cli.RootOptions{
		Remote:        parsed.Exec.Remote,
		RemoteAuthEnv: parsed.Exec.RemoteAuthEnv,
	})
	if err != nil {
		return err
	}
	renderer := codexexec.NewRemoteTurnRenderer(stdout, stderr, parsed.Exec.JSON, parsed.Exec.Color, parsed.Exec.LastMessageFile)
	client, err := openRemoteExecClient(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.close()

	var thread *appserver.Thread
	if sessionID != "" {
		var resumed appserver.ThreadResumeResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{
			ThreadID:     sessionID,
			ExcludeTurns: true,
		}, &resumed); err != nil {
			return err
		}
		thread = resumed.Thread
	} else {
		var started appserver.ThreadStartResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadStart, appserver.ThreadStartParams{
			ApprovalPolicy: "never",
			Model:          model,
		}, &started); err != nil {
			return err
		}
		thread = started.Thread
	}
	if thread == nil || strings.TrimSpace(thread.ID) == "" {
		return errors.New("app-server did not return a thread")
	}
	if strings.EqualFold(strings.TrimSpace(thread.Status.Type), "active") {
		return errors.New("remote thread already has an active turn")
	}
	if err := renderer.PrintThreadStarted(thread.ID); err != nil {
		return err
	}

	var response turn.TurnStartResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodTurnStart, turn.TurnStartParams{
		ThreadID:       thread.ID,
		TurnTrigger:    "exec",
		ApprovalPolicy: "never",
		Model:          model,
		Effort:         effort,
		Input: []turn.TurnUserInput{{
			Type: "text",
			Text: promptText,
		}},
	}, &response); err != nil {
		return err
	}
	current := remoteExecTurn{
		threadID:    thread.ID,
		turnID:      strings.TrimSpace(response.Turn.ID),
		ephemeral:   thread.Ephemeral,
		historyMode: strings.TrimSpace(string(thread.HistoryMode)),
	}
	state := &remoteExecState{started: map[string]bool{}}

	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	go func() {
		if _, ok := <-interrupts; !ok {
			return
		}
		if current.turnID == "" {
			return
		}
		_, _ = client.sendRequest(ctx, appserver.MethodTurnInterrupt, turn.TurnInterruptParams{
			ThreadID: current.threadID,
			TurnID:   current.turnID,
		})
	}()

	failed, err := streamRemoteExecTurn(ctx, client, renderer, current, state)
	if finishErr := renderer.Finish(); err == nil {
		err = finishErr
	}
	if err != nil {
		return err
	}
	if failed {
		return &ExitError{Code: 1, Silent: true}
	}
	return nil
}

// validateRemoteExecOptions mirrors Rust remote::run's ensure!: model, reasoning
// effort, and output options are client-settable; every other agent setting is
// rejected up front because the server owns it.
func validateRemoteExecOptions(parsed *cli.Parsed) error {
	exec := parsed.Exec
	shared := exec.Shared
	rootShared := parsed.Root.Shared
	unsupported := exec.Trust || exec.IgnoreUserConfig || exec.IgnoreRules || exec.StrictConfig || exec.Ephemeral ||
		strings.TrimSpace(exec.ThreadSource) != "" || strings.TrimSpace(exec.CyberAccessProgram) != "" ||
		strings.TrimSpace(exec.OutputSchema) != "" ||
		len(shared.Images) > 0 || len(rootShared.Images) > 0 ||
		shared.OSS || rootShared.OSS ||
		strings.TrimSpace(shared.OSSProvider) != "" || strings.TrimSpace(rootShared.OSSProvider) != "" ||
		strings.TrimSpace(shared.Profile) != "" || strings.TrimSpace(rootShared.Profile) != "" ||
		strings.TrimSpace(shared.Sandbox) != "" ||
		shared.ApproveForMe || rootShared.ApproveForMe ||
		shared.DangerouslyBypassApprovalsAndSandbox || rootShared.DangerouslyBypassApprovalsAndSandbox ||
		shared.DangerouslyBypassHookTrust || rootShared.DangerouslyBypassHookTrust ||
		strings.TrimSpace(shared.CWD) != "" || strings.TrimSpace(rootShared.CWD) != "" ||
		shared.Worktree || rootShared.Worktree ||
		len(shared.AddDirs) > 0 || len(rootShared.AddDirs) > 0
	if unsupported {
		return errors.New("--remote supports prompt, resume by UUID, model, reasoning effort, and output options; configure other agent settings on the server")
	}
	return nil
}

// resolveRemoteExecOverrides mirrors Rust #63462 remote::run: `--remote`
// accepts `--model`, `-c model=...`, and `-c model_reasoning_effort=...`, and
// `--model` takes precedence over `-c model=...`.
func resolveRemoteExecOverrides(parsed *cli.Parsed) (string, *string, error) {
	if parsed == nil {
		return "", nil, nil
	}
	raw := append(append([]string(nil), parsed.Root.ConfigOverrides...), parsed.Exec.ConfigOverrides...)
	overrides, err := config.ParseOverrides(raw)
	if err != nil {
		return "", nil, err
	}
	var model string
	var effort *string
	for _, override := range overrides {
		switch override.Path {
		case "model":
			value, ok := override.Value.(string)
			if !ok {
				return "", nil, errors.New("invalid model override: expected a string")
			}
			model = value
		case "model_reasoning_effort":
			value, ok := override.Value.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return "", nil, errors.New("invalid model_reasoning_effort override")
			}
			effortValue := value
			effort = &effortValue
		default:
			return "", nil, fmt.Errorf("--remote does not support config override `%s`; configure it on the server", override.Path)
		}
	}
	if cliModel := firstNonEmptyLocal(strings.TrimSpace(parsed.Exec.Shared.Model), strings.TrimSpace(parsed.Root.Shared.Model)); cliModel != "" {
		model = cliModel
	}
	if cliEffort := firstNonEmptyLocal(strings.TrimSpace(parsed.Exec.Shared.ModelReasoningEffort), strings.TrimSpace(parsed.Root.Shared.ModelReasoningEffort)); cliEffort != "" {
		effortValue := cliEffort
		effort = &effortValue
	}
	return model, effort, nil
}

// resolveRemoteExecTurn mirrors Rust's session/prompt selection for remote
// turns: a fresh prompt, or `exec resume <UUID> [PROMPT]`.
func resolveRemoteExecTurn(parsed *cli.Parsed, stdin io.Reader, stderr io.Writer) (string, string, error) {
	exec := parsed.Exec
	switch exec.Subcommand {
	case "":
		resolved, err := prompt.ResolveRootPrompt(exec.Prompt, stdin, stderr)
		return "", resolved, err
	case "resume":
		if exec.Resume.Last || exec.Resume.All || len(exec.Shared.Images) > 0 {
			return "", "", errors.New("remote resume requires a thread UUID")
		}
		id := strings.TrimSpace(exec.Resume.SessionID)
		if _, err := uuid.Parse(id); err != nil {
			return "", "", errors.New("remote resume requires a thread UUID")
		}
		promptArg := exec.Resume.Prompt
		if strings.TrimSpace(promptArg) == "" {
			promptArg = exec.Prompt
		}
		resolved, err := prompt.ResolveExecPrompt(promptArg, stdin, stderr)
		return id, resolved, err
	default:
		return "", "", errors.New("--remote supports exec and exec resume")
	}
}

// streamRemoteExecTurn consumes notifications until the started turn finishes,
// rendering the same event shapes the local exec processors do. It reports
// whether the turn failed or was interrupted.
func streamRemoteExecTurn(ctx context.Context, client *remoteAppServerTUIClient, renderer *codexexec.RemoteTurnRenderer, current remoteExecTurn, state *remoteExecState) (bool, error) {
	var lastUsage *appserver.TokenUsage
	errorSeen := false
	for {
		message, err := client.readRemoteMessage(ctx)
		if err != nil {
			return false, errors.New("app-server disconnected before turn completion")
		}
		method := strings.TrimSpace(message.Method)
		if method == "" {
			// A response to an interrupt (or another one-shot request) carries
			// nothing the turn loop needs.
			continue
		}
		if len(message.ID) > 0 {
			if err := client.respondServerRequest(ctx, message); err != nil {
				return false, err
			}
			continue
		}
		done, failed, err := handleRemoteExecNotification(ctx, client, message, renderer, current, state, &lastUsage, &errorSeen)
		if err != nil {
			return false, err
		}
		if done {
			return failed || errorSeen, nil
		}
	}
}

func handleRemoteExecNotification(ctx context.Context, client *remoteAppServerTUIClient, message remoteAppServerMessage, renderer *codexexec.RemoteTurnRenderer, current remoteExecTurn, state *remoteExecState, lastUsage **appserver.TokenUsage, errorSeen *bool) (bool, bool, error) {
	method := appserver.NotificationMethod(strings.TrimSpace(message.Method))
	switch method {
	case appserver.NotificationTurnStarted:
		var payload appserver.TurnStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) {
			return false, false, nil
		}
		return false, false, renderer.Emit(protocol.TurnStarted())
	case appserver.NotificationItemStarted:
		var payload appserver.ItemStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) || !remoteExecTurnMatches(payload.TurnID, current.turnID) {
			return false, false, nil
		}
		item, ok := remoteExecItemFromPayload(payload.Item, false)
		if !ok || item.ID == "" {
			return false, false, nil
		}
		state.started[item.ID] = true
		return false, false, renderer.Emit(protocol.ItemStarted(item))
	case appserver.NotificationItemCompleted:
		var payload appserver.ItemCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) || !remoteExecTurnMatches(payload.TurnID, current.turnID) {
			return false, false, nil
		}
		item, ok := remoteExecItemFromPayload(payload.Item, true)
		if !ok || item.ID == "" {
			return false, false, nil
		}
		delete(state.started, item.ID)
		if item.Type == "agent_message" {
			renderer.SetFinalMessage(item.Text)
		}
		return false, false, renderer.Emit(protocol.ItemCompleted(item))
	case appserver.NotificationError:
		var payload appserver.ErrorNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) || !remoteExecTurnMatches(payload.TurnID, current.turnID) {
			return false, false, nil
		}
		message, details := turnErrorParts(payload.Error)
		if !payload.WillRetry {
			*errorSeen = true
			state.lastCriticalError = turnErrorWithDetails(message, details)
		}
		return false, false, renderer.PrintError(message, details)
	case appserver.NotificationConfigWarning:
		var payload config.ConfigWarningNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		return false, false, renderer.PrintWarning(noticeMessage(payload.Summary, payload.Details))
	case appserver.NotificationWarning:
		var payload appserver.WarningNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if payload.ThreadID != nil && !remoteExecThreadMatches(*payload.ThreadID, current.threadID) {
			return false, false, nil
		}
		return false, false, renderer.PrintWarning(payload.Message)
	case appserver.NotificationDeprecationNotice:
		var payload appserver.DeprecationNoticeNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		return false, false, renderer.PrintDeprecation(payload.Summary, payload.Details)
	case appserver.NotificationModelRerouted:
		var payload appserver.ModelReroutedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		return false, false, renderer.PrintModelReroute(payload.FromModel, payload.ToModel, payload.Reason)
	case appserver.NotificationThreadTokenUsageUpdated:
		var payload appserver.ThreadTokenUsageUpdatedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) {
			return false, false, nil
		}
		usage := payload.TokenUsage
		*lastUsage = &usage
		return false, false, nil
	case appserver.NotificationTurnPlanUpdated:
		var payload appserver.TurnPlanUpdatedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) || !remoteExecTurnMatches(payload.TurnID, current.turnID) {
			return false, false, nil
		}
		items := make([]protocol.TodoItem, 0, len(payload.Plan))
		for _, step := range payload.Plan {
			items = append(items, protocol.TodoItem{
				Text:      strings.TrimSpace(step.Step),
				Completed: step.Status == appserver.TurnPlanStepCompleted,
			})
		}
		return false, false, renderer.EmitPlan(items)
	case appserver.NotificationAuthRecoveryStarted, appserver.NotificationAuthRecoveryCompleted:
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		renderer.PrintAuthRecovery(payload.Message, method == appserver.NotificationAuthRecoveryCompleted)
		return false, false, nil
	case appserver.NotificationHookStarted, appserver.NotificationHookCompleted:
		var payload appserver.HookStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		name, status := hookRunDisplay(payload.Run)
		renderer.PrintHook(name, status)
		return false, false, nil
	case appserver.NotificationTurnCompleted:
		var payload appserver.TurnCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return false, false, err
		}
		if !remoteExecThreadMatches(payload.ThreadID, current.threadID) || !remoteExecTurnMatches(payload.Turn.ID, current.turnID) {
			return false, false, nil
		}
		if payload.Turn.Status == appserver.TurnStatusInProgress {
			return false, false, nil
		}
		// Rust backfills the completed turn before the processors see it so a
		// dropped item notification cannot hide the final answer.
		backfillRemoteCompletedTurnItems(ctx, client, current, &payload.Turn)
		if err := renderer.FlushPlan(); err != nil {
			return false, false, err
		}
		if err := reconcileRemoteExecItems(renderer, state, payload.Turn.Items); err != nil {
			return false, false, err
		}
		switch payload.Turn.Status {
		case appserver.TurnStatusCompleted:
			if text := finalRemoteTurnMessage(payload.Turn.Items); text != "" {
				renderer.SetFinalMessage(text)
			}
			usage := remoteExecUsage(*lastUsage)
			if *lastUsage != nil {
				renderer.SetTokenUsage(usage)
			}
			return true, false, renderer.Emit(protocol.TurnCompletedWithID(usage, payload.Turn.ID))
		case appserver.TurnStatusInterrupted:
			renderer.ClearFinalMessage()
			renderer.PrintInterrupted()
			return true, true, nil
		case appserver.TurnStatusFailed:
			renderer.ClearFinalMessage()
			message := strings.TrimSpace(state.lastCriticalError)
			humanMessage := ""
			if payload.Turn.Error != nil {
				rawMessage, details := turnErrorParts(*payload.Turn.Error)
				message = turnErrorWithDetails(rawMessage, details)
				humanMessage = rawMessage
			}
			if message == "" {
				message = "turn failed"
			}
			if err := renderer.PrintTurnFailed(message, humanMessage); err != nil {
				return false, false, err
			}
			return true, true, nil
		default:
			return false, false, nil
		}
	}
	return false, false, nil
}

func remoteExecThreadMatches(candidate, threadID string) bool {
	candidate = strings.TrimSpace(candidate)
	return candidate == "" || candidate == strings.TrimSpace(threadID)
}

// backfillRemoteCompletedTurnItems mirrors Rust's
// maybe_backfill_turn_completed_items: a non-ephemeral thread whose completion
// carries a partial item view is re-read from app-server history so dropped item
// notifications cannot hide the final answer. Failures are ignored, as Rust only
// logs them.
func backfillRemoteCompletedTurnItems(ctx context.Context, client *remoteAppServerTUIClient, turn remoteExecTurn, completed *appserver.Turn) {
	if client == nil || completed == nil || turn.ephemeral {
		return
	}
	// Rust defaults a missing items_view to Full, so an old server that omits
	// the field never triggers a history read.
	if completed.ItemsView == "" || completed.ItemsView == appserver.TurnItemsFull {
		return
	}
	if turn.historyMode == string(appserver.ThreadHistoryPaginated) {
		items, ok := listRemoteTurnItems(ctx, client, turn.threadID, completed.ID)
		if !ok {
			return
		}
		completed.Items = mergeRemoteTurnItems(items, completed.Items)
		return
	}
	thread, err := remoteThreadRead(ctx, client, turn.threadID, true)
	if err != nil {
		return
	}
	for _, candidate := range thread.Turns {
		if strings.TrimSpace(candidate.ID) != strings.TrimSpace(completed.ID) {
			continue
		}
		completed.Items = candidate.Items
		return
	}
}

// listRemoteTurnItems pages thread/items/list for one turn.
func listRemoteTurnItems(ctx context.Context, client *remoteAppServerTUIClient, threadID, turnID string) ([]appserver.ThreadItem, bool) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil, false
	}
	var (
		items  []appserver.ThreadItem
		cursor *appserver.ThreadItemsListCursor
		seen   = map[string]bool{}
	)
	for {
		var page appserver.ThreadItemsListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadItemsList, appserver.ThreadItemsListParams{
			ThreadID: strings.TrimSpace(threadID),
			TurnID:   &turnID,
			Cursor:   cursor,
		}, &page); err != nil {
			return nil, false
		}
		for _, entry := range page.Data {
			items = append(items, entry.Item)
		}
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" {
			return items, true
		}
		next := strings.TrimSpace(*page.NextCursor)
		if seen[next] {
			return nil, false
		}
		seen[next] = true
		cursor = &appserver.ThreadItemsListCursor{Opaque: next}
	}
}

// mergeRemoteTurnItems overlays the completion items on the backfilled ones by
// item id, appending any item the history read did not return.
func mergeRemoteTurnItems(backfilled, completion []appserver.ThreadItem) []appserver.ThreadItem {
	items := append([]appserver.ThreadItem(nil), backfilled...)
	for _, item := range completion {
		replaced := false
		for i := range items {
			if items[i].ID == item.ID {
				items[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			items = append(items, item)
		}
	}
	return items
}

func remoteExecTurnMatches(candidate, turnID string) bool {
	candidate = strings.TrimSpace(candidate)
	return candidate == "" || turnID == "" || candidate == strings.TrimSpace(turnID)
}

// turnErrorParts splits a turn error the way the exec processors do: the JSONL
// output appends the additional details, while the human renderer prints only
// TurnError's Display message.
func turnErrorParts(err appserver.TurnError) (string, string) {
	message := strings.TrimSpace(err.Message)
	details := ""
	if err.AdditionalDetails != nil {
		details = strings.TrimSpace(*err.AdditionalDetails)
	}
	return message, details
}

func turnErrorWithDetails(message, details string) string {
	if strings.TrimSpace(details) == "" {
		return strings.TrimSpace(message)
	}
	return fmt.Sprintf("%s (%s)", strings.TrimSpace(message), strings.TrimSpace(details))
}

func noticeMessage(summary string, details *string) string {
	summary = strings.TrimSpace(summary)
	if details != nil {
		if detailText := strings.TrimSpace(*details); detailText != "" {
			return fmt.Sprintf("%s (%s)", summary, detailText)
		}
	}
	return summary
}

func finalRemoteTurnMessage(items []appserver.ThreadItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(items[i].Type), "agentMessage") {
			continue
		}
		if text := strings.TrimSpace(items[i].Text); text != "" {
			return text
		}
	}
	for i := len(items) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(items[i].Type), "plan") {
			continue
		}
		if text := strings.TrimSpace(items[i].Text); text != "" {
			return text
		}
	}
	return ""
}

// remoteExecUsage mirrors Rust's usage_from_last_total: only the thread's total
// breakdown is reported on turn.completed.
func remoteExecUsage(last *appserver.TokenUsage) protocol.Usage {
	if last == nil {
		return protocol.Usage{}
	}
	return remoteUsageBreakdown(last.Total)
}

// reconcileRemoteExecItems completes items that started but never received an
// item/completed notification before the turn ended.
func reconcileRemoteExecItems(renderer *codexexec.RemoteTurnRenderer, state *remoteExecState, items []appserver.ThreadItem) error {
	if state == nil {
		return nil
	}
	for _, threadItem := range items {
		rawID := strings.TrimSpace(threadItem.ID)
		if rawID == "" || !state.started[rawID] {
			continue
		}
		item, ok := remoteExecItemFromPayload(appserver.BuildThreadItemPayload(threadItem), true)
		if !ok {
			continue
		}
		delete(state.started, rawID)
		if err := renderer.Emit(protocol.ItemCompleted(item)); err != nil {
			return err
		}
	}
	return nil
}

// remoteExecItemFromPayload maps app-server thread items onto the exec JSONL
// item set (Rust exec/src/event_processor_with_jsonl_output.rs map_item_with_id).
// Items outside that set are ignored so the remote stream matches local exec.
func remoteExecItemFromPayload(payload appserver.ThreadItemPayload, completed bool) (protocol.ThreadItem, bool) {
	id := remotePayloadString(payload, "id")
	switch remotePayloadString(payload, "type") {
	case "agentMessage":
		if !completed {
			return protocol.ThreadItem{}, false
		}
		return protocol.AgentMessageItemWithPhase(id, remotePayloadString(payload, "text"), remotePayloadString(payload, "phase")), true
	case "reasoning":
		if !completed {
			return protocol.ThreadItem{}, false
		}
		summary := remotePayloadStringSlice(payload, "summary")
		content := remotePayloadStringSlice(payload, "content", "reasoningContent")
		text := strings.Join(summary, "\n")
		if strings.TrimSpace(text) == "" {
			return protocol.ThreadItem{}, false
		}
		return protocol.ThreadItem{ID: id, Type: "reasoning", Summary: summary, Content: content, Text: text}, true
	case "commandExecution":
		var exitCode *int
		if value, ok := remotePayloadInt(payload, "exitCode", "exit_code"); ok {
			exitCode = &value
		}
		item := protocol.CommandExecutionItem(
			id,
			remotePayloadString(payload, "command"),
			remoteFirstPayloadRawString(payload, "aggregatedOutput", "output"),
			exitCode,
			remoteExecCommandStatus(remotePayloadString(payload, "status"), completed),
		)
		return item, true
	case "fileChange":
		return protocol.FileChangeItem(id, remoteExecFileChanges(payload), remoteExecPatchStatus(remotePayloadString(payload, "status"), completed)), true
	case "mcpToolCall":
		item := protocol.MCPToolCallItem(
			id,
			remotePayloadString(payload, "server"),
			remotePayloadString(payload, "tool"),
			payload["arguments"],
			remoteMCPToolResult(payload["result"]),
			remoteMCPToolError(payload["error"]),
			remoteExecMCPStatus(remotePayloadString(payload, "status"), completed),
		)
		return item, true
	case "collabAgentToolCall":
		tool, ok := remoteExecCollabTool(remotePayloadString(payload, "tool"))
		if !ok {
			return protocol.ThreadItem{}, false
		}
		status, ok := remoteExecCollabStatus(remotePayloadString(payload, "status"), completed)
		if !ok {
			return protocol.ThreadItem{}, false
		}
		item := protocol.CollabToolCallItem(
			id,
			tool,
			remoteFirstPayloadString(payload, "senderThreadId", "sender_thread_id"),
			remotePayloadStringSlice(payload, "receiverThreadIds", "receiver_thread_ids"),
			remotePayloadOptionalString(payload, "prompt"),
			remotePayloadCollabAgentStates(payload, "agentsStates", "agents_states"),
			status,
		)
		return item, true
	case "webSearch", "web_search":
		action, _ := payload["action"].(map[string]any)
		var results *[]any
		if raw, ok := payload["results"]; ok && raw != nil {
			if list, ok := raw.([]any); ok {
				results = &list
			}
		}
		return protocol.WebSearchItemWithResults(id, remotePayloadString(payload, "query"), action, results), true
	default:
		return protocol.ThreadItem{}, false
	}
}

func remoteExecCommandStatus(raw string, completed bool) string {
	if !completed {
		return "in_progress"
	}
	switch raw {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "declined":
		return "declined"
	default:
		return "in_progress"
	}
}

func remoteExecPatchStatus(raw string, completed bool) string {
	if !completed {
		return "in_progress"
	}
	switch raw {
	case "completed":
		return "completed"
	case "failed", "declined":
		return "failed"
	default:
		return "in_progress"
	}
}

func remoteExecMCPStatus(raw string, completed bool) string {
	if !completed || raw == "inProgress" {
		return "in_progress"
	}
	switch raw {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	default:
		return "in_progress"
	}
}

func remoteExecCollabTool(raw string) (string, bool) {
	switch raw {
	case "spawnAgent":
		return "spawn_agent", true
	case "sendInput":
		return "send_input", true
	case "resumeAgent", "wait":
		return "wait", true
	case "closeAgent":
		return "close_agent", true
	default:
		return "", false
	}
}

func remoteExecCollabStatus(raw string, completed bool) (string, bool) {
	if !completed {
		return "in_progress", true
	}
	switch raw {
	case "completed":
		return "completed", true
	case "failed":
		return "failed", true
	default:
		return "", false
	}
}

func remoteExecFileChanges(payload map[string]any) []protocol.FileChange {
	raw, ok := payload["changes"].([]any)
	if !ok {
		return []protocol.FileChange{}
	}
	changes := make([]protocol.FileChange, 0, len(raw))
	for _, entry := range raw {
		change, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		kind := ""
		if kindPayload, ok := change["kind"].(map[string]any); ok {
			kind = remotePayloadString(kindPayload, "type")
		}
		changes = append(changes, protocol.FileChange{
			Path:     remotePayloadString(change, "path"),
			Kind:     kind,
			Diff:     remotePayloadString(change, "diff"),
			MovePath: remotePayloadString(change, "move_path"),
		})
	}
	return changes
}

func hookRunDisplay(run any) (string, string) {
	payload, ok := run.(map[string]any)
	if !ok {
		return "", ""
	}
	name := ""
	for _, key := range []string{"eventName", "event_name"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			name = strings.TrimSpace(value)
			break
		}
	}
	status := ""
	if value, ok := payload["status"].(string); ok {
		status = strings.TrimSpace(value)
	}
	return name, status
}
