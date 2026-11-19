package win

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"

	"codex_go/sandbox/windowssandbox"
)

const (
	SandboxUsersGroup        = "CodexSandboxUsers"
	SandboxUsersGroupComment = "Codex sandbox internal group (managed)"
)

var resolveSIDStringForSecrets = resolveSIDString

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

func ProvisionSandboxUsers(codexHome string, offlineUsername string, onlineUsername string, log io.Writer, forProvisioning bool) error {
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
	return WriteSandboxUserSecrets(codexHome, offlineUsername, offlinePassword, onlineUsername, onlinePassword, forProvisioning)
}

func EnsureSandboxUser(username string, password string, log io.Writer) error {
	if err := EnsureLocalUser(username, password, log); err != nil {
		return err
	}
	return EnsureLocalGroupMember(SandboxUsersGroup, username)
}

// sandboxPasswordClasses mirrors Rust's random_password (#62094). NetUserSetInfo
// enforces the machine's password policy, and a draw from the combined alphabet
// alone can still omit a class the policy requires.
var sandboxPasswordClasses = [4]string{
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"abcdefghijklmnopqrstuvwxyz",
	"0123456789",
	"!@#$%^&*()-_=+",
}

// SandboxPasswordLength is the generated password length.
const SandboxPasswordLength = 24

// RandomSandboxPassword returns a password that always contains an uppercase
// letter, a lowercase letter, a digit, and a symbol, in a shuffled order.
func RandomSandboxPassword() (string, error) {
	password := make([]byte, 0, SandboxPasswordLength)
	for _, class := range sandboxPasswordClasses {
		index, err := randomPasswordIndex(len(class))
		if err != nil {
			return "", err
		}
		password = append(password, class[index])
	}
	alphabet := sandboxPasswordClasses[0] + sandboxPasswordClasses[1] + sandboxPasswordClasses[2] + sandboxPasswordClasses[3]
	for len(password) < SandboxPasswordLength {
		index, err := randomPasswordIndex(len(alphabet))
		if err != nil {
			return "", err
		}
		password = append(password, alphabet[index])
	}
	for i := len(password) - 1; i > 0; i-- {
		index, err := randomPasswordIndex(i + 1)
		if err != nil {
			return "", err
		}
		password[i], password[index] = password[index], password[i]
	}
	return string(password), nil
}

// randomPasswordIndex draws an unbiased index with the system CSPRNG, matching
// Rust's OsRng gen_range.
func randomPasswordIndex(n int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

func WriteSandboxUserSecrets(codexHome string, offlineUser string, offlinePassword string, onlineUser string, onlinePassword string, forProvisioning bool) error {
	offlineSID, err := resolveSIDStringForSecrets(offlineUser)
	if err != nil {
		return fmt.Errorf("resolve offline sandbox account SID: %w", err)
	}
	onlineSID, err := resolveSIDStringForSecrets(onlineUser)
	if err != nil {
		return fmt.Errorf("resolve online sandbox account SID: %w", err)
	}
	offlineBlob, err := windowssandbox.DPAPIProtect([]byte(offlinePassword))
	if err != nil {
		return fmt.Errorf("dpapi protect failed for offline user: %w", err)
	}
	onlineBlob, err := windowssandbox.DPAPIProtect([]byte(onlinePassword))
	if err != nil {
		return fmt.Errorf("dpapi protect failed for online user: %w", err)
	}
	users := &windowssandbox.SandboxUsersFile{
		Version: windowssandbox.SetupVersion,
		Offline: windowssandbox.SandboxUserRecord{
			Username: offlineUser,
			Password: base64.StdEncoding.EncodeToString(offlineBlob),
			SID:      offlineSID,
		},
		Online: windowssandbox.SandboxUserRecord{
			Username: onlineUser,
			Password: base64.StdEncoding.EncodeToString(onlineBlob),
			SID:      onlineSID,
		},
	}
	if forProvisioning {
		return windowssandbox.WriteSandboxUsersFileForProvisioning(codexHome, users)
	}
	return windowssandbox.WriteSandboxUsersFile(codexHome, users)
}

func logSetupLine(log io.Writer, line string) {
	if log == nil {
		return
	}
	_, _ = fmt.Fprintln(log, line)
}
