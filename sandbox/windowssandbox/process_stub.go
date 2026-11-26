//go:build !windows

package windowssandbox

import "io"

func CreateProcessAsUserWithToken(req ProcessSpawnRequest) (*CreatedProcess, error) {
	return nil, unsupported("process.create_process_as_user")
}

func SpawnProcessWithPipesWithToken(req PipeSpawnRequest) (*PipeSpawnHandles, error) {
	return nil, unsupported("process.spawn_process_with_pipes")
}

func WritePipeHandle(handle uintptr, data io.Reader) error {
	return unsupported("process.write_pipe_handle")
}

func ReadHandleLoop(handle uintptr, onChunk func([]byte)) (<-chan struct{}, error) {
	return nil, unsupported("process.read_handle_loop")
}

func WaitCreatedProcess(process *CreatedProcess, timeoutMS *int64, cancellation CancellationToken) (ProcessWaitOutcome, error) {
	return "", unsupported("process.wait_created_process")
}

func TerminateCreatedProcess(process *CreatedProcess, exitCode uint32) error {
	return unsupported("process.terminate_created_process")
}

func CreatedProcessExitCode(process *CreatedProcess) (int, error) {
	return 0, unsupported("process.exit_code")
}

func (p *CreatedProcess) Close() error {
	return nil
}

func (h *PipeSpawnHandles) Close() error {
	return nil
}
