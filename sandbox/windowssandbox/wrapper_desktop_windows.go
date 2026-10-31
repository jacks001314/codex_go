//go:build windows

package windowssandbox

import "fmt"

// PrepareWindowsSandboxPrivateDesktopName selects the caller-owned private
// desktop for a sandboxed launch. Rust #44658 keeps that cache in the calling
// process because the wrapper exits with every helper request; the selected
// name is passed to the wrapper instead of letting it create a desktop that
// disappears immediately.
func PrepareWindowsSandboxPrivateDesktopName(req WindowsSandboxCommandArgsRequest) (string, error) {
	if req.WindowsSandboxLevel == WindowsSandboxLevelDisabled {
		return "", nil
	}
	capture := wrapperCaptureRequest(&WindowsSandboxWrapperRequest{WindowsSandboxCommandArgsRequest: req})
	if capture.PermissionProfile == nil {
		profile, err := ResolveCapturePermissionProfile(capture)
		if err != nil {
			return "", err
		}
		capture.PermissionProfile = profile
	}
	if req.WindowsSandboxLevel == WindowsSandboxLevelElevated {
		return elevatedWrapperPrivateDesktopName(capture)
	}
	return legacyWrapperPrivateDesktopName(capture)
}

func legacyWrapperPrivateDesktopName(capture *CaptureRequest) (string, error) {
	context, err := PrepareLegacySpawnContext(capture, SpawnPrepOptions{})
	if err != nil {
		return "", err
	}
	capabilityRoots := LegacySessionCapabilityRoots(context.Permissions, context.CurrentDir, context.Env, capture.CodexHome)
	security, err := PrepareLegacySessionSecurity(context.UsesWriteCapabilities, capture.CodexHome, capture.CWD, capabilityRoots)
	if err != nil {
		return "", err
	}
	defer CloseTokenHandle(security.Token)
	return SharedLegacyPrivateDesktopName(security, context.Permissions, context.CurrentDir, context.Env, capture.DenyWritePaths, context.LogsBaseDir)
}

// elevatedWrapperPrivateDesktopName mirrors Rust #44658: choose the desktop
// from the sandbox account and its capability SIDs without running the
// wrapper's ACL refresh (require_sandbox_account, not the setup refresh).
func elevatedWrapperPrivateDesktopName(capture *CaptureRequest) (string, error) {
	context, err := PrepareSpawnContextCommon(capture, SpawnPrepOptions{InheritPath: true, AddGitSafeDirectory: true})
	if err != nil {
		return "", err
	}
	creds, err := RequireLogonSandboxCredsForPermissions(
		context.Permissions,
		context.CurrentDir,
		context.Env,
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
		return "", err
	}
	caps, err := LoadOrCreateCapabilitySIDs(capture.CodexHome)
	if err != nil {
		return "", err
	}
	var capSIDs []string
	if context.UsesWriteCapabilities {
		writeRoots := EffectiveWriteRootsForSetup(context.Permissions, context.CurrentDir, context.Env, capture.CodexHome, capture.WriteRootsOverride, capture.WriteRootsOverrideSet)
		rootSIDs, err := RootCapabilitySIDs(capture.CodexHome, capture.CWD, writeRoots)
		if err != nil {
			return "", err
		}
		capSIDs = rootCapabilitySIDStrings(rootSIDs)
		if len(capSIDs) == 0 {
			return "", fmt.Errorf("workspace-write sandbox has no writable root capability SIDs")
		}
	} else {
		capSIDs = []string{caps.Readonly}
	}
	policy, err := elevatedDesktopPolicyForCapture(capture, context.Permissions, context.Env, capSIDs)
	if err != nil {
		return "", err
	}
	return SharedPrivateDesktopForUser(creds.Username, policy)
}
