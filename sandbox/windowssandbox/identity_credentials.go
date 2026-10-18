package windowssandbox

import (
	"errors"
	"fmt"
	"strings"
)

// SandboxAccountCredentialMismatch reports that Windows rejected a stored
// sandbox account password. Missing credential files and policy failures remain
// distinct so they never trigger password repair (Rust #47695).
type SandboxAccountCredentialMismatch struct {
	Underlying error
}

func (e *SandboxAccountCredentialMismatch) Error() string {
	return "Windows rejected the stored sandbox account password"
}

func (e *SandboxAccountCredentialMismatch) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Underlying
}

var (
	logonSandboxAccountForCredentials         = logonSandboxAccount
	resolveSandboxAccountSIDForCredentials    = resolveSandboxAccountSID
	storedCredentialsNeedRepairForCredentials = storedCredentialsNeedRepair
)

func isSandboxCredentialMismatch(err error) bool {
	var mismatch *SandboxAccountCredentialMismatch
	return errors.As(err, &mismatch)
}

func sandboxAccountLogonFailure(code uint32, underlying error) error {
	switch code {
	case 1326, 1330, 1907: // logon failure, password expired, password must change
		return &SandboxAccountCredentialMismatch{Underlying: underlying}
	default:
		return fmt.Errorf("log on existing sandbox account: %w", underlying)
	}
}

// credentialsNeedRepair mirrors Rust's provisioning gate: a credential
// mismatch on either account requires a full setup only after the recorded
// account identities still prove ownership. Every non-credential error is
// returned unchanged and never rotates passwords.
func credentialsNeedRepair(accounts []SandboxCredentials, logon func(SandboxCredentials) error, validateOwnership func() error) (bool, error) {
	needsRepair := false
	for _, account := range accounts {
		err := logon(account)
		switch {
		case err == nil:
		case isSandboxCredentialMismatch(err):
			needsRepair = true
		default:
			return false, err
		}
	}
	if needsRepair {
		if err := validateOwnership(); err != nil {
			return false, err
		}
	}
	return needsRepair, nil
}

func storedCredentialsNeedRepair(codexHome string) (bool, error) {
	users, err := ReadSandboxUsersFile(codexHome)
	if err != nil || users == nil || !users.VersionMatches() {
		return false, err
	}
	records := []SandboxUserRecord{users.Offline, users.Online}
	credentials := make([]SandboxCredentials, 0, len(records))
	for _, record := range records {
		password, err := DecodeSandboxUserPassword(record)
		if err != nil {
			return false, err
		}
		credentials = append(credentials, SandboxCredentials{
			Username: record.Username,
			Password: password,
			Domain:   ".",
		})
	}
	return credentialsNeedRepair(
		credentials,
		logonSandboxAccountForCredentials,
		func() error {
			return validateSandboxAccountOwnership(records, resolveSandboxAccountSIDForCredentials)
		},
	)
}

func validateSandboxAccountOwnership(records []SandboxUserRecord, resolve func(string) (string, error)) error {
	for _, record := range records {
		recordSID := strings.TrimSpace(record.SID)
		if recordSID == "" {
			return fmt.Errorf("sandbox account %s has no recorded SID; rerun setup", record.Username)
		}
		currentSID, err := resolve(record.Username)
		if err != nil {
			return fmt.Errorf("resolve sandbox account %s: %w", record.Username, err)
		}
		if currentSID != recordSID {
			return fmt.Errorf("managed sandbox account %s was replaced", record.Username)
		}
	}
	return nil
}
