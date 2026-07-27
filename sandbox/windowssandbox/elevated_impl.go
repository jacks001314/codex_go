package windowssandbox

import (
	"fmt"
	"time"

	"codex_go/sandbox/windowssandbox/elevated"
)

type ElevatedSandboxProfileCaptureRequest struct {
	Capture CaptureRequest
}

func SpawnWindowsSandboxElevatedRunnerTransport(capture *CaptureRequest) (*elevated.RunnerTransport, error) {
	if capture == nil {
		return nil, ErrInvalidRequest
	}
	if err := capture.Validate(); err != nil {
		return nil, err
	}
	profile, err := ResolveCapturePermissionProfile(capture)
	if err != nil {
		return nil, err
	}
	permissions, err := ResolvePermissions(profile, capture.WorkspaceRoots)
	if err != nil {
		return nil, err
	}
	envMap := cloneEnv(capture.Env)
	if envMap == nil {
		envMap = map[string]string{}
	}
	context, err := PrepareElevatedSpawnContextForPermissions(
		permissions,
		capture.CodexHome,
		capture.CWD,
		envMap,
		capture.Command,
		capture.ReadRootsOverride,
		capture.ReadRootsIncludePlatformDefaults,
		capture.WriteRootsOverride,
		capture.WriteRootsOverrideSet,
		capture.DenyReadPaths,
		capture.DenyWritePaths,
		capture.ProxyEnforced,
		effectiveProxySettingsMode(capture.ProxySettingsMode),
		capture.DisallowSetupElevation,
	)
	if err != nil {
		return nil, err
	}
	var timeout *uint64
	if capture.TimeoutMS != nil {
		value := uint64(*capture.TimeoutMS)
		timeout = &value
	}
	spawnRequest := elevated.SpawnRequest{
		Command:           cloneStrings(capture.Command),
		CWD:               capture.CWD,
		Env:               envMap,
		PermissionProfile: profile,
		WorkspaceRoots:    cloneStrings(capture.WorkspaceRoots),
		CodexHome:         context.SandboxBase,
		RealCodexHome:     capture.CodexHome,
		CapSIDs:           cloneStrings(context.CapSIDs),
		TimeoutMS:         timeout,
		TTY:               capture.TTY,
		StdinOpen:         capture.StdinOpen,
		UsePrivateDesktop: capture.UsePrivateDesktop,
	}
	creds := toElevatedRunnerCreds(context.SandboxCreds)
	return elevated.RetryRunnerSpawnOnce(
		creds,
		capture.Command,
		func(creds elevated.SandboxCredentials) (*elevated.RunnerTransport, error) {
			return elevated.SpawnRunnerTransport(capture.CodexHome, capture.CWD, &creds, "", spawnRequest)
		},
		func() (elevated.SandboxCredentials, error) {
			refreshed, err := refreshLogonSandboxCredsForPermissions(
				permissions,
				capture.CWD,
				envMap,
				capture.CodexHome,
				capture.ReadRootsOverride,
				capture.ReadRootsOverrideSet,
				capture.ReadRootsIncludePlatformDefaults,
				capture.WriteRootsOverride,
				capture.WriteRootsOverrideSet,
				capture.DenyReadPaths,
				capture.DenyWritePaths,
				capture.ProxyEnforced,
				effectiveProxySettingsMode(capture.ProxySettingsMode),
				!capture.DisallowSetupElevation,
			)
			if err != nil {
				return elevated.SandboxCredentials{}, err
			}
			return toElevatedRunnerCreds(refreshed), nil
		},
	)
}

func RunWindowsSandboxCaptureForPermissionProfileElevated(req *ElevatedSandboxProfileCaptureRequest) (*CaptureResult, error) {
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if err := req.Capture.Validate(); err != nil {
		return nil, err
	}
	capture := &req.Capture
	profile, err := ResolveCapturePermissionProfile(capture)
	if err != nil {
		return nil, err
	}
	permissions, err := ResolvePermissions(profile, capture.WorkspaceRoots)
	if err != nil {
		return nil, err
	}
	envMap := cloneEnv(capture.Env)
	if envMap == nil {
		envMap = map[string]string{}
	}
	context, err := PrepareElevatedSpawnContextForPermissions(
		permissions,
		capture.CodexHome,
		capture.CWD,
		envMap,
		capture.Command,
		capture.ReadRootsOverride,
		capture.ReadRootsIncludePlatformDefaults,
		capture.WriteRootsOverride,
		capture.WriteRootsOverrideSet,
		capture.DenyReadPaths,
		capture.DenyWritePaths,
		capture.ProxyEnforced,
		effectiveProxySettingsMode(capture.ProxySettingsMode),
		capture.DisallowSetupElevation,
	)
	if err != nil {
		return nil, err
	}
	var timeout *uint64
	if capture.TimeoutMS != nil {
		value := uint64(*capture.TimeoutMS)
		timeout = &value
	}
	spawnRequest := elevated.SpawnRequest{
		Command:           cloneStrings(capture.Command),
		CWD:               capture.CWD,
		Env:               envMap,
		PermissionProfile: profile,
		WorkspaceRoots:    cloneStrings(capture.WorkspaceRoots),
		CodexHome:         context.SandboxBase,
		RealCodexHome:     capture.CodexHome,
		CapSIDs:           cloneStrings(context.CapSIDs),
		TimeoutMS:         timeout,
		TTY:               capture.TTY,
		StdinOpen:         capture.StdinOpen,
		UsePrivateDesktop: capture.UsePrivateDesktop,
	}
	creds := toElevatedRunnerCreds(context.SandboxCreds)
	_ = LogNote(capture.CodexHome, fmt.Sprintf(
		"elevated runner spawn starting: user=%s cwd=%s caps=%d",
		creds.Username,
		capture.CWD,
		len(spawnRequest.CapSIDs),
	))
	transport, err := elevated.RetryRunnerSpawnOnce(
		creds,
		capture.Command,
		func(creds elevated.SandboxCredentials) (*elevated.RunnerTransport, error) {
			_ = LogNote(capture.CodexHome, fmt.Sprintf("elevated runner transport attempt: user=%s cwd=%s", creds.Username, capture.CWD))
			transport, err := elevated.SpawnRunnerTransport(capture.CodexHome, capture.CWD, &creds, "", spawnRequest)
			if err != nil {
				_ = LogNote(capture.CodexHome, "elevated runner transport failed: "+err.Error())
				return nil, err
			}
			_ = LogNote(capture.CodexHome, "elevated runner transport ready")
			return transport, nil
		},
		func() (elevated.SandboxCredentials, error) {
			_ = LogNote(capture.CodexHome, "elevated runner credentials refresh starting")
			refreshed, err := RefreshLogonSandboxCredsForPermissions(
				permissions,
				capture.CWD,
				envMap,
				capture.CodexHome,
				capture.ReadRootsOverride,
				capture.ReadRootsOverrideSet,
				capture.ReadRootsIncludePlatformDefaults,
				capture.WriteRootsOverride,
				capture.WriteRootsOverrideSet,
				capture.DenyReadPaths,
				capture.DenyWritePaths,
				capture.ProxyEnforced,
				effectiveProxySettingsMode(capture.ProxySettingsMode),
			)
			if err != nil {
				_ = LogNote(capture.CodexHome, "elevated runner credentials refresh failed: "+err.Error())
				return elevated.SandboxCredentials{}, err
			}
			_ = LogNote(capture.CodexHome, "elevated runner credentials refresh completed")
			return toElevatedRunnerCreds(refreshed), nil
		},
	)
	if err != nil {
		_ = LogNote(capture.CodexHome, "elevated runner spawn failed: "+err.Error())
		return nil, err
	}
	defer transport.Close()
	cancelDone := startElevatedCancelWriter(transport, capture.Cancellation)
	if cancelDone != nil {
		defer close(cancelDone)
	}

	var stdout []byte
	var stderr []byte
	_ = LogNote(capture.CodexHome, "elevated runner waiting for command exit")
	for {
		msg, err := elevated.ReadFrame(transport.PipeRead)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			return nil, fmt.Errorf("runner pipe closed before exit")
		}
		switch {
		case msg.Message.SpawnReady != nil:
		case msg.Message.Output != nil:
			data, err := elevated.DecodeBytes(msg.Message.Output.DataB64)
			if err != nil {
				return nil, err
			}
			if msg.Message.Output.Stream == elevated.OutputStreamStderr {
				stderr = append(stderr, data...)
			} else {
				stdout = append(stdout, data...)
			}
		case msg.Message.Exit != nil:
			exit := msg.Message.Exit
			_ = LogNote(capture.CodexHome, fmt.Sprintf("elevated runner command exited: exit_code=%d timed_out=%t", exit.ExitCode, exit.TimedOut))
			if exit.ExitCode == 0 {
				_ = LogSuccess(capture.Command, capture.CodexHome)
			} else {
				_ = LogFailure(capture.Command, fmt.Sprintf("exit code %d", exit.ExitCode), capture.CodexHome)
			}
			return &CaptureResult{
				ExitCode: exit.ExitCode,
				Stdout:   stdout,
				Stderr:   stderr,
				TimedOut: exit.TimedOut,
			}, nil
		case msg.Message.Error != nil:
			_ = LogNote(capture.CodexHome, "elevated runner error: "+msg.Message.Error.Message)
			return nil, fmt.Errorf("runner error: %s", msg.Message.Error.Message)
		}
	}
}

func effectiveProxySettingsMode(mode ProxySettingsMode) ProxySettingsMode {
	if mode == "" {
		return ProxySettingsReconcile
	}
	return mode
}

func startElevatedCancelWriter(transport *elevated.RunnerTransport, cancellation CancellationToken) chan struct{} {
	if transport == nil || transport.PipeWrite == nil || cancellation.IsCancelled == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if cancellation.Cancelled() {
					_ = elevated.WriteFrame(transport.PipeWrite, &elevated.FramedMessage{
						Version: elevated.IPCProtocolVersion,
						Message: elevated.Message{
							Terminate: &elevated.EmptyPayload{},
						},
					})
					return
				}
			}
		}
	}()
	return done
}

func toElevatedRunnerCreds(creds *SandboxCredentials) elevated.SandboxCredentials {
	if creds == nil {
		return elevated.SandboxCredentials{}
	}
	return elevated.SandboxCredentials{
		Username: creds.Username,
		Password: creds.Password,
		Domain:   creds.Domain,
	}
}
