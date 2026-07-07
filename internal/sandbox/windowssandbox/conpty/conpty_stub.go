//go:build !windows

package conpty

import "codex_go/internal/sandbox/windowssandbox"

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

func (i *Instance) Close() error {
	return nil
}
