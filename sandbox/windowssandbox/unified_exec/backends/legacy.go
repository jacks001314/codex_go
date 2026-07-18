package backends

import (
	"fmt"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/conpty"
)

func RunLegacyBackend(req *windowssandbox.CaptureRequest) (*windowssandbox.CaptureResult, error) {
	if req != nil && req.TTY {
		return runLegacyBackendPTY(req)
	}
	return windowssandbox.RunWindowsSandboxCaptureWithFilesystemOverrides(req)
}

func runLegacyBackendPTY(req *windowssandbox.CaptureRequest) (*windowssandbox.CaptureResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	context, err := windowssandbox.PrepareLegacySpawnContext(req, windowssandbox.SpawnPrepOptions{
		InheritPath:         false,
		AddGitSafeDirectory: false,
	})
	if err != nil {
		return nil, err
	}
	if !context.Permissions.HasFullDiskReadAccess() {
		return nil, fmt.Errorf("restricted read-only access requires the elevated Windows sandbox backend")
	}
	if len(req.DenyReadPaths) > 0 {
		return nil, fmt.Errorf("deny-read overrides require the elevated Windows sandbox backend")
	}
	capabilityRoots := windowssandbox.LegacySessionCapabilityRoots(context.Permissions, context.CurrentDir, context.Env, req.CodexHome)
	security, err := windowssandbox.PrepareLegacySessionSecurity(context.UsesWriteCapabilities, req.CodexHome, req.CWD, capabilityRoots)
	if err != nil {
		return nil, err
	}
	defer windowssandbox.CloseTokenHandle(security.Token)

	windowssandbox.AllowNullDeviceForWorkspaceWrite(context.UsesWriteCapabilities)
	if err := windowssandbox.ApplyLegacySessionACLRules(
		context.Permissions,
		req.CodexHome,
		context.CurrentDir,
		context.Env,
		nil,
		req.DenyWritePaths,
		windowssandbox.LegacyAclSIDs{ReadonlySID: security.ReadonlySID, WriteRootSIDs: security.WriteRootSIDs},
	); err != nil {
		return nil, err
	}

	process, instance, err := conpty.SpawnProcessAsUserWithToken(conpty.SpawnRequest{
		Token:             security.Token,
		Command:           req.Command,
		CWD:               req.CWD,
		Env:               context.Env,
		UsePrivateDesktop: req.UsePrivateDesktop,
		LogsBaseDir:       context.LogsBaseDir,
	})
	if err != nil {
		return nil, err
	}
	defer process.Close()
	defer instance.Close()
	if !req.StdinOpen {
		_ = instance.CloseInputWrite()
	}

	var stdout []byte
	stdoutDone, err := windowssandbox.ReadHandleLoop(instance.TakeOutputRead(), func(chunk []byte) {
		stdout = append(stdout, chunk...)
	})
	if err != nil {
		return nil, err
	}
	outcome, err := windowssandbox.WaitCreatedProcess(process, req.TimeoutMS, req.Cancellation)
	if err != nil {
		return nil, err
	}
	timedOut := outcome == windowssandbox.ProcessWaitTimedOut
	cancelled := outcome == windowssandbox.ProcessWaitCancelled
	exitCode := 1
	if timedOut || cancelled {
		_ = windowssandbox.TerminateCreatedProcess(process, 1)
	} else {
		exitCode, err = windowssandbox.CreatedProcessExitCode(process)
		if err != nil {
			return nil, err
		}
	}
	<-stdoutDone
	if timedOut {
		exitCode = 128 + 64
	}
	if exitCode == 0 {
		_ = windowssandbox.LogSuccess(req.Command, req.CodexHome)
	} else {
		_ = windowssandbox.LogFailure(req.Command, fmt.Sprintf("exit code %d", exitCode), req.CodexHome)
	}
	return &windowssandbox.CaptureResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		TimedOut: timedOut,
	}, nil
}
