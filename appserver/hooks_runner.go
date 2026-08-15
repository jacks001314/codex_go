package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/envutil"
)

type HookRunner struct {
	ShellProgram string
	ShellArgs    []string
	Notify       func(NotificationMethod, any)
	Now          func() time.Time
	// McpToolHookExecutor executes mcp_tool hooks (Rust #38705). When nil,
	// mcp_tool handlers are skipped like Rust's engine without an executor.
	McpToolHookExecutor McpToolHookExecutor

	mu            sync.Mutex
	asyncRuntimes map[string]*asyncHookRuntime
}

// HookMcpCall mirrors Rust HookMcpCall: one MCP tool call requested by a
// configured mcp_tool hook handler.
type HookMcpCall struct {
	Server  string
	Tool    string
	Input   map[string]any
	Timeout time.Duration
}

// McpToolHookExecutor mirrors Rust HookMcpExecutor: returns text interpreted
// using ordinary command-hook output semantics.
type McpToolHookExecutor interface {
	Execute(ctx context.Context, call HookMcpCall) (string, error)
}

type HookRunRequest struct {
	ThreadID      string
	TurnID        *string
	CWD           string
	EventName     HookEventName
	MatcherInputs []string
	RunIDSuffix   string
	InputJSON     string
	Hooks         []HookMetadata
}

type HookRunResult struct {
	Runs               []HookRunSummary
	Blocked            bool
	BlockReason        string
	Stopped            bool
	StopReason         string
	UpdatedInput       any
	AdditionalContexts []string
	FeedbackMessage    string
}

type hookCommandRunResult struct {
	StartedAt   int64
	CompletedAt int64
	DurationMS  int64
	ExitCode    *int32
	Stdout      string
	Stderr      string
	Error       *string
}

func NewHookRunner() *HookRunner {
	return &HookRunner{Now: time.Now, asyncRuntimes: map[string]*asyncHookRuntime{}}
}

func (r *HookRunner) Run(ctx context.Context, request *HookRunRequest) (*HookRunResult, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: hook runner is nil", ErrInvalidHook)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request == nil {
		return nil, fmt.Errorf("%w: hook run request is nil", ErrInvalidHook)
	}
	if strings.TrimSpace(request.ThreadID) == "" {
		return nil, fmt.Errorf("%w: threadId is required", ErrInvalidHook)
	}
	if strings.TrimSpace(request.CWD) == "" {
		return nil, fmt.Errorf("%w: cwd is required", ErrInvalidHook)
	}
	selected := selectHookHandlers(request.Hooks, request.EventName, request.MatcherInputs)
	runs := make([]HookRunSummary, 0, len(selected))
	result := &HookRunResult{}
	for _, metadata := range selected {
		if metadata.ExecutionMode == HookExecutionAsync && request.EventName != HookEventSessionEnd {
			r.scheduleAsyncHook(ctx, request, metadata)
			continue
		}
		started := runningHookSummary(metadata, r.now())
		started = hookSummaryWithRunIDSuffix(started, request.RunIDSuffix)
		r.notify(NotificationHookStarted, &HookRunStartedNotification{
			ThreadID: request.ThreadID,
			TurnID:   cloneString(request.TurnID),
			Run:      started,
		})

		var runResult *hookCommandRunResult
		if metadata.HandlerType == HookHandlerMCPTool {
			runResult = r.runMcpToolHook(ctx, metadata, request)
		} else {
			runResult = r.runCommand(ctx, metadata, request.InputJSON, request.CWD)
		}
		completed := completedHookSummary(metadata, runResult, hookRunStatus(request.EventName, runResult), hookOutputEntries(request.EventName, runResult))
		completed = hookSummaryWithRunIDSuffix(completed, request.RunIDSuffix)
		mergeHookRunEffect(result, hookRunEffect(request.EventName, runResult))
		r.notify(NotificationHookCompleted, &HookRunCompletedNotification{
			ThreadID: request.ThreadID,
			TurnID:   cloneString(request.TurnID),
			Run:      completed,
		})
		runs = append(runs, completed)
	}
	result.Runs = runs
	return result, nil
}

func (r *HookRunner) Preview(request *HookRunRequest) []HookRunSummary {
	if request == nil {
		return nil
	}
	selected := selectHookHandlers(request.Hooks, request.EventName, request.MatcherInputs)
	out := make([]HookRunSummary, 0, len(selected))
	for _, metadata := range selected {
		out = append(out, hookSummaryWithRunIDSuffix(runningHookSummary(metadata, r.now()), request.RunIDSuffix))
	}
	return out
}

func (r *HookRunner) runCommand(ctx context.Context, metadata HookMetadata, inputJSON string, cwd string) *hookCommandRunResult {
	started := r.now()
	startedAt := started.UTC().UnixMilli()
	timeoutSec := metadata.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultDiscoveredHookTimeoutSec
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := r.commandForHook(execCtx, metadata)
	cmd.Dir = cwd
	cmd.Env = hookCommandEnv(os.Environ(), metadata.Env)
	// Rust c4513cb982: hook child processes must not inherit Codex launch
	// context (OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE).
	envutil.ScrubCommandEnv(cmd)
	cmd.Stdin = strings.NewReader(inputJSON)
	var stdoutBuffer lockedOutputBuffer
	var stderrBuffer lockedOutputBuffer
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer

	tree, err := startHookProcessTree(cmd)
	stdout := ""
	stderr := ""
	if err == nil {
		waitErr := make(chan error, 1)
		go func() { waitErr <- tree.wait() }()
		select {
		case err = <-waitErr:
		case <-execCtx.Done():
			// Rust dd916428cd: terminate the whole process tree on timeout.
			tree.terminate()
			err = <-waitErr
		}
		stdout = stdoutBuffer.String()
		stderr = stderrBuffer.String()
	}
	completed := r.now()
	result := &hookCommandRunResult{
		StartedAt:   startedAt,
		CompletedAt: completed.UTC().UnixMilli(),
		DurationMS:  completed.Sub(started).Milliseconds(),
		Stdout:      stdout,
		Stderr:      stderr,
	}
	if err == nil {
		if tree != nil {
			tree.preserveDescendants()
		}
		code := int32(0)
		result.ExitCode = &code
		return result
	}
	if execCtx.Err() == context.DeadlineExceeded {
		if tree != nil {
			tree.terminate()
		}
		result.Error = stringPointer(fmt.Sprintf("hook timed out after %ds", timeoutSec))
		return result
	}
	if exitErr, ok := err.(*osexec.ExitError); ok {
		if tree != nil {
			tree.preserveDescendants()
		}
		code := int32(exitErr.ExitCode())
		result.ExitCode = &code
		return result
	}
	if tree != nil {
		tree.terminate()
	}
	result.Error = stringPointer(err.Error())
	return result
}

// runMcpToolHook executes an mcp_tool hook (Rust #38705): the argument
// template is expanded against the hook event JSON, then the executor's
// returned text is interpreted with ordinary command-hook output semantics.
func (r *HookRunner) runMcpToolHook(ctx context.Context, metadata HookMetadata, request *HookRunRequest) *hookCommandRunResult {
	started := r.now()
	startedAt := started.UTC().UnixMilli()
	timeoutSec := metadata.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultDiscoveredHookTimeoutSec
	}
	result := &hookCommandRunResult{
		StartedAt: startedAt,
	}
	if r == nil || r.McpToolHookExecutor == nil {
		result.Error = stringPointer("MCP invocation is not available yet")
		return result
	}
	var hookEvent any
	if strings.TrimSpace(request.InputJSON) != "" {
		if err := json.Unmarshal([]byte(request.InputJSON), &hookEvent); err != nil {
			result.Error = stringPointer("failed to parse hook event input")
			return result
		}
	}
	input, err := expandMcpArgumentTemplate(metadata.Input, hookEvent)
	if err != nil {
		result.Error = stringPointer(err.Error())
		return result
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	output, err := r.McpToolHookExecutor.Execute(execCtx, HookMcpCall{
		Server:  ptrStringValue(metadata.Server),
		Tool:    ptrStringValue(metadata.Tool),
		Input:   input,
		Timeout: time.Duration(timeoutSec) * time.Second,
	})
	cancel()
	completed := r.now()
	result.CompletedAt = completed.UTC().UnixMilli()
	result.DurationMS = completed.Sub(started).Milliseconds()
	if err != nil {
		result.Error = stringPointer(err.Error())
		return result
	}
	code := int32(0)
	result.ExitCode = &code
	result.Stdout = output
	return result
}

// expandMcpArgumentTemplate recursively substitutes ${field.nested}
// placeholders using values from a hook event (Rust mcp_runner.rs). A complete
// placeholder preserves its JSON type; a placeholder embedded in surrounding
// text is rendered as a string. Missing fields fail the hook.
func expandMcpArgumentTemplate(template map[string]any, hookEvent any) (map[string]any, error) {
	if len(template) == 0 {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(template))
	for key, value := range template {
		resolved, err := resolveMcpTemplateValue(value, hookEvent)
		if err != nil {
			return nil, err
		}
		out[key] = resolved
	}
	return out, nil
}

func resolveMcpTemplateValue(value any, hookEvent any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return expandMcpArgumentTemplate(typed, hookEvent)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			resolved, err := resolveMcpTemplateValue(typed[i], hookEvent)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case string:
		return resolveMcpTemplateString(typed, hookEvent)
	default:
		return value, nil
	}
}

var mcpArgumentPlaceholderPattern = regexp.MustCompile(`\$\{([^{}]+)\}`)

func resolveMcpTemplateString(text string, hookEvent any) (any, error) {
	captures := mcpArgumentPlaceholderPattern.FindAllStringSubmatchIndex(text, -1)
	if len(captures) == 0 {
		return text, nil
	}
	if len(captures) == 1 && captures[0][0] == 0 && captures[0][1] == len(text) {
		path := text[2 : len(text)-1]
		return resolveMcpTemplatePath(hookEvent, path)
	}
	var resolved strings.Builder
	previousEnd := 0
	for _, capture := range captures {
		resolved.WriteString(text[previousEnd:capture[0]])
		path := text[capture[2]:capture[3]]
		value, err := resolveMcpTemplatePath(hookEvent, path)
		if err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case string:
			resolved.WriteString(typed)
		case nil:
			resolved.WriteString("null")
		case bool:
			resolved.WriteString(strconv.FormatBool(typed))
		case float64:
			resolved.WriteString(strconv.FormatFloat(typed, 'f', -1, 64))
		case json.Number:
			resolved.WriteString(typed.String())
		default:
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			resolved.Write(data)
		}
		previousEnd = capture[1]
	}
	resolved.WriteString(text[previousEnd:])
	return resolved.String(), nil
}

func resolveMcpTemplatePath(hookEvent any, path string) (any, error) {
	var current any = hookEvent
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("missing field %s in hook event", path)
		}
		next, ok := object[part]
		if !ok {
			return nil, fmt.Errorf("missing field %s in hook event", path)
		}
		current = next
	}
	return current, nil
}

func hookCommandEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	env := make([]string, 0, len(base)+len(overrides))
	seen := map[string]bool{}
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			seen[key] = true
		}
		env = append(env, item)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := overrides[key]
		if seen[key] {
			for i := range env {
				if strings.HasPrefix(env[i], key+"=") {
					env[i] = key + "=" + value
					break
				}
			}
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func (r *HookRunner) commandForHook(ctx context.Context, metadata HookMetadata) *osexec.Cmd {
	command := ptrStringValue(metadata.Command)
	if strings.TrimSpace(r.ShellProgram) == "" {
		if runtime.GOOS == "windows" {
			program := os.Getenv("COMSPEC")
			if strings.TrimSpace(program) == "" {
				program = "cmd.exe"
			}
			return osexec.CommandContext(ctx, program, "/C", command)
		}
		program := os.Getenv("SHELL")
		if strings.TrimSpace(program) == "" {
			program = "/bin/sh"
		}
		return osexec.CommandContext(ctx, program, "-lc", command)
	}
	args := append([]string(nil), r.ShellArgs...)
	args = append(args, command)
	return osexec.CommandContext(ctx, r.ShellProgram, args...)
}

func (r *HookRunner) notify(method NotificationMethod, params any) {
	if r != nil && r.Notify != nil {
		r.Notify(method, params)
	}
}

func (r *HookRunner) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func selectHookHandlers(hooks []HookMetadata, event HookEventName, matcherInputs []string) []HookMetadata {
	out := make([]HookMetadata, 0, len(hooks))
	for _, metadata := range hooks {
		if metadata.EventName != event || !metadata.Enabled {
			continue
		}
		switch metadata.HandlerType {
		case HookHandlerCommand:
			if metadata.Command == nil || strings.TrimSpace(*metadata.Command) == "" {
				continue
			}
		case HookHandlerMCPTool:
			if event == HookEventSessionEnd {
				continue
			}
			if strings.TrimSpace(ptrStringValue(metadata.Server)) == "" || strings.TrimSpace(ptrStringValue(metadata.Tool)) == "" {
				continue
			}
		default:
			continue
		}
		if !hookTrustAllowsExecution(&metadata) {
			continue
		}
		if !hookMatches(event, metadata.Matcher, matcherInputs) {
			continue
		}
		out = append(out, cloneMetadata(metadata))
	}
	sortHooks(out)
	return out
}

func hookTrustAllowsExecution(metadata *HookMetadata) bool {
	if metadata == nil {
		return false
	}
	return metadata.BypassTrust || metadata.IsManaged || metadata.TrustStatus == HookTrustManaged || metadata.TrustStatus == HookTrustTrusted
}

func hookMatches(event HookEventName, matcher *string, matcherInputs []string) bool {
	switch event {
	case HookEventPreToolUse, HookEventPermissionRequest, HookEventPostToolUse, HookEventPreCompact, HookEventPostCompact, HookEventSessionStart, HookEventSubagentStart, HookEventSubagentStop:
		if len(matcherInputs) == 0 {
			return matchesHookMatcher(matcher, nil)
		}
		for _, input := range matcherInputs {
			input := strings.TrimSpace(input)
			if input == "" {
				continue
			}
			if matchesHookMatcher(matcher, &input) {
				return true
			}
		}
		return false
	case HookEventUserPromptSubmit, HookEventStop, HookEventSessionEnd:
		return true
	default:
		return false
	}
}

func matchesHookMatcher(matcher *string, input *string) bool {
	if matcher == nil {
		return true
	}
	value := strings.TrimSpace(*matcher)
	if value == "" || value == "*" {
		return true
	}
	if isExactHookMatcher(value) {
		if input == nil {
			return false
		}
		for _, candidate := range strings.Split(value, "|") {
			if candidate == *input {
				return true
			}
		}
		return false
	}
	if input == nil {
		return false
	}
	regex, err := regexp.Compile(value)
	if err != nil {
		return false
	}
	return regex.MatchString(*input)
}

func validateHookMatcherPattern(matcher string) error {
	matcher = strings.TrimSpace(matcher)
	if matcher == "" || matcher == "*" || isExactHookMatcher(matcher) {
		return nil
	}
	_, err := regexp.Compile(matcher)
	return err
}

func isExactHookMatcher(value string) bool {
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '|' {
			continue
		}
		return false
	}
	return true
}

func runningHookSummary(metadata HookMetadata, now time.Time) HookRunSummary {
	return HookRunSummary{
		ID:            hookRunID(metadata),
		EventName:     metadata.EventName,
		HandlerType:   metadata.HandlerType,
		ExecutionMode: HookExecutionSync,
		Scope:         hookScopeForEvent(metadata.EventName),
		SourcePath:    metadata.SourcePath,
		Source:        metadata.Source,
		DisplayOrder:  metadata.DisplayOrder,
		Status:        HookRunRunning,
		StatusMessage: cloneString(metadata.StatusMessage),
		StartedAt:     now.UTC().UnixMilli(),
		Entries:       []HookOutputEntry{},
	}
}

func completedHookSummary(metadata HookMetadata, runResult *hookCommandRunResult, status HookRunStatus, entries []HookOutputEntry) HookRunSummary {
	completedAt := runResult.CompletedAt
	durationMS := runResult.DurationMS
	return HookRunSummary{
		ID:            hookRunID(metadata),
		EventName:     metadata.EventName,
		HandlerType:   metadata.HandlerType,
		ExecutionMode: HookExecutionSync,
		Scope:         hookScopeForEvent(metadata.EventName),
		SourcePath:    metadata.SourcePath,
		Source:        metadata.Source,
		DisplayOrder:  metadata.DisplayOrder,
		Status:        status,
		StatusMessage: cloneString(metadata.StatusMessage),
		StartedAt:     runResult.StartedAt,
		CompletedAt:   &completedAt,
		DurationMS:    &durationMS,
		Entries:       entries,
	}
}

func hookSummaryWithRunIDSuffix(summary HookRunSummary, suffix string) HookRunSummary {
	if suffix == "" {
		return summary
	}
	summary.ID = summary.ID + ":" + suffix
	return summary
}

func hookRunID(metadata HookMetadata) string {
	return fmt.Sprintf("%s:%d:%s", hookEventRunLabel(metadata.EventName), metadata.DisplayOrder, metadata.SourcePath)
}

func hookEventRunLabel(event HookEventName) string {
	return strings.ReplaceAll(hookEventKeyLabel(event), "_", "-")
}

func hookScopeForEvent(event HookEventName) HookScope {
	switch event {
	case HookEventSessionStart, HookEventSubagentStart:
		return HookScopeThread
	default:
		return HookScopeTurn
	}
}

func hookRunStatus(event HookEventName, runResult *hookCommandRunResult) HookRunStatus {
	if runResult == nil {
		return HookRunFailed
	}
	if runResult.Error != nil {
		return HookRunFailed
	}
	if status, ok := hookExitStatus(event, runResult); ok {
		return status
	}
	if status, ok := parsedHookRunStatus(event, runResult.Stdout); ok {
		return status
	}
	if runResult.ExitCode != nil && *runResult.ExitCode != 0 {
		return HookRunFailed
	}
	return HookRunCompleted
}

func hookExitStatus(event HookEventName, runResult *hookCommandRunResult) (HookRunStatus, bool) {
	if runResult == nil || runResult.ExitCode == nil || *runResult.ExitCode == 0 {
		return "", false
	}
	if *runResult.ExitCode == 2 && hookExitCodeTwoBlocks(event) && strings.TrimSpace(runResult.Stderr) != "" {
		return HookRunBlocked, true
	}
	return HookRunFailed, true
}

func hookExitCodeTwoBlocks(event HookEventName) bool {
	switch event {
	case HookEventPreToolUse, HookEventPermissionRequest, HookEventPostToolUse, HookEventUserPromptSubmit, HookEventStop, HookEventSubagentStop:
		return true
	default:
		return false
	}
}

func parsedHookRunStatus(event HookEventName, stdout string) (HookRunStatus, bool) {
	if strings.TrimSpace(stdout) == "" {
		return "", false
	}
	if !HookOutputLooksLikeJSON(stdout) {
		if event == HookEventStop || event == HookEventSubagentStop {
			return HookRunFailed, true
		}
		return HookRunCompleted, true
	}
	switch event {
	case HookEventSessionStart:
		output := ParseHookSessionStartOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
	case HookEventSubagentStart:
		output := ParseHookSubagentStartOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
	case HookEventPreToolUse:
		output := ParseHookPreToolUseOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.InvalidReason != nil {
			return HookRunFailed, true
		}
		if output.BlockReason != nil {
			return HookRunBlocked, true
		}
	case HookEventPermissionRequest:
		output := ParseHookPermissionRequestOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.InvalidReason != nil {
			return HookRunFailed, true
		}
		if output.Decision != nil && output.Decision.Kind == HookPermissionRequestDeny {
			return HookRunBlocked, true
		}
	case HookEventPostToolUse:
		output := ParseHookPostToolUseOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
		if output.InvalidReason != nil || output.InvalidBlockReason != nil {
			return HookRunFailed, true
		}
		if output.ShouldBlock {
			return HookRunBlocked, true
		}
	case HookEventPreCompact:
		output := ParseHookPreCompactOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
		if output.InvalidReason != nil {
			return HookRunFailed, true
		}
	case HookEventPostCompact:
		output := ParseHookPostCompactOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
		if output.InvalidReason != nil {
			return HookRunFailed, true
		}
	case HookEventUserPromptSubmit:
		output := ParseHookUserPromptSubmitOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
		if output.InvalidBlockReason != nil {
			return HookRunFailed, true
		}
		if output.ShouldBlock {
			return HookRunBlocked, true
		}
	case HookEventStop:
		output := ParseHookStopOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
		if output.InvalidBlockReason != nil {
			return HookRunFailed, true
		}
		if output.ShouldBlock {
			return HookRunBlocked, true
		}
	case HookEventSubagentStop:
		output := ParseHookSubagentStopOutput(stdout)
		if output == nil {
			return HookRunFailed, true
		}
		if output.Universal != nil && !output.Universal.ContinueProcessing {
			return HookRunStopped, true
		}
		if output.InvalidBlockReason != nil {
			return HookRunFailed, true
		}
		if output.ShouldBlock {
			return HookRunBlocked, true
		}
	default:
		return "", false
	}
	return HookRunCompleted, true
}

func hookOutputEntries(event HookEventName, runResult *hookCommandRunResult) []HookOutputEntry {
	if runResult == nil {
		return []HookOutputEntry{{Kind: HookOutputError, Text: "hook execution failed"}}
	}
	entries := []HookOutputEntry{}
	if runResult.Error != nil && strings.TrimSpace(*runResult.Error) != "" {
		entries = append(entries, HookOutputEntry{Kind: HookOutputError, Text: strings.TrimSpace(*runResult.Error)})
	}
	if runResult.ExitCode != nil && *runResult.ExitCode != 0 {
		if *runResult.ExitCode == 2 && hookExitCodeTwoBlocks(event) {
			if text := strings.TrimSpace(runResult.Stderr); text != "" {
				entries = append(entries, HookOutputEntry{Kind: HookOutputFeedback, Text: text})
			} else {
				entries = append(entries, HookOutputEntry{Kind: HookOutputError, Text: hookExitCodeTwoMissingReason(event)})
			}
			return entries
		}
		entries = append(entries, HookOutputEntry{Kind: HookOutputError, Text: fmt.Sprintf("hook exited with code %d", *runResult.ExitCode)})
	}
	if text := strings.TrimSpace(runResult.Stderr); text != "" {
		entries = append(entries, HookOutputEntry{Kind: HookOutputError, Text: text})
	}
	entries = append(entries, parsedHookOutputEntries(event, runResult.Stdout)...)
	return entries
}

type hookRunEffectResult struct {
	Blocked            bool
	BlockReason        string
	Stopped            bool
	StopReason         string
	UpdatedInput       any
	AdditionalContexts []string
	FeedbackMessages   []string
}

func hookRunEffect(event HookEventName, runResult *hookCommandRunResult) *hookRunEffectResult {
	if runResult == nil {
		return nil
	}
	effect := &hookRunEffectResult{}
	if runResult.ExitCode != nil && *runResult.ExitCode == 2 && hookExitCodeTwoBlocks(event) {
		if text := strings.TrimSpace(runResult.Stderr); text != "" {
			effect.Blocked = true
			effect.BlockReason = text
			effect.FeedbackMessages = append(effect.FeedbackMessages, text)
		}
		return effect
	}
	if strings.TrimSpace(runResult.Stdout) == "" {
		return effect
	}
	if !HookOutputLooksLikeJSON(runResult.Stdout) {
		if event == HookEventSessionStart || event == HookEventSubagentStart || event == HookEventUserPromptSubmit {
			effect.AdditionalContexts = append(effect.AdditionalContexts, strings.TrimSpace(runResult.Stdout))
		}
		return effect
	}
	switch event {
	case HookEventSessionStart:
		output := ParseHookSessionStartOutput(runResult.Stdout)
		if output == nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, output.AdditionalContext, nil, true)
	case HookEventSubagentStart:
		output := ParseHookSubagentStartOutput(runResult.Stdout)
		if output == nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, output.AdditionalContext, nil, false)
	case HookEventPreToolUse:
		output := ParseHookPreToolUseOutput(runResult.Stdout)
		if output == nil || output.InvalidReason != nil {
			return effect
		}
		if output.AdditionalContext != nil && strings.TrimSpace(*output.AdditionalContext) != "" {
			effect.AdditionalContexts = append(effect.AdditionalContexts, strings.TrimSpace(*output.AdditionalContext))
		}
		if output.BlockReason != nil && strings.TrimSpace(*output.BlockReason) != "" {
			effect.Blocked = true
			effect.BlockReason = strings.TrimSpace(*output.BlockReason)
			effect.FeedbackMessages = append(effect.FeedbackMessages, effect.BlockReason)
		}
		effect.UpdatedInput = output.UpdatedInput
	case HookEventPermissionRequest:
		output := ParseHookPermissionRequestOutput(runResult.Stdout)
		if output == nil || output.InvalidReason != nil {
			return effect
		}
		if output.Decision != nil && output.Decision.Kind == HookPermissionRequestDeny && output.Decision.Message != nil {
			effect.Blocked = true
			effect.BlockReason = strings.TrimSpace(*output.Decision.Message)
			if effect.BlockReason != "" {
				effect.FeedbackMessages = append(effect.FeedbackMessages, effect.BlockReason)
			}
		}
	case HookEventPostToolUse:
		output := ParseHookPostToolUseOutput(runResult.Stdout)
		if output == nil || output.InvalidReason != nil || output.InvalidBlockReason != nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, output.AdditionalContext, output.Reason, true)
		if output.ShouldBlock {
			effect.Blocked = true
			effect.BlockReason = trimmedHookString(output.Reason)
		}
	case HookEventPreCompact:
		output := ParseHookPreCompactOutput(runResult.Stdout)
		if output == nil || output.InvalidReason != nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, output.AdditionalContext, nil, true)
	case HookEventPostCompact:
		output := ParseHookPostCompactOutput(runResult.Stdout)
		if output == nil || output.InvalidReason != nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, output.AdditionalContext, nil, true)
	case HookEventUserPromptSubmit:
		output := ParseHookUserPromptSubmitOutput(runResult.Stdout)
		if output == nil || output.InvalidBlockReason != nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, output.AdditionalContext, output.Reason, true)
		if output.ShouldBlock {
			effect.Blocked = true
			effect.BlockReason = trimmedHookString(output.Reason)
		}
	case HookEventStop:
		output := ParseHookStopOutput(runResult.Stdout)
		if output == nil || output.InvalidBlockReason != nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, nil, output.Reason, true)
		if output.ShouldBlock {
			effect.Blocked = true
			effect.BlockReason = trimmedHookString(output.Reason)
		}
	case HookEventSubagentStop:
		output := ParseHookSubagentStopOutput(runResult.Stdout)
		if output == nil || output.InvalidBlockReason != nil {
			return effect
		}
		applyContextInjectingEffect(effect, output.Universal, nil, output.Reason, true)
		if output.ShouldBlock {
			effect.Blocked = true
			effect.BlockReason = trimmedHookString(output.Reason)
		}
	}
	return effect
}

func applyContextInjectingEffect(effect *hookRunEffectResult, universal *HookUniversalOutput, context *string, feedback *string, honorStop bool) {
	if effect == nil {
		return
	}
	if context != nil && strings.TrimSpace(*context) != "" {
		effect.AdditionalContexts = append(effect.AdditionalContexts, strings.TrimSpace(*context))
	}
	if honorStop && universal != nil && !universal.ContinueProcessing {
		effect.Stopped = true
		effect.StopReason = trimmedHookString(universal.StopReason)
		message := trimmedHookString(feedback)
		if message == "" {
			message = effect.StopReason
		}
		if message != "" {
			effect.FeedbackMessages = append(effect.FeedbackMessages, message)
		}
	} else if feedback != nil && strings.TrimSpace(*feedback) != "" {
		effect.FeedbackMessages = append(effect.FeedbackMessages, strings.TrimSpace(*feedback))
	}
}

func mergeHookRunEffect(result *HookRunResult, effect *hookRunEffectResult) {
	if result == nil || effect == nil {
		return
	}
	if effect.Blocked {
		result.Blocked = true
		if result.BlockReason == "" {
			result.BlockReason = effect.BlockReason
		}
	}
	if effect.Stopped {
		result.Stopped = true
		if result.StopReason == "" {
			result.StopReason = effect.StopReason
		}
	}
	if effect.UpdatedInput != nil {
		result.UpdatedInput = effect.UpdatedInput
	}
	result.AdditionalContexts = append(result.AdditionalContexts, effect.AdditionalContexts...)
	result.FeedbackMessage = joinHookMessages(result.FeedbackMessage, effect.FeedbackMessages...)
}

func joinHookMessages(existing string, values ...string) string {
	chunks := []string{}
	if strings.TrimSpace(existing) != "" {
		chunks = append(chunks, strings.TrimSpace(existing))
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			chunks = append(chunks, strings.TrimSpace(value))
		}
	}
	return strings.Join(chunks, "\n\n")
}

func hookExitCodeTwoMissingReason(event HookEventName) string {
	switch event {
	case HookEventPreToolUse:
		return "PreToolUse hook exited with code 2 but did not write a blocking reason to stderr"
	case HookEventPostToolUse:
		return "PostToolUse hook exited with code 2 but did not write feedback to stderr"
	case HookEventUserPromptSubmit:
		return "UserPromptSubmit hook exited with code 2 but did not write a blocking reason to stderr"
	case HookEventPermissionRequest:
		return "PermissionRequest hook exited with code 2 but did not write a denial reason to stderr"
	case HookEventStop:
		return "Stop hook exited with code 2 but did not write a continuation prompt to stderr"
	case HookEventSubagentStop:
		return "SubagentStop hook exited with code 2 but did not write a continuation prompt to stderr"
	default:
		return "hook exited with code 2 but did not write a blocking reason to stderr"
	}
}

func parsedHookOutputEntries(event HookEventName, stdout string) []HookOutputEntry {
	if strings.TrimSpace(stdout) == "" {
		return nil
	}
	if !HookOutputLooksLikeJSON(stdout) {
		if event == HookEventSessionStart || event == HookEventSubagentStart || event == HookEventUserPromptSubmit {
			return []HookOutputEntry{{Kind: HookOutputContext, Text: strings.TrimSpace(stdout)}}
		}
		if event == HookEventStop || event == HookEventSubagentStop {
			return []HookOutputEntry{{Kind: HookOutputError, Text: invalidHookJSONMessage(event)}}
		}
		return nil
	}
	if parseHookOutputEnvelope(stdout) == nil {
		return []HookOutputEntry{{Kind: HookOutputError, Text: invalidHookJSONMessage(event)}}
	}
	switch event {
	case HookEventSessionStart:
		return hookEntriesFromSessionStart(ParseHookSessionStartOutput(stdout))
	case HookEventSubagentStart:
		return hookEntriesFromSubagentStart(ParseHookSubagentStartOutput(stdout))
	case HookEventPreToolUse:
		return hookEntriesFromPreToolUse(ParseHookPreToolUseOutput(stdout))
	case HookEventPermissionRequest:
		return hookEntriesFromPermissionRequest(ParseHookPermissionRequestOutput(stdout))
	case HookEventPostToolUse:
		return hookEntriesFromPostToolUse(ParseHookPostToolUseOutput(stdout))
	case HookEventPreCompact:
		return hookEntriesFromStateless(ParseHookPreCompactOutput(stdout), "PreCompact hook stopped execution")
	case HookEventPostCompact:
		return hookEntriesFromStateless(ParseHookPostCompactOutput(stdout), "PostCompact hook stopped execution")
	case HookEventUserPromptSubmit:
		return hookEntriesFromUserPromptSubmit(ParseHookUserPromptSubmitOutput(stdout))
	case HookEventStop:
		return hookEntriesFromStop(ParseHookStopOutput(stdout))
	case HookEventSubagentStop:
		return hookEntriesFromStop(ParseHookSubagentStopOutput(stdout))
	default:
		return nil
	}
}

func invalidHookJSONMessage(event HookEventName) string {
	switch event {
	case HookEventSessionStart:
		return "hook returned invalid session start JSON output"
	case HookEventSubagentStart:
		return "hook returned invalid subagent start JSON output"
	case HookEventPreToolUse:
		return "hook returned invalid pre-tool-use JSON output"
	case HookEventPermissionRequest:
		return "hook returned invalid permission-request JSON output"
	case HookEventPostToolUse:
		return "hook returned invalid post-tool-use JSON output"
	case HookEventPreCompact:
		return "hook returned invalid PreCompact hook JSON output"
	case HookEventPostCompact:
		return "hook returned invalid PostCompact hook JSON output"
	case HookEventUserPromptSubmit:
		return "hook returned invalid user prompt submit JSON output"
	case HookEventStop:
		return "hook returned invalid stop hook JSON output"
	case HookEventSubagentStop:
		return "hook returned invalid subagent stop hook JSON output"
	default:
		return "hook returned invalid JSON output"
	}
}

func hookEntriesFromSessionStart(output *HookSessionStartOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, output.AdditionalContext, nil, nil, true, "")
}

func hookEntriesFromSubagentStart(output *HookSessionStartOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, output.AdditionalContext, nil, nil, false, "")
}

func hookEntriesFromPreToolUse(output *HookPreToolUseOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, output.AdditionalContext, output.BlockReason, output.InvalidReason, false, "")
}

func hookEntriesFromPermissionRequest(output *HookPermissionRequestOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	var blockReason *string
	if output.Decision != nil && output.Decision.Kind == HookPermissionRequestDeny {
		blockReason = output.Decision.Message
	}
	return hookEntriesFromUniversal(output.Universal, nil, blockReason, output.InvalidReason, false, "")
}

func hookEntriesFromPostToolUse(output *HookPostToolUseOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, output.AdditionalContext, output.Reason, firstStringPointer(output.InvalidReason, output.InvalidBlockReason), true, "PostToolUse hook stopped execution")
}

func hookEntriesFromStateless(output *HookStatelessOutput, stopFallback string) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, output.AdditionalContext, nil, output.InvalidReason, true, stopFallback)
}

func hookEntriesFromUserPromptSubmit(output *HookUserPromptSubmitOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, output.AdditionalContext, output.Reason, output.InvalidBlockReason, true, "")
}

func hookEntriesFromStop(output *HookStopOutput) []HookOutputEntry {
	if output == nil {
		return nil
	}
	return hookEntriesFromUniversal(output.Universal, nil, output.Reason, output.InvalidBlockReason, true, "")
}

func hookEntriesFromUniversal(universal *HookUniversalOutput, context *string, feedback *string, invalid *string, honorStop bool, stopFallback string) []HookOutputEntry {
	entries := []HookOutputEntry{}
	if universal != nil && universal.SystemMessage != nil && strings.TrimSpace(*universal.SystemMessage) != "" {
		entries = append(entries, HookOutputEntry{Kind: HookOutputFeedback, Text: strings.TrimSpace(*universal.SystemMessage)})
	}
	if invalid == nil && context != nil && strings.TrimSpace(*context) != "" {
		entries = append(entries, HookOutputEntry{Kind: HookOutputContext, Text: strings.TrimSpace(*context)})
	}
	if invalid == nil && honorStop && universal != nil && !universal.ContinueProcessing {
		text := trimmedHookString(universal.StopReason)
		if text == "" {
			text = stopFallback
		}
		if text != "" {
			entries = append(entries, HookOutputEntry{Kind: HookOutputStop, Text: text})
		}
	} else if invalid == nil && feedback != nil && strings.TrimSpace(*feedback) != "" {
		entries = append(entries, HookOutputEntry{Kind: HookOutputFeedback, Text: strings.TrimSpace(*feedback)})
	}
	if invalid != nil && strings.TrimSpace(*invalid) != "" {
		entries = append(entries, HookOutputEntry{Kind: HookOutputError, Text: strings.TrimSpace(*invalid)})
	}
	return entries
}

func trimmedHookString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func firstStringPointer(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func hookInputJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
