package windowssandbox

import (
	"fmt"

	coresandbox "codex_go/internal/sandbox"
)

type LegacyPreflightRequest struct {
	PermissionProfileID string
	WorkspaceRoots      []string
	CodexHome           string
	CWD                 string
	Env                 map[string]string
}

func RunWindowsSandboxCapture(req *CaptureRequest) (*CaptureResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return RunWindowsSandboxCaptureWithFilesystemOverrides(req)
}

func RunWindowsSandboxCaptureWithFilesystemOverrides(req *CaptureRequest) (*CaptureResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	context, err := PrepareLegacySpawnContext(req, SpawnPrepOptions{
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
	capabilityRoots := LegacySessionCapabilityRoots(context.Permissions, context.CurrentDir, context.Env, req.CodexHome)
	security, err := PrepareLegacySessionSecurity(context.UsesWriteCapabilities, req.CodexHome, req.CWD, capabilityRoots)
	if err != nil {
		return nil, err
	}
	defer CloseTokenHandle(security.Token)

	AllowNullDeviceForWorkspaceWrite(context.UsesWriteCapabilities)
	if err := ApplyLegacySessionACLRules(
		context.Permissions,
		req.CodexHome,
		context.CurrentDir,
		context.Env,
		nil,
		req.DenyWritePaths,
		LegacyAclSIDs{ReadonlySID: security.ReadonlySID, WriteRootSIDs: security.WriteRootSIDs},
	); err != nil {
		return nil, err
	}
	handles, err := SpawnProcessWithPipesWithToken(PipeSpawnRequest{
		Token:             security.Token,
		Command:           req.Command,
		CWD:               req.CWD,
		Env:               context.Env,
		StdinMode:         StdinModeClosed,
		StderrMode:        StderrModeSeparate,
		UsePrivateDesktop: req.UsePrivateDesktop,
		LogsBaseDir:       context.LogsBaseDir,
	})
	if err != nil {
		return nil, err
	}
	defer handles.Close()

	var stdout []byte
	stdoutDone, err := ReadHandleLoop(handles.StdoutRead, func(chunk []byte) {
		stdout = append(stdout, chunk...)
	})
	if err != nil {
		return nil, err
	}
	var stderr []byte
	stderrDone, err := ReadHandleLoop(handles.StderrRead, func(chunk []byte) {
		stderr = append(stderr, chunk...)
	})
	if err != nil {
		return nil, err
	}
	outcome, err := WaitCreatedProcess(handles.Process, req.TimeoutMS, req.Cancellation)
	if err != nil {
		return nil, err
	}
	timedOut := outcome == ProcessWaitTimedOut
	cancelled := outcome == ProcessWaitCancelled
	exitCode := 1
	if timedOut || cancelled {
		_ = TerminateCreatedProcess(handles.Process, 1)
	} else {
		exitCode, err = CreatedProcessExitCode(handles.Process)
		if err != nil {
			return nil, err
		}
	}
	<-stdoutDone
	<-stderrDone
	handles.StdoutRead = 0
	handles.StderrRead = 0
	if timedOut {
		exitCode = 128 + 64
	}
	if exitCode == 0 {
		_ = LogSuccess(req.Command, req.CodexHome)
	} else {
		_ = LogFailure(req.Command, fmt.Sprintf("exit code %d", exitCode), req.CodexHome)
	}
	return &CaptureResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		TimedOut: timedOut,
	}, nil
}

func RunWindowsSandboxLegacyPreflight(req *LegacyPreflightRequest) error {
	if req == nil {
		return ErrInvalidRequest
	}
	profile, _, err := coresandbox.ResolvePermissionProfile(req.PermissionProfileID)
	if err != nil {
		return err
	}
	permissions, err := ResolvePermissions(profile, req.WorkspaceRoots)
	if err != nil {
		return nil
	}
	if !permissions.UsesWriteCapabilitiesForCWD(req.CWD, req.Env) {
		return nil
	}
	if err := EnsureCodexHomeExists(req.CodexHome); err != nil {
		return err
	}
	capabilityRoots := LegacySessionCapabilityRoots(permissions, req.CWD, req.Env, req.CodexHome)
	writeRootSIDs, err := RootCapabilitySIDs(req.CodexHome, req.CWD, capabilityRoots)
	if err != nil {
		return err
	}
	return ApplyLegacySessionACLRules(
		permissions,
		req.CodexHome,
		req.CWD,
		req.Env,
		nil,
		nil,
		LegacyAclSIDs{WriteRootSIDs: writeRootSIDs},
	)
}
