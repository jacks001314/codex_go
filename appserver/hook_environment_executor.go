package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/execpolicy"
	"codex_go/execserver"
	"codex_go/tool"
	"codex_go/utils"

	"github.com/google/uuid"
)

const (
	environmentHookOutputMaxBytes  = 1024 * 1024
	environmentHookReadMaxBytes    = 64 * 1024
	environmentHookReadWaitMS      = uint64(1000)
	environmentHookConnectBudget   = 10 * time.Second
	environmentHookTerminateBudget = 5 * time.Second
)

// appServerHookEnvironmentExecutor runs environment command hooks on remote
// executors (Rust hook_environment_executor::EnvironmentCommandExecutor,
// #56304). Local environments keep the hook runner's in-process launcher, so
// targets only carry environments that are not hosted here.
type appServerHookEnvironmentExecutor struct {
	targets map[string]tool.UnifiedExecEnvironment
}

func newAppServerHookEnvironmentExecutor(targets map[string]tool.UnifiedExecEnvironment) *appServerHookEnvironmentExecutor {
	if len(targets) == 0 {
		return nil
	}
	return &appServerHookEnvironmentExecutor{targets: targets}
}

func (e *appServerHookEnvironmentExecutor) Execute(ctx context.Context, environmentID string, command string, env map[string]string, inputJSON string, timeout time.Duration) (*EnvironmentHookOutput, error) {
	if e == nil {
		return nil, errors.New("environment hook executor is unavailable")
	}
	target, ok := e.targets[strings.TrimSpace(environmentID)]
	if !ok {
		return nil, errors.New("hook environment was not ready for this event")
	}
	if target.Shell == nil {
		return nil, errors.New("hook environment did not report a shell")
	}
	script, err := environmentHookScript(target.Shell.Type, command)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = time.Duration(defaultDiscoveredHookTimeoutSec) * time.Second
	}
	deadline := time.Now().Add(timeout)
	execCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	client, err := dialHookEnvironment(execCtx, target)
	if err != nil {
		if execCtx.Err() != nil {
			return nil, errors.New("environment command hook timed out during launch")
		}
		return nil, err
	}
	defer client.Close()

	useLoginShell := target.AllowLoginShell != nil && *target.AllowLoginShell
	argv := target.Shell.DeriveExecArgs(script, useLoginShell)
	processID := "hook-" + uuid.NewString()
	if _, err := client.Start(execCtx, &execserver.ExecParams{
		ProcessID: processID,
		Argv:      argv,
		CWD:       environmentExecCWD(target.CWD),
		EnvPolicy: hookExecEnvPolicy(target.ShellEnvironmentPolicy, target.CWD),
		Env:       env,
		// Exec-server keeps pipe stdin open and there is no close-stdin RPC, so
		// the script reads exactly one line and then reaches EOF (Rust).
		PipeStdin: true,
	}); err != nil {
		if execCtx.Err() != nil {
			return nil, errors.New("environment command hook timed out during launch")
		}
		return nil, err
	}

	written, err := client.Write(execCtx, &execserver.WriteParams{
		ProcessID: processID,
		Chunk:     inputJSON + "\n",
		WriteID:   uuid.NewString(),
	})
	if err != nil {
		terminateHookProcess(client, processID)
		if execCtx.Err() != nil {
			return nil, errors.New("environment command hook timed out")
		}
		return nil, err
	}
	if written != nil && written.Status != "accepted" {
		terminateHookProcess(client, processID)
		return nil, fmt.Errorf("hook process did not accept its input: %s", written.Status)
	}

	output, err := readHookEnvironmentOutput(execCtx, client, processID)
	if err != nil {
		terminateHookProcess(client, processID)
		if execCtx.Err() != nil {
			return nil, errors.New("environment command hook timed out")
		}
		return nil, err
	}
	return output, nil
}

// environmentExecCWD renders the environment's working directory as a file URI
// so a Windows path still resolves when this process runs on another platform.
func environmentExecCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || strings.HasPrefix(strings.ToLower(cwd), "file:") {
		return cwd
	}
	legacy := utils.NewLegacyAppPathString(cwd)
	if uri, ok := legacy.ToInferredPathURI(); ok {
		return uri.String()
	}
	return cwd
}

func dialHookEnvironment(ctx context.Context, target tool.UnifiedExecEnvironment) (*execserver.Client, error) {
	budget := target.ConnectBudget(environmentHookConnectBudget)
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < budget {
			budget = remaining
		}
	}
	if budget <= 0 {
		return nil, context.DeadlineExceeded
	}
	connectCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	options := execserver.DialClientOptions{
		ClientName:        "codex-go-hooks",
		InitializeTimeout: target.ExecServerInitializeTimeout,
		Provisioned:       target.Provisioned,
	}
	if target.NoiseProvider != nil {
		return execserver.DialNoiseRendezvousClient(connectCtx, target.NoiseProvider, options)
	}
	options.HTTPHeaders = target.ExecServerHTTPHeaders.Clone()
	options.StdioCommand = target.ExecServerStdioCommand
	return execserver.DialClientWithOptions(connectCtx, target.ExecServerURL, options)
}

// readHookEnvironmentOutput drains retained output until the executor reports a
// closed process with an empty final page, mirroring Rust's hook read loop. The
// closed flag describes the process, not this bounded page.
func readHookEnvironmentOutput(ctx context.Context, client *execserver.Client, processID string) (*EnvironmentHookOutput, error) {
	var afterSeq *uint64
	stdout := make([]byte, 0, environmentHookReadMaxBytes)
	stderr := make([]byte, 0, environmentHookReadMaxBytes)
	maxBytes := environmentHookReadMaxBytes
	waitMS := environmentHookReadWaitMS
	for {
		response, err := client.Read(ctx, &execserver.ReadParams{
			ProcessID: processID,
			AfterSeq:  afterSeq,
			MaxBytes:  &maxBytes,
			WaitMS:    &waitMS,
		})
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("exec-server returned an empty process/read response")
		}
		if response.Failure != nil {
			return nil, errors.New(*response.Failure)
		}
		drained := response.Closed && len(response.Chunks) == 0
		for _, chunk := range response.Chunks {
			switch chunk.Stream {
			case "stderr":
				stderr = appendBoundedHookOutput(stderr, chunk.Chunk)
			default:
				stdout = appendBoundedHookOutput(stdout, chunk.Chunk)
			}
		}
		if response.NextSeq > 0 {
			seq := response.NextSeq - 1
			afterSeq = &seq
		}
		if drained {
			if response.ExitCode == nil {
				return nil, errors.New("hook process closed without exit status")
			}
			return &EnvironmentHookOutput{
				ExitCode: int32(*response.ExitCode),
				Stdout:   string(stdout),
				Stderr:   string(stderr),
			}, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

func appendBoundedHookOutput(target []byte, chunk []byte) []byte {
	remaining := environmentHookOutputMaxBytes - len(target)
	if remaining <= 0 {
		return target
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	return append(target, chunk...)
}

func terminateHookProcess(client *execserver.Client, processID string) {
	if client == nil || strings.TrimSpace(processID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), environmentHookTerminateBudget)
	defer cancel()
	_, _ = client.Terminate(ctx, &execserver.TerminateParams{ProcessID: processID})
}

// environmentHookScript feeds exactly one line of hook input to the configured
// command and preserves its exit status, mirroring Rust #56304.
func environmentHookScript(shellType tool.ShellType, command string) (string, error) {
	switch shellType {
	case tool.ShellPowerShell:
		// A script block can reset $?. Run the hook at the top level and
		// preserve its exit code before PowerShell's -Command reduces it.
		return "$global:LASTEXITCODE = 0\n" +
			"[Console]::In.ReadLine() | " + command + "\n" +
			"if ($?) { exit 0 }\n" +
			"if ($LASTEXITCODE) { exit $LASTEXITCODE }\n" +
			"exit 1", nil
	case tool.ShellCmd:
		return "", errors.New("environment command hooks require PowerShell on Windows")
	default:
		// Start the hook on a new line so a leading `(` cannot become Bash's
		// arithmetic `((`.
		return "IFS= read -r codex_hook_input; printf '%s' \"$codex_hook_input\" | (\n" +
			command + "\n)", nil
	}
}

// hookExecEnvPolicy converts an environment's shell_environment_policy table to
// the exec-server wire form so remote hooks inherit the same variables a shell
// in that environment would.
func hookExecEnvPolicy(table map[string]any, cwd string) *execserver.ExecEnvPolicy {
	policy := execpolicy.EnvPolicyFromShellEnvironmentPolicy(table, cwd)
	out := &execserver.ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true}
	if policy == nil {
		return out
	}
	if inherit := strings.TrimSpace(policy.Inherit); inherit != "" {
		out.Inherit = inherit
	}
	if policy.IgnoreDefaultExcludes != nil {
		out.IgnoreDefaultExcludes = *policy.IgnoreDefaultExcludes
	}
	for _, pattern := range policy.Exclude {
		out.Exclude = append(out.Exclude, pattern.Value)
	}
	for _, pattern := range policy.IncludeOnly {
		out.IncludeOnly = append(out.IncludeOnly, pattern.Value)
	}
	out.Set = policy.Set
	return out
}
