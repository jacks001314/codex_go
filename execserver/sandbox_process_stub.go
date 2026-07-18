//go:build !windows

package execserver

func startExecServerSandboxProcess(params *ExecParams) (*startedExecServerSandboxProcess, bool, error) {
	_ = params
	return nil, false, nil
}
