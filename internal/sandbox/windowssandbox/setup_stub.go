//go:build !windows

package windowssandbox

func runElevatedSetup(req *SandboxSetupRequest) error {
	return unsupported("setup.run_elevated_setup")
}

func runElevatedProvisioningSetup(req *SandboxSetupRequest) error {
	return unsupported("setup.run_elevated_provisioning_setup")
}

func runSetupRefresh(codexHome string) error {
	return unsupported("setup.run_setup_refresh")
}

func runSetupRefreshForRequest(req *SandboxSetupRequest) error {
	return unsupported("setup.run_setup_refresh")
}
