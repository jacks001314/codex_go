//go:build !windows

package elevated

func connectRunner(pipeName string) (*RunnerClient, error) {
	return nil, unsupported("elevated.runner_client.connect")
}

func closeRunnerClient(c *RunnerClient) error {
	return nil
}

func spawnRunnerTransport(codexHome string, cwd string, creds *SandboxCredentials, currentExe string, request SpawnRequest) (*RunnerTransport, error) {
	return nil, unsupported("elevated.runner_client.spawn_runner_transport")
}
