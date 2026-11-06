package windowssandbox

import "runtime"

// Hooks keep PrepareElevatedSandbox testable without elevation. Production
// values are the exported entry points used by the app-server runner.
var (
	prepareSandboxSetupIsComplete = SandboxSetupIsComplete
	prepareRunElevatedSetup       = RunElevatedSetup
	prepareRunSetupRefresh        = RunSetupRefreshForRequest
)

// PrepareElevatedSandbox mirrors Rust core/src/windows_sandbox.rs
// prepare_elevated_sandbox (upstream #42309): elevated provisioning only runs
// while setup is incomplete, and the non-elevated ACL refresh always runs so
// the current permission profile supplies the read, write and deny paths. A
// completed provisioning therefore still refreshes ACLs whenever the sandbox
// is (re)activated from the TUI or the app server.
func PrepareElevatedSandbox(req *SandboxSetupRequest) error {
	if req == nil {
		return ErrInvalidRequest
	}
	if runtime.GOOS != "windows" {
		// Rust's non-Windows stub is a no-op; the sandbox cannot be prepared.
		return nil
	}
	complete, err := prepareSandboxSetupIsComplete(req.CodexHome)
	if err != nil {
		return err
	}
	if !complete {
		if err := prepareRunElevatedSetup(req); err != nil {
			return err
		}
	}
	return prepareRunSetupRefresh(req)
}
