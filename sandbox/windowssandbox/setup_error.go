package windowssandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	json "github.com/goccy/go-json"
)

type SetupErrorCode string

const (
	SetupErrorOrchestratorSandboxDirCreateFailed  SetupErrorCode = "orchestrator_sandbox_dir_create_failed"
	SetupErrorOrchestratorElevationCheckFailed    SetupErrorCode = "orchestrator_elevation_check_failed"
	SetupErrorOrchestratorElevationRequired       SetupErrorCode = "orchestrator_elevation_required"
	SetupErrorOrchestratorPayloadSerializeFailed  SetupErrorCode = "orchestrator_payload_serialize_failed"
	SetupErrorOrchestratorHelperLaunchFailed      SetupErrorCode = "orchestrator_helper_launch_failed"
	SetupErrorOrchestratorHelperLaunchCanceled    SetupErrorCode = "orchestrator_helper_launch_canceled"
	SetupErrorOrchestratorHelperExitNonzero       SetupErrorCode = "orchestrator_helper_exit_nonzero"
	SetupErrorOrchestratorHelperReportReadFailed  SetupErrorCode = "orchestrator_helper_report_read_failed"
	SetupErrorOrchestratorHelperIncomplete        SetupErrorCode = "orchestrator_helper_incomplete"
	SetupErrorHelperRequestArgsFailed             SetupErrorCode = "helper_request_args_failed"
	SetupErrorHelperSandboxDirCreateFailed        SetupErrorCode = "helper_sandbox_dir_create_failed"
	SetupErrorHelperLogFailed                     SetupErrorCode = "helper_log_failed"
	SetupErrorHelperUserProvisionFailed           SetupErrorCode = "helper_user_provision_failed"
	SetupErrorHelperUsersGroupCreateFailed        SetupErrorCode = "helper_users_group_create_failed"
	SetupErrorHelperUserCreateOrUpdateFailed      SetupErrorCode = "helper_user_create_or_update_failed"
	SetupErrorHelperDPAPIProtectFailed            SetupErrorCode = "helper_dpapi_protect_failed"
	SetupErrorHelperUsersFileWriteFailed          SetupErrorCode = "helper_users_file_write_failed"
	SetupErrorHelperSetupMarkerWriteFailed        SetupErrorCode = "helper_setup_marker_write_failed"
	SetupErrorHelperSIDResolveFailed              SetupErrorCode = "helper_sid_resolve_failed"
	SetupErrorHelperCapabilitySIDFailed           SetupErrorCode = "helper_capability_sid_failed"
	SetupErrorHelperFirewallCOMInitFailed         SetupErrorCode = "helper_firewall_com_init_failed"
	SetupErrorHelperFirewallPolicyAccessFailed    SetupErrorCode = "helper_firewall_policy_access_failed"
	SetupErrorHelperFirewallPolicyIneffective     SetupErrorCode = "helper_firewall_policy_ineffective"
	SetupErrorHelperFirewallRuleCreateOrAddFailed SetupErrorCode = "helper_firewall_rule_create_or_add_failed"
	SetupErrorHelperFirewallRuleVerifyFailed      SetupErrorCode = "helper_firewall_rule_verify_failed"
	SetupErrorHelperReadACLHelperSpawnFailed      SetupErrorCode = "helper_read_acl_helper_spawn_failed"
	SetupErrorHelperSandboxLockFailed             SetupErrorCode = "helper_sandbox_lock_failed"
	SetupErrorHelperUnknownError                  SetupErrorCode = "helper_unknown_error"
)

type SetupErrorReport struct {
	Code    SetupErrorCode `json:"code"`
	Message string         `json:"message"`
}

type SetupFailure struct {
	Code    SetupErrorCode
	Message string
}

func NewSetupFailure(code SetupErrorCode, message string) *SetupFailure {
	return &SetupFailure{Code: code, Message: message}
}

func SetupFailureFromReport(report SetupErrorReport) *SetupFailure {
	return NewSetupFailure(report.Code, report.Message)
}

func (f *SetupFailure) Error() string {
	if f == nil {
		return ""
	}
	return string(f.Code) + ": " + f.Message
}

func (f *SetupFailure) MetricMessage() string {
	if f == nil {
		return ""
	}
	return SanitizeSetupMetricTagValue(f.Message)
}

func SetupErrorPath(codexHome string) string {
	return filepath.Join(codexHome, ".sandbox", "setup_error.json")
}

func ClearSetupErrorReport(codexHome string) error {
	err := os.Remove(SetupErrorPath(codexHome))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func WriteSetupErrorReport(codexHome string, report *SetupErrorReport) error {
	return writeSetupErrorReport(codexHome, report, func(path string, data []byte) error {
		return os.WriteFile(path, data, 0o600)
	})
}

// WriteSetupErrorReportForProvisioning writes the report through a fresh
// handle relative to the pinned sandbox directory (Rust #42342).
func WriteSetupErrorReportForProvisioning(codexHome string, report *SetupErrorReport) error {
	return writeSetupErrorReport(codexHome, report, WriteFileAtomically)
}

func writeSetupErrorReport(codexHome string, report *SetupErrorReport, write func(string, []byte) error) error {
	if report == nil {
		return ErrInvalidRequest
	}
	dir := filepath.Join(codexHome, ".sandbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return write(SetupErrorPath(codexHome), data)
}

func ReadSetupErrorReport(codexHome string) (*SetupErrorReport, error) {
	data, err := os.ReadFile(SetupErrorPath(codexHome))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var report SetupErrorReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

// SetupErrorChain renders an error together with its wrapped causes as
// "outer: cause: root", mirroring Rust's alternate anyhow formatting
// (`{error:#}`, #46237). A cause already present in the rendered parent is
// skipped, so `%w`-wrapped messages do not repeat themselves; error types that
// keep their cause out of Error() still surface it here.
func SetupErrorChain(err error) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	seen := map[string]bool{}
	for current := err; current != nil; current = errors.Unwrap(current) {
		message := strings.TrimSpace(current.Error())
		if message == "" || seen[message] {
			continue
		}
		if len(parts) > 0 && strings.Contains(parts[len(parts)-1], message) {
			continue
		}
		seen[message] = true
		parts = append(parts, message)
	}
	return strings.Join(parts, ": ")
}

func SanitizeSetupMetricTagValue(value string) string {
	return sanitizeMetricTagValue(RedactUsernameSegmentsFromSetupMessage(value, setupUsernames()))
}

func RedactUsernameSegmentsFromSetupMessage(value string, usernames []string) string {
	if len(usernames) == 0 {
		return value
	}
	segments := []string{}
	separators := []rune{}
	var current strings.Builder
	for _, ch := range value {
		if ch == '\\' || ch == '/' {
			segments = append(segments, current.String())
			current.Reset()
			separators = append(separators, ch)
			continue
		}
		current.WriteRune(ch)
	}
	segments = append(segments, current.String())
	for i, segment := range segments {
		for _, username := range usernames {
			if username == "" {
				continue
			}
			if strings.EqualFold(segment, username) {
				segments[i] = "<user>"
				break
			}
		}
	}
	var out strings.Builder
	for i, segment := range segments {
		out.WriteString(segment)
		if i < len(separators) {
			out.WriteRune(separators[i])
		}
	}
	return out.String()
}

func setupUsernames() []string {
	var out []string
	seen := map[string]bool{}
	for _, key := range []string{"USERNAME", "USER"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, value)
	}
	return out
}

func sanitizeMetricTagValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var out strings.Builder
	lastUnderscore := false
	for _, ch := range value {
		allowed := unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' || ch == '-' || ch == '.' || ch == '<' || ch == '>'
		if allowed {
			out.WriteRune(ch)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			out.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(out.String(), "_")
	if result == "" {
		return "unknown"
	}
	if len(result) > 200 {
		return result[:200]
	}
	return result
}
