package appserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"codex_go/internal/model"
	"codex_go/internal/session"
	usershell "codex_go/internal/shell"
	"codex_go/internal/turn"
)

type threadShellCommandRun struct {
	ThreadID   string
	TurnID     string
	Command    string
	CWD        string
	Standalone bool
	StartedAt  int64
}

func (r *RuntimeRouter) handleThreadShellCommand(params *ShellCommandParams) (*ShellCommandResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: runtime router is nil", ErrInvalidThreadExtraRequest)
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if err := r.requireLoadedThreadForRuntimeOp(threadID); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(params.Command)
	cwd, err := r.threadShellCommandCWD(threadID)
	if err != nil {
		return nil, err
	}
	params.Command = command
	response, err := r.requireThreadExtras().ShellCommand(params)
	if err != nil {
		return nil, err
	}
	run, err := r.prepareThreadShellCommandRun(threadID, command, cwd)
	if err != nil {
		return nil, err
	}
	go r.runThreadShellCommand(context.Background(), run)
	return response, nil
}

func (r *RuntimeRouter) threadShellCommandCWD(threadID string) (string, error) {
	cwd := ""
	if r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		record, err := r.threadRecord(session.ThreadID(threadID), false, false)
		if err != nil {
			return "", err
		}
		cwd = strings.TrimSpace(record.Metadata.CWD)
	}
	if cwd == "" && r != nil {
		cwd = strings.TrimSpace(r.services.DefaultCWD)
	}
	if cwd == "" {
		if current, err := os.Getwd(); err == nil {
			cwd = current
		}
	}
	return cwd, nil
}

func (r *RuntimeRouter) prepareThreadShellCommandRun(threadID string, command string, cwd string) (*threadShellCommandRun, error) {
	if active := r.activeRuntimeTurnForShellCommand(threadID); active != nil && strings.TrimSpace(active.TurnID) != "" {
		return &threadShellCommandRun{
			ThreadID:   threadID,
			TurnID:     active.TurnID,
			Command:    command,
			CWD:        cwd,
			Standalone: false,
			StartedAt:  active.StartedAtMS,
		}, nil
	}
	start := &turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "!" + command,
		Input: []turn.TurnUserInput{{
			Type: "text",
			Text: "!" + command,
		}},
	}
	response, err := r.requireTurns().Start(start)
	if err != nil {
		return nil, err
	}
	appTurn := appTurnFromTurnRecord(&response.Turn, nil, TurnStatusInProgress, nil, nil)
	r.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: appTurn})
	return &threadShellCommandRun{
		ThreadID:   threadID,
		TurnID:     response.Turn.ID,
		Command:    command,
		CWD:        cwd,
		Standalone: true,
		StartedAt:  response.Turn.StartedAt,
	}, nil
}

func (r *RuntimeRouter) activeRuntimeTurnForShellCommand(threadID string) *activeRuntimeTurn {
	if r == nil {
		return nil
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[strings.TrimSpace(threadID)]
	if active == nil {
		return nil
	}
	copy := *active
	return &copy
}

func (r *RuntimeRouter) runThreadShellCommand(ctx context.Context, run *threadShellCommandRun) {
	if r == nil || run == nil {
		return
	}
	itemID := "user-shell-" + safeIdentifier(run.TurnID) + "-" + safeIdentifier(fmt.Sprintf("%d", time.Now().UTC().UnixNano()))
	processID := "process-" + safeIdentifier(itemID)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.registerThreadShellCommandTerminal(run, itemID, processID, cancel)
	defer r.requireThreadExtras().RemoveBackgroundTerminal(run.ThreadID, processID)
	startedAtMS := time.Now().UTC().UnixMilli()
	started := threadShellCommandItem(run, itemID, processID, CommandExecutionInProgress, "", nil, nil, startedAtMS)
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		Item:        threadItemPayload(started),
		ThreadID:    run.ThreadID,
		TurnID:      run.TurnID,
		StartedAtMS: startedAtMS,
	})

	output := &threadShellCommandOutput{
		router:   r,
		threadID: run.ThreadID,
		turnID:   run.TurnID,
		itemID:   itemID,
	}
	startedAt := time.Now()
	exitCode, status := r.runThreadShellCommandProcess(runCtx, run, processID, output)
	durationMS := time.Since(startedAt).Milliseconds()
	outputText := output.String()
	record := threadShellCommandRecord(run, exitCode, time.Duration(durationMS)*time.Millisecond, outputText)
	if !run.Standalone {
		r.enqueueThreadShellCommandRecord(run, record)
	}
	r.persistThreadShellCommandRecord(run, record)
	completedAtMS := time.Now().UTC().UnixMilli()
	completed := threadShellCommandItem(run, itemID, processID, status, outputText, &exitCode, &durationMS, startedAtMS)
	r.notify(NotificationItemCompleted, &ItemCompletedNotification{
		Item:          threadItemPayload(completed),
		ThreadID:      run.ThreadID,
		TurnID:        run.TurnID,
		CompletedAtMS: completedAtMS,
	})
	if run.Standalone {
		_ = r.requireTurns().Complete(&turn.TurnCompleteParams{ThreadID: run.ThreadID, TurnID: run.TurnID, Status: string(TurnStatusCompleted)})
		duration := completedAtMS - startedAtMS
		r.notify(NotificationTurnCompleted, &TurnCompletedNotification{
			ThreadID: run.ThreadID,
			Turn:     completedTurnNotificationTurn(run.TurnID, TurnStatusCompleted, nil, &run.StartedAt, &completedAtMS, &duration),
		})
	}
}

func (r *RuntimeRouter) registerThreadShellCommandTerminal(run *threadShellCommandRun, itemID string, processID string, cancel context.CancelFunc) {
	if r == nil || run == nil {
		return
	}
	r.requireThreadExtras().AddBackgroundTerminalWithCancel(run.ThreadID, &BackgroundTerminal{
		ItemID:    itemID,
		ProcessID: processID,
		Command:   run.Command,
		CWD:       run.CWD,
	}, cancel)
}

func threadShellCommandRecord(run *threadShellCommandRun, exitCode int64, duration time.Duration, outputText string) *usershell.CommandRecord {
	if run == nil {
		return nil
	}
	return usershell.NewCommandRecord(run.Command, usershell.ExecOutput{
		ExitCode: int(exitCode),
		Duration: duration,
		Stdout:   outputText,
	}, 20_000)
}

func (r *RuntimeRouter) enqueueThreadShellCommandRecord(run *threadShellCommandRun, record *usershell.CommandRecord) {
	if r == nil || run == nil || record == nil || strings.TrimSpace(run.ThreadID) == "" || strings.TrimSpace(run.TurnID) == "" {
		return
	}
	item := model.UserMessageInputItem(record.Render())
	if item == nil {
		return
	}
	_ = r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
		ThreadID:   run.ThreadID,
		TurnID:     run.TurnID,
		InputItems: []any{item},
	})
}

func (r *RuntimeRouter) persistThreadShellCommandRecord(run *threadShellCommandRun, record *usershell.CommandRecord) {
	if r == nil || run == nil || record == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	now := time.Now().UTC()
	item := session.Item{
		ID:        "user-shell-record-" + safeIdentifier(run.TurnID) + "-" + safeIdentifier(fmt.Sprintf("%d", now.UnixNano())),
		Type:      "message",
		Role:      "user",
		Text:      record.Render(),
		CreatedAt: now,
		Metadata: map[string]any{
			"kind":    "user_shell_command",
			"turnId":  run.TurnID,
			"command": run.Command,
		},
	}
	if _, err := r.services.ThreadRouter.store.AppendItem(session.ThreadID(run.ThreadID), item); err != nil {
		return
	}
	_ = r.appendRuntimeRollout(run.ThreadID, []session.Item{item}, now)
}

func (r *RuntimeRouter) runThreadShellCommandProcess(ctx context.Context, run *threadShellCommandRun, processID string, output *threadShellCommandOutput) (int64, CommandExecutionStatus) {
	argv := threadShellCommandArgv(run.Command)
	cmd := osexec.CommandContext(ctx, argv[0], argv[1:]...)
	if strings.TrimSpace(run.CWD) != "" {
		cmd.Dir = run.CWD
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		output.WriteString("failed to capture stdout: " + err.Error())
		return 1, CommandExecutionFailed
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		output.WriteString("failed to capture stderr: " + err.Error())
		return 1, CommandExecutionFailed
	}
	if err := cmd.Start(); err != nil {
		output.WriteString("failed to start shell command: " + err.Error())
		return 1, CommandExecutionFailed
	}
	r.updateThreadShellCommandTerminalOSPID(run, processID, cmd)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(output, stdout)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(output, stderr)
	}()
	err = cmd.Wait()
	wg.Wait()
	exitCode, waitErr := commandExecExitCode(ctx, err)
	if waitErr != nil {
		if strings.TrimSpace(output.String()) != "" {
			output.WriteString("\n")
		}
		output.WriteString(waitErr.Error())
		return 1, CommandExecutionFailed
	}
	if exitCode != 0 {
		return int64(exitCode), CommandExecutionFailed
	}
	return int64(exitCode), CommandExecutionCompleted
}

func (r *RuntimeRouter) updateThreadShellCommandTerminalOSPID(run *threadShellCommandRun, processID string, cmd *osexec.Cmd) {
	if r == nil || run == nil || cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	osPID := uint32(cmd.Process.Pid)
	_, _ = r.requireThreadExtras().UpdateBackgroundTerminal(&BackgroundTerminalUpdateParams{
		ThreadID:  run.ThreadID,
		ProcessID: processID,
		OSPID:     &osPID,
	})
}

func threadShellCommandArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", command}
	}
	return []string{"/bin/sh", "-lc", command}
}

func threadShellCommandItem(run *threadShellCommandRun, itemID string, processID string, status CommandExecutionStatus, aggregatedOutput string, exitCode *int64, durationMS *int64, createdAtMS int64) ThreadItem {
	data := map[string]any{
		"command":   run.Command,
		"cwd":       run.CWD,
		"processId": processID,
		"source":    string(CommandExecutionSourceUserShell),
		"status":    string(status),
	}
	if aggregatedOutput != "" {
		data["aggregatedOutput"] = aggregatedOutput
	}
	if exitCode != nil {
		data["exitCode"] = *exitCode
	}
	if durationMS != nil {
		data["durationMs"] = *durationMS
	}
	return ThreadItem{
		ID:        itemID,
		Type:      "commandExecution",
		TurnID:    run.TurnID,
		CreatedAt: createdAtMS,
		Data:      data,
	}
}

type threadShellCommandOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	router   *RuntimeRouter
	threadID string
	turnID   string
	itemID   string
}

func (w *threadShellCommandOutput) Write(data []byte) (int, error) {
	if w == nil || len(data) == 0 {
		return len(data), nil
	}
	text := string(data)
	w.mu.Lock()
	_, _ = w.buffer.WriteString(text)
	w.mu.Unlock()
	if w.router != nil {
		w.router.notify(NotificationCommandExecutionOutputDelta, &CommandExecutionOutputDeltaNotification{
			ThreadID: w.threadID,
			TurnID:   w.turnID,
			ItemID:   w.itemID,
			Delta:    text,
		})
	}
	return len(data), nil
}

func (w *threadShellCommandOutput) WriteString(text string) {
	if text == "" {
		return
	}
	_, _ = w.Write([]byte(text))
}

func (w *threadShellCommandOutput) String() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}
