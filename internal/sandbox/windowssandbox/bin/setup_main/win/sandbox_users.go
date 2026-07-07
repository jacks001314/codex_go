package win

import (
	"encoding/base64"
	"fmt"
	"io"

	"codex_go/internal/sandbox/windowssandbox"
	"github.com/sethvargo/go-password/password"
)

const (
	SandboxUsersGroup        = "CodexSandboxUsers"
	SandboxUsersGroupComment = "Codex sandbox internal group (managed)"
)

type SandboxUser struct {
	Name string
	SID  string
}

func EnsureSandboxUsers() ([]SandboxUser, error) {
	if err := EnsureSandboxUsersGroup(io.Discard); err != nil {
		return nil, err
	}
	sid, err := ResolveSandboxUsersGroupSID()
	if err != nil {
		return nil, err
	}
	return []SandboxUser{{Name: SandboxUsersGroup, SID: windowssandbox.StringFromSIDBytes(sid)}}, nil
}

func EnsureSandboxUsersGroup(log io.Writer) error {
	return EnsureLocalGroup(SandboxUsersGroup, SandboxUsersGroupComment, log)
}

func ProvisionSandboxUsers(codexHome string, offlineUsername string, onlineUsername string, log io.Writer) error {
	if err := EnsureSandboxUsersGroup(log); err != nil {
		return err
	}
	offlinePassword, err := RandomSandboxPassword()
	if err != nil {
		return err
	}
	onlinePassword, err := RandomSandboxPassword()
	if err != nil {
		return err
	}
	if err := EnsureSandboxUser(offlineUsername, offlinePassword, log); err != nil {
		return err
	}
	if err := EnsureSandboxUser(onlineUsername, onlinePassword, log); err != nil {
		return err
	}
	return WriteSandboxUserSecrets(codexHome, offlineUsername, offlinePassword, onlineUsername, onlinePassword)
}

func EnsureSandboxUser(username string, password string, log io.Writer) error {
	if err := EnsureLocalUser(username, password, log); err != nil {
		return err
	}
	return EnsureLocalGroupMember(SandboxUsersGroup, username)
}

func RandomSandboxPassword() (string, error) {
	return password.Generate(24, 6, 6, false, false)
}

func WriteSandboxUserSecrets(codexHome string, offlineUser string, offlinePassword string, onlineUser string, onlinePassword string) error {
	offlineBlob, err := windowssandbox.DPAPIProtect([]byte(offlinePassword))
	if err != nil {
		return fmt.Errorf("dpapi protect failed for offline user: %w", err)
	}
	onlineBlob, err := windowssandbox.DPAPIProtect([]byte(onlinePassword))
	if err != nil {
		return fmt.Errorf("dpapi protect failed for online user: %w", err)
	}
	return windowssandbox.WriteSandboxUsersFile(codexHome, &windowssandbox.SandboxUsersFile{
		Version: windowssandbox.SetupVersion,
		Offline: windowssandbox.SandboxUserRecord{
			Username: offlineUser,
			Password: base64.StdEncoding.EncodeToString(offlineBlob),
		},
		Online: windowssandbox.SandboxUserRecord{
			Username: onlineUser,
			Password: base64.StdEncoding.EncodeToString(onlineBlob),
		},
	})
}

func logSetupLine(log io.Writer, line string) {
	if log == nil {
		return
	}
	_, _ = fmt.Fprintln(log, line)
}
