//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	toml "github.com/pelletier/go-toml/v2"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// policyRejectionContext is the text every rejected request carries (Rust
// ipc::handle_request's context on the policy failure).
const policyRejectionContext = "requested sandbox settings violate administrator-controlled machine policy"

// logPolicyRejection records a rejected request; tests replace it to observe the
// message (Rust service::log_error(EVENT_REQUEST_REJECTED, ...)).
var logPolicyRejection = func(message string) {
	LogError(EventRequestRejected, message)
}

// policyViolationResponse maps a failed policy check to the answer the client
// receives. A configuration that does not parse is also an interactive-helper
// problem, so the client may fall back (Rust ipc::handle_request answers
// Unavailable for a TOML failure); every other rejection is recorded as a
// sanitized event and answered with a bounded Error.
func policyViolationResponse(err error) provisionservice.Response {
	if isConfigParseError(err) {
		return provisionservice.Response{Kind: provisionservice.ResponseUnavailable}
	}
	logPolicyRejection(policyRejectionDiagnostic(err))
	return failureResponse(fmt.Errorf("%s: %w", policyRejectionContext, err))
}

// policyRejectionDiagnostic names the failing stage and a typed cause for the
// event log. Only service-owned labels and typed codes belong here: parser
// wrappers can stringify whole config lines, including credentials (Rust
// ipc::policy_rejection_diagnostic).
func policyRejectionDiagnostic(err error) string {
	return fmt.Sprintf("Codex sandbox provisioning was rejected by administrator policy: stage=%s %s",
		policyRejectionStage(err), policyRejectionDetail(err))
}

// policyRejectionStage reports the label the service attached to the failure.
// Go's wrapped messages append their cause, so a label also matches as a prefix;
// Rust compares the whole message because anyhow keeps the context alone.
func policyRejectionStage(err error) string {
	if err == nil {
		return "unknown"
	}
	message := err.Error()
	for _, stage := range [...]struct{ label, name string }{
		{"start managed configuration runtime", "runtime"},
		{"load bootstrap configuration", "bootstrap"},
		{"resolve cloud configuration authentication", "resolve_auth"},
		{"initialize cloud configuration authentication", "initialize_auth"},
		{"load managed configuration", "managed"},
		{"enforce managed provisioning requirements", "enforce"},
	} {
		if message == stage.label || strings.HasPrefix(message, stage.label+": ") {
			return stage.name
		}
	}
	return "unknown"
}

// policyRejectionDetail reports the first typed cause in the chain: a cloud
// bundle failure keeps its code and HTTP status, an OS failure keeps its error
// kind and Win32 code, and a configuration failure is the InvalidData io error
// Rust wraps it in. Anything else has no safe detail to report.
func policyRejectionDetail(err error) string {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		switch typed := cause.(type) {
		case *config.CloudConfigLoadError:
			return "cloud=" + cloudConfigCodeName(typed.Code) + " http=" + optionalInt(typed.StatusCode)
		case *config.ConfigLoadError:
			return "io=InvalidData win32=None"
		case syscall.Errno:
			if typed == 0 {
				continue
			}
			return fmt.Sprintf("io=%s win32=Some(%d)", ioErrorKindName(typed), uintptr(typed))
		}
	}
	return "cause=unavailable"
}

// isConfigParseError reports whether the failure came from the TOML parser, in
// which case the interactive setup helper can still repair the configuration
// (Rust ipc::is_config_parse_error).
func isConfigParseError(err error) bool {
	var decodeErr *toml.DecodeError
	if errors.As(err, &decodeErr) {
		return true
	}
	var strictErr *toml.StrictMissingError
	return errors.As(err, &strictErr)
}

// cloudConfigCodeName names a bundle load failure the way Rust's Debug does, so
// one event reader understands both builds.
func cloudConfigCodeName(code config.CloudConfigLoadErrorCode) string {
	switch code {
	case config.CloudConfigLoadAuth:
		return "Auth"
	case config.CloudConfigLoadTimeout:
		return "Timeout"
	case config.CloudConfigLoadRequestFailed:
		return "RequestFailed"
	case config.CloudConfigLoadInvalidBundle:
		return "InvalidBundle"
	case config.CloudConfigLoadInternal:
		return "Internal"
	}
	return string(code)
}

func optionalInt(value *int) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%d)", *value)
}

// ioErrorKindName names a Win32 failure the way Rust's io::Error::kind does
// (std's decode_error_kind), because an event reader parses the same wording
// from both builds.
func ioErrorKindName(errno syscall.Errno) string {
	switch uint32(errno) {
	case 2, 3, 15: // ERROR_FILE_NOT_FOUND, ERROR_PATH_NOT_FOUND, ERROR_INVALID_DRIVE
		return "NotFound"
	case 5, 32, 33: // ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
		return "PermissionDenied"
	case 8, 14: // ERROR_NOT_ENOUGH_MEMORY, ERROR_OUTOFMEMORY
		return "OutOfMemory"
	case 17: // ERROR_NOT_SAME_DEVICE
		return "CrossesDevices"
	case 23, 1113: // ERROR_CRC, ERROR_NO_UNICODE_TRANSLATION
		return "InvalidData"
	case 80, 183: // ERROR_FILE_EXISTS, ERROR_ALREADY_EXISTS
		return "AlreadyExists"
	case 87, 2202: // ERROR_INVALID_PARAMETER, ERROR_CANT_ACCESS_FILE
		return "InvalidInput"
	case 109, 232: // ERROR_BROKEN_PIPE, ERROR_NO_DATA
		return "BrokenPipe"
	case 123, 161: // ERROR_INVALID_NAME, ERROR_BAD_PATHNAME
		return "InvalidFilename"
	case 995: // ERROR_OPERATION_ABORTED
		return "Interrupted"
	}
	return "Uncategorized"
}
