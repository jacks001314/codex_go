//go:build windows

package windowsandboxservice

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// removalScript is the native cleanup helper Rust embeds with
// include_str!("removal.ps1").
//
//go:embed assets/removal.ps1
var removalScript string

const (
	// removalReadinessDeadline bounds the child's profile preparation.
	removalReadinessDeadline = 30 * time.Second
	// removalReadinessPoll is the readiness polling interval.
	removalReadinessPoll = 20 * time.Millisecond
	// removalReadinessLimit bounds the readiness line the parent accepts.
	removalReadinessLimit = 16
	// removalCommandLineLimit is the Windows command-line bound.
	removalCommandLineLimit = 32767
)

var (
	modRuntimeKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemDirectory      = modRuntimeKernel32.NewProc("GetSystemDirectoryW")
	procSetNamedPipeHandleState = modRuntimeKernel32.NewProc("SetNamedPipeHandleState")
)

// removalTarget is one managed account the native cleanup removes.
type removalTarget struct {
	Username string  `json:"username"`
	SID      string  `json:"sid"`
	Handle   uintptr `json:"handle,omitempty"`
}

// PreparedRemoval owns prepared managed logons and waits for both COMMIT and
// service-process EOF (Rust registered_runtime::removal::PreparedRemoval).
type PreparedRemoval struct {
	process *os.Process
	stdin   *os.File
	stdout  *os.File
	tokens  []windows.Token
}

// Tokens exposes the prepared logons so cleanup can report them.
func (prepared *PreparedRemoval) Tokens() []windows.Token {
	if prepared == nil {
		return nil
	}
	return append([]windows.Token(nil), prepared.tokens...)
}

// Commit authorizes native cleanup once the guard has successfully finished.
// The only pipe writer stays open until this own-process service exits.
func (prepared *PreparedRemoval) Commit() error {
	if prepared == nil || prepared.stdin == nil {
		return errors.New("cleanup input closed")
	}
	if _, err := prepared.stdin.WriteString("COMMIT\n"); err != nil {
		return fmt.Errorf("commit Windows package cleanup: %w", err)
	}
	// Dropping an uncommitted preparation only sends EOF, which aborts.
	prepared.process = nil
	prepared.stdin = nil
	return nil
}

// Abort releases an uncommitted preparation so the child sees EOF and stops.
func (prepared *PreparedRemoval) Abort() {
	if prepared == nil {
		return
	}
	if prepared.stdin != nil {
		_ = prepared.stdin.Close()
		prepared.stdin = nil
	}
	if prepared.stdout != nil {
		_ = prepared.stdout.Close()
		prepared.stdout = nil
	}
	if prepared.process != nil {
		_ = prepared.process.Release()
		prepared.process = nil
	}
}

// RestoreDisabledRuntimeAccounts restores durable temporary-enable intent before
// owner restoration or IPC admission (Rust restore_disabled_accounts).
func RestoreDisabledRuntimeAccounts() error {
	lock, err := windowssandbox.AcquireSandboxSetupLock(pipeSetupLockTimeoutMillis)
	if err != nil {
		return fmt.Errorf("acquire the sandbox setup lock: %w", err)
	}
	defer lock.Release()
	record, err := runtimeLoadInstallation()
	if err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	family, hasFamily, err := runtimeProcessPackageFamily()
	if err != nil {
		return err
	}
	if !hasFamily || !strings.EqualFold(runtimeRecordFamily(record), family) {
		return nil
	}
	if err := ValidateRuntimeRecord(record); err != nil {
		return err
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	for index := range runtime.Accounts {
		account := runtime.Accounts[index]
		if !account.CleanupLogonPending {
			continue
		}
		if err := validateRuntimeAccountSID(account); err != nil {
			return err
		}
		flags, found, err := runtimeLocalUserFlags(account.Account.Username())
		if err != nil {
			return err
		}
		if !found {
			return errors.New("cleanup account disappeared before flag restoration")
		}
		if err := runtimeSetLocalUserFlags(account.Account.Username(), flags|userFlagAccountDisable); err != nil {
			return err
		}
		runtime.Accounts[index].CleanupLogonPending = false
		if err := runtimeSaveInstallation(record); err != nil {
			return err
		}
	}
	return nil
}

// PrepareRuntimeRemoval prepares exact managed logons before native cleanup and
// authorizes removal only after the guard succeeds (Rust
// registered_runtime::removal::prepare).
func PrepareRuntimeRemoval(ownerToken windows.Token, record *windowssandbox.InstallationRecord) (*PreparedRemoval, error) {
	if record == nil {
		return nil, errors.New("missing installation record")
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return nil, err
	}
	if runtime.Retiring != nil {
		return nil, errors.New("runtime is already retiring")
	}
	prepared := &PreparedRemoval{}
	success := false
	defer func() {
		if !success {
			prepared.Abort()
		}
	}()
	targets := make([]removalTarget, 0, len(runtime.Accounts))
	planTokens := make([]windows.Token, 0, len(runtime.Accounts))
	for index := range runtime.Accounts {
		account := runtime.Accounts[index]
		username := account.Account.Username()
		flags, found, err := runtimeLocalUserFlags(username)
		if err != nil {
			return nil, err
		}
		if !found {
			packages, err := runtimeRegisteredPackages(account.UserSID, runtime.PackageFamily)
			if err != nil {
				return nil, err
			}
			if len(packages) != 0 {
				return nil, errors.New("runtime account is missing while its package remains registered")
			}
			continue
		}
		// Persist the obligation before changing SAM, so process death cannot
		// lose it.
		wasDisabled := flags&userFlagAccountDisable != 0
		if wasDisabled {
			runtime.Accounts[index].CleanupLogonPending = true
			// Older bundled clients also fail closed while account recovery is
			// pending.
			runtime.ReadyPackage = nil
			if err := runtimeSaveInstallation(record); err != nil {
				return nil, err
			}
			if err := runtimeSetLocalUserFlags(username, flags&^userFlagAccountDisable); err != nil {
				return nil, err
			}
		}
		token, logonErr := logonRegisteredRuntimeAccountForRecord(record, ownerToken, account.Account)
		if wasDisabled {
			if err := validateRuntimeAccountSID(account); err != nil {
				return nil, err
			}
			current, found, err := runtimeLocalUserFlags(username)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, errors.New("cleanup account disappeared before flag restoration")
			}
			if err := runtimeSetLocalUserFlags(username, current|userFlagAccountDisable); err != nil {
				return nil, fmt.Errorf("restore disabled sandbox account after cleanup logon: %w", err)
			}
			runtime.Accounts[index].CleanupLogonPending = false
			if err := runtimeSaveInstallation(record); err != nil {
				return nil, err
			}
		}
		if logonErr != nil {
			return nil, logonErr
		}
		if _, err := ValidateRuntimeTarget(token, account.Account, account.UserSID); err != nil {
			_ = token.Close()
			return nil, err
		}
		planTokens = append(planTokens, token)
		targets = append(targets, removalTarget{Username: username, SID: account.UserSID})
	}
	prepared.tokens = planTokens
	// Flag recovery is durable before the in-memory retirement generation exists.
	runtime.ReadyPackage = nil
	generation := uniqueRuntimeGeneration()
	runtime.Retiring = &generation
	groupSID := ""
	if len(planTokens) != 0 {
		group, err := runtimeResolveSIDString(runtimeSandboxUsersGroup)
		if err != nil {
			return nil, err
		}
		groupSID = group
	}
	executable, err := windowsPowerShellPath()
	if err != nil {
		return nil, err
	}
	serviceName, err := provisionservice.ServiceName()
	if err != nil {
		return nil, err
	}
	command := &exec.Cmd{
		Path: executable,
		Args: []string{
			executable,
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			removalScript,
		},
		Dir:    filepath.Dir(executable),
		Stderr: nil,
		SysProcAttr: &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_NO_WINDOW,
			HideWindow:    true,
		},
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start Windows package cleanup: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start Windows package cleanup: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Windows package cleanup: %w", err)
	}
	prepared.process = command.Process
	prepared.stdin = stdin.(*os.File)
	prepared.stdout = stdout.(*os.File)
	plan := struct {
		Key       string          `json:"key"`
		LegacyKey string          `json:"legacy_key"`
		Value     string          `json:"value"`
		Record    any             `json:"record"`
		Targets   []removalTarget `json:"targets"`
		Service   string          `json:"service_name"`
		GroupSID  string          `json:"group_sid"`
	}{
		Key:       windowssandbox.CoreInstallationKey,
		LegacyKey: windowssandbox.InstallationKey,
		Value:     windowssandbox.InstallationValue,
		Record:    record,
		Targets:   targets,
		Service:   serviceName,
		GroupSID:  groupSID,
	}
	if err := duplicateRemovalTokens(command.Process, planTokens, plan.Targets); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("serialize Windows package cleanup plan: %w", err)
	}
	if _, err := prepared.stdin.Write(append(encoded, '\n')); err != nil {
		return nil, fmt.Errorf("write Windows package cleanup plan: %w", err)
	}
	if err := awaitRemovalReadiness(prepared.stdout); err != nil {
		return nil, err
	}
	success = true
	return prepared, nil
}
