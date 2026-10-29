package windowssandbox

import (
	"errors"
	"fmt"
	"strings"

	"codex_go/sandbox/windowssandbox/internal/setupenv"
)

// Windows package-name query results (winerror.h / appmodel.h).
const (
	errorSuccess            uint32 = 0
	errorInsufficientBuffer uint32 = 122
	appModelErrorNoPackage  uint32 = 15700
	// packageNameMaxLength bounds the allocation a package-name query may ask
	// for (Rust `process_package_name`'s max_length).
	packageNameMaxLength uint32 = 32768
)

// registeredCoreEnv selects the registered-core runtime for Windows sandbox
// wrappers (Rust `CODEX_WINDOWS_REGISTERED_CORE`).
const registeredCoreEnv = setupenv.RegisteredCoreEnv

// SetupRuntime selects legacy helper materialization or verified app-contained
// Core for setup (Rust `SetupRuntime`).
type SetupRuntime = setupenv.Runtime

const (
	SetupRuntimeLegacy     = setupenv.Legacy
	SetupRuntimeRegistered = setupenv.Registered
)

// PackageNameQuery mirrors Rust's `query_package_name` query closure: it
// receives the length pointer and an optional buffer and returns the Win32
// status.
type PackageNameQuery func(length *uint32, buffer *uint16) uint32

// QueryPackageName ports Rust's `query_package_name`: package absence
// (APPMODEL_ERROR_NO_PACKAGE) is distinct from failure, the allocation is
// bounded by maxLength, and the returned name must be terminated and valid
// UTF-16.
func QueryPackageName(query PackageNameQuery, maxLength uint32) (string, bool, error) {
	if query == nil {
		return "", false, errors.New("package name query is required")
	}
	var length uint32
	status := query(&length, nil)
	if status == appModelErrorNoPackage {
		return "", false, nil
	}
	if status != errorInsufficientBuffer || length <= 1 || length > maxLength {
		return "", false, fmt.Errorf("package name query failed: %d", status)
	}
	buffer := make([]uint16, length)
	status = query(&length, &buffer[0])
	if status != errorSuccess || length <= 1 || int(length) > len(buffer) {
		return "", false, fmt.Errorf("package name read failed: %d", status)
	}
	units := buffer[:length]
	if units[len(units)-1] != 0 {
		return "", false, errors.New("unterminated package name")
	}
	units = units[:len(units)-1]
	for _, unit := range units {
		if unit == 0 {
			return "", false, errors.New("invalid package name")
		}
	}
	name, err := decodePackageName(units)
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// decodePackageName rejects unpaired UTF-16 surrogates, matching Rust's
// `String::from_utf16`.
func decodePackageName(units []uint16) (string, error) {
	runes := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		unit := units[i]
		switch {
		case unit >= 0xD800 && unit <= 0xDBFF:
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				return "", errors.New("invalid package name")
			}
			runes = append(runes, 0x10000+(rune(unit-0xD800)<<10)+rune(units[i+1]-0xDC00))
			i++
		case unit >= 0xDC00 && unit <= 0xDFFF:
			return "", errors.New("invalid package name")
		default:
			runes = append(runes, rune(unit))
		}
	}
	return string(runes), nil
}

// RequestedValue ports Rust's `requested_value`: only the exact value `1`
// requests registered-core execution.
func RequestedValue(value string, present bool) bool {
	return setupenv.RequestedValue(value, present)
}

// RegisteredCoreRequested captures the startup request once (Rust's
// `registered_core_requested` OnceLock). The flag never authorizes a package or
// process by itself; it only decides whether a missing package identity is an
// error.
func RegisteredCoreRequested() bool {
	return setupenv.Requested()
}

// CurrentSetupRuntime returns the runtime selected when this process started
// (Rust `current_setup_runtime`).
func CurrentSetupRuntime() SetupRuntime {
	return setupenv.Current()
}

// ApplyWrapperRuntimeEnv propagates the parent's runtime selection to an
// external sandbox wrapper process (Rust
// `add_windows_sandbox_wrapper_setup_env`). Any inherited or shell-supplied
// spelling is removed first so a child cannot override the parent's choice.
func ApplyWrapperRuntimeEnv(env map[string]string) {
	ApplyWrapperRuntimeEnvForSelection(env, RegisteredCoreRequested())
}

// ApplyWrapperRuntimeEnvForSelection is the injectable form used by tests and
// by callers that already captured the startup selection.
func ApplyWrapperRuntimeEnvForSelection(env map[string]string, registeredCore bool) {
	if env == nil {
		return
	}
	for key := range env {
		if strings.EqualFold(key, registeredCoreEnv) {
			delete(env, key)
		}
	}
	if registeredCore {
		env[registeredCoreEnv] = "1"
	}
}
