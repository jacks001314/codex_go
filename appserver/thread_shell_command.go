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

	"codex_go/config"
	"codex_go/envutil"
	"codex_go/execpolicy"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	usershell "codex_go/shell"
	"codex_go/tool"
	"codex_go/turn"
)

type threadShellCommandRun struct {
	ThreadID   string
	TurnID     string
	Command    string
	CWD        string
	Standalone bool
	StartedAt  int64
	TimeoutMs  *int64
}

// threadShellCommandLaunch prepares a user shell command the way Rust's
// `tasks/user_shell.rs` does: the session's own shell runs it as a login
// command, the session's shell snapshot is replayed so the user's aliases,
// functions and options still apply, and Codex's own PATH entries stay on PATH
// after the snapshot restores the user's.
//
// The command itself runs outside the sandbox - `/shell` is the explicit
// full-access escape hatch - so the snapshot is captured without one too.
func (r *RuntimeRouter) threadShellCommandLaunch(ctx context.Context, run *threadShellCommandRun) ([]string, []string) {
	if r == nil || run == nil {
		return threadShellCommandArgv(""), nil
	}
	shellType, shellPath, _ := r.sessionShellForThread(run.ThreadID)
	if shellPath == "" {
		// The environment reported no shell; keep the previous behavior rather
		// than failing a command the user typed.
		return threadShellCommandArgv(run.Command), nil
	}
	sessionShell := &tool.Shell{Type: tool.DetectShellType(shellPath), Path: shellPath}
	if shellType != tool.ShellUnknown {
		sessionShell.Type = shellType
	}
	argv := sessionShell.DeriveExecArgs(run.Command, true)

	cfg := r.effectiveWriteStdinConfig(run.ThreadID)
	env, explicitOverrides := r.threadShellCommandEnv(run, cfg)
	prepends := &tool.RuntimePathPrepends{}
	if runtime.GOOS != "windows" {
		tool.ApplyPackagePathPrepends(env, prepends)
	}
	if provider := r.shellSnapshotProviderForTurn(run.ThreadID, cfg); provider != nil {
		snapshotPath := provider(ctx, tool.SnapshotProviderRequest{
			ShellType:       sessionShell.Type,
			ShellPath:       sessionShell.Path,
			CWD:             run.CWD,
			AllowLoginShell: true,
		})
		if snapshotPath != "" {
			argv = tool.MaybeWrapShellLCWithSnapshot(argv, sessionShell, snapshotPath, explicitOverrides, env, prepends.Entries())
		}
	}
	return argv, envSliceFromMap(env)
}

// sessionShellForThread resolves the shell the thread's environment runs, and
// whether that environment is remote (a remote shell cannot be captured or
// snapshotted on this host).
func (r *RuntimeRouter) sessionShellForThread(threadID string) (tool.ShellType, string, bool) {
	if r == nil || r.services.Environment == nil {
		return tool.ShellUnknown, "", false
	}
	// Rust uses the turn environment's shell; Go resolves the same selection the
	// turn's launches use, and falls back to the implicit local environment.
	for _, environment := range r.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{ThreadID: strings.TrimSpace(threadID)}) {
		if environment.Shell == nil || strings.TrimSpace(environment.Shell.Path) == "" {
			continue
		}
		remote := strings.TrimSpace(environment.ExecServerURL) != "" ||
			environment.NoiseProvider != nil ||
			environment.ExecServerStdioCommand != nil
		return environment.Shell.Type, strings.TrimSpace(environment.Shell.Path), remote
	}
	local := r.services.Environment.LocalShell()
	if shellPath := strings.TrimSpace(local.Path); shellPath != "" {
		shellType := tool.DetectShellType(shellPath)
		if detected := tool.DetectShellType(local.Name); detected != tool.ShellUnknown {
			shellType = detected
		}
		return shellType, shellPath, false
	}
	return tool.ShellUnknown, "", false
}

// threadShellCommandEnv builds the user shell command's environment from the
// thread's shell environment policy, with the session identity and apply-patch
// mode injected, and reports the policy's explicit overrides so the snapshot
// wrapper restores them after sourcing.
func (r *RuntimeRouter) threadShellCommandEnv(run *threadShellCommandRun, cfg *config.Config) (map[string]string, map[string]string) {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	explicitOverrides := map[string]string{}
	if cfg != nil {
		if table, ok := cfg.Values["shell_environment_policy"].(map[string]any); ok {
			if policy := execpolicy.EnvPolicyFromShellEnvironmentPolicy(table, run.CWD); policy != nil {
				env = execpolicy.CreateEnv(policy, &run.ThreadID, env)
				for key, value := range policy.Set {
					explicitOverrides[key] = value
				}
			}
		}
		env = envutil.InjectApplyPatchEnv(env, features.Enabled(cfg.FeatureSettings(), "apply_patch_preserve_line_endings"))
	}
	// Rust 97729885d4: the shared root-session identity, falling back to the
	// thread id when the thread record has none.
	sessionID := strings.TrimSpace(run.ThreadID)
	if r != nil {
		if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(run.ThreadID)), false, false); err == nil && record != nil && strings.TrimSpace(record.SessionID) != "" {
			sessionID = strings.TrimSpace(record.SessionID)
		}
	}
	if sessionID != "" {
		env["CODEX_SESSION_ID"] = sessionID
	}
	if threadID := strings.TrimSpace(run.ThreadID); threadID != "" {
		env["CODEX_THREAD_ID"] = threadID
	}
	return env, explicitOverrides
}

// envSliceFromMap renders an environment map for exec.Cmd.
func envSliceFromMap(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func (r *RuntimeRouter) handleThreadShellCommand(request *Request, params *ShellCommandParams) (*ShellCommandResponse, error) {
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
	// Rust `thread_shell_command_inner` loads the thread and then enforces the
	// direct-input policy before running the shell command.
	if err := r.ensureDirectInputAllowed(request, threadID); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(params.Command)
	cwd, err := r.threadShellCommandCWD(threadID)
	if err != nil {
		return nil, err
	}
	params.Command = command
	timeoutMs := cloneInt64(params.TimeoutMs)
	response, err := r.requireThreadExtras().ShellCommand(params)
	if err != nil {
		return nil, err
	}
	run, err := r.prepareThreadShellCommandRun(threadID, command, cwd, timeoutMs)
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

func (r *RuntimeRouter) prepareThreadShellCommandRun(threadID string, command string, cwd string, timeoutMs *int64) (*threadShellCommandRun, error) {
	if active := r.activeRuntimeTurnForShellCommand(threadID); active != nil && strings.TrimSpace(active.TurnID) != "" {
		return &threadShellCommandRun{
			ThreadID:   threadID,
			TurnID:     active.TurnID,
			Command:    command,
			CWD:        cwd,
			Standalone: false,
			StartedAt:  active.StartedAtMS,
			TimeoutMs:  cloneInt64(timeoutMs),
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
		TimeoutMs:  cloneInt64(timeoutMs),
	}, nil
}

func (r *RuntimeRouter) activeRuntimeTurnForShellCommand(threadID string) *activeRuntimeTurn {
	if r == nil {
		return nil
	}
	active := r.threads.ActiveTurn(strings.TrimSpace(threadID))
	if active == nil {
		return nil
	}
	return active
}

func (r *RuntimeRouter) runThreadShellCommand(ctx context.Context, run *threadShellCommandRun) {
	if r == nil || run == nil {
		return
	}
	itemID := "user-shell-" + safeIdentifier(run.TurnID) + "-" + safeIdentifier(fmt.Sprintf("%d", time.Now().UTC().UnixNano()))
	processID := "process-" + safeIdentifier(itemID)
	// Rust #41384: configurable thread shell-command timeout, defaulting to one
	// hour; zero requests an immediate timeout.
	timeout := time.Hour
	if run.TimeoutMs != nil {
		timeout = time.Duration(*run.TimeoutMs) * time.Millisecond
	}
	runCtx, cancel := context.WithDeadline(ctx, time.Now().Add(timeout))
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
		r.notifyTurnCompletedOnce(&TurnCompletedNotification{
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
	argv, commandEnv := r.threadShellCommandLaunch(ctx, run)
	cmd := osexec.CommandContext(ctx, argv[0], argv[1:]...)
	if strings.TrimSpace(run.CWD) != "" {
		cmd.Dir = run.CWD
	}
	cmd.Env = commandEnv
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
