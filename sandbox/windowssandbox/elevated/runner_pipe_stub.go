//go:build !windows

package elevated

func CurrentUsername() (string, error) {
	return "", unsupported("elevated.runner_pipe.current_username")
}

func CreateNamedPipe(name string, access uint32, sandboxUsername string) (*RunnerPipe, error) {
	return nil, unsupported("elevated.runner_pipe.create_named_pipe")
}

func ConnectPipe(pipe *RunnerPipe, expectedRunnerPID uint32) error {
	return unsupported("elevated.runner_pipe.connect_pipe")
}

func ConnectPipeHandle(handle uintptr, expectedRunnerPID uint32) error {
	return unsupported("elevated.runner_pipe.connect_pipe")
}

func (p *RunnerPipe) Close() error {
	return nil
}
