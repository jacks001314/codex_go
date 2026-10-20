package integrity

import (
	"codex_go/sandbox"
	mxcsandbox "codex_go/sandbox/mxc"
)

func preparePlatformIntegrityPolicy(request *RunnerRequest) error {
	if request == nil || request.SandboxType != sandbox.SandboxTypeWindowsMxc {
		return nil
	}
	_, err := mxcsandbox.PrepareEntriesPolicy(request.Entries, request.CWD, request.Env, request.CWD)
	return err
}
