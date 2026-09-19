//go:build !windows

package conpty

import "codex_go/sandbox/windowssandbox"

func Create(columns int16, rows int16) (*Instance, error) {
	return nil, windowssandbox.Unsupported("conpty.create")
}

func SpawnProcessAsUser(command []string, cwd string) (*Instance, error) {
	return nil, windowssandbox.Unsupported("conpty.spawn_process_as_user")
}

func SpawnProcessAsUserWithToken(req SpawnRequest) (*windowssandbox.CreatedProcess, *Instance, error) {
	return nil, nil, windowssandbox.Unsupported("conpty.spawn_process_as_user")
}

func (i *Instance) Resize(columns uint16, rows uint16) error {
	return windowssandbox.Unsupported("conpty.resize")
}

func (i *Instance) CloseInputWrite() error {
	return nil
}

// ReleasePseudoConsoleAvailable mirrors the Windows probe: the API only exists
// on Windows 11 24H2 and newer.
func ReleasePseudoConsoleAvailable() bool {
	return false
}

// FinishSpawn mirrors the Windows post-spawn step.
func (i *Instance) FinishSpawn() {}

func (i *Instance) Close() error {
	return nil
}
