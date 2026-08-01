package sandbox

import (
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"codex_go/network"
)

const outputSnippetMaxChars = 512

// SandboxType identifies the platform sandbox that actually ran a process.
type SandboxType string

const (
	SandboxTypeNone                   SandboxType = "none"
	SandboxTypeMacosSeatbelt          SandboxType = "macosSeatbelt"
	SandboxTypeLinuxSeccomp           SandboxType = "linuxSeccomp"
	SandboxTypeWindowsRestrictedToken SandboxType = "windowsRestrictedToken"
)

// SandboxViolationEvent is a normalized violation observed by sandbox enforcement.
type SandboxViolationEvent interface {
	sandboxViolationEvent()
}

type SandboxViolationBackend string

const (
	SandboxViolationBackendLinuxSandbox        SandboxViolationBackend = "linux_sandbox"
	SandboxViolationBackendManagedNetworkProxy SandboxViolationBackend = "managed_network_proxy"
	SandboxViolationBackendSeatbelt            SandboxViolationBackend = "seatbelt"
	SandboxViolationBackendWindowsSandbox      SandboxViolationBackend = "windows_sandbox"
)

type FileSystemSandboxViolation struct {
	Backend       SandboxViolationBackend
	Reason        FileSystemSandboxViolationReason
	Path          *string
	OutputSnippet string
}

func (FileSystemSandboxViolation) sandboxViolationEvent() {}

type FileSystemSandboxViolationReason string

const (
	FileSystemSandboxViolationOperationNotPermitted FileSystemSandboxViolationReason = "operation_not_permitted"
	FileSystemSandboxViolationPermissionDenied      FileSystemSandboxViolationReason = "permission_denied"
	FileSystemSandboxViolationReadOnlyFileSystem    FileSystemSandboxViolationReason = "read_only_file_system"
	FileSystemSandboxViolationPolicyDenied          FileSystemSandboxViolationReason = "policy_denied"
	FileSystemSandboxViolationFailedToWriteFile     FileSystemSandboxViolationReason = "failed_to_write_file"
	FileSystemSandboxViolationSignalSyscall         FileSystemSandboxViolationReason = "sigsys"
)

type NetworkSandboxViolation struct {
	Backend   SandboxViolationBackend
	Host      string
	Reason    string
	Client    *string
	Method    *string
	Mode      *network.ProxyMode
	Protocol  string
	Decision  *string
	Source    *network.ProxyDecisionSource
	Port      *uint16
	Timestamp int64
}

func (NetworkSandboxViolation) sandboxViolationEvent() {}

type SandboxExecOutput struct {
	ExitCode         int
	Stdout           string
	Stderr           string
	AggregatedOutput string
}

var sandboxDeniedKeywords = []struct {
	reason FileSystemSandboxViolationReason
	text   string
}{
	{FileSystemSandboxViolationOperationNotPermitted, "operation not permitted"},
	{FileSystemSandboxViolationPermissionDenied, "permission denied"},
	{FileSystemSandboxViolationReadOnlyFileSystem, "read-only file system"},
	{FileSystemSandboxViolationPolicyDenied, "seccomp"},
	{FileSystemSandboxViolationPolicyDenied, "sandbox"},
	{FileSystemSandboxViolationPolicyDenied, "landlock"},
	{FileSystemSandboxViolationFailedToWriteFile, "failed to write file"},
}

// ClassifyFileSystemSandboxViolation mirrors Rust's conservative classifier.
func ClassifyFileSystemSandboxViolation(sandboxType SandboxType, output SandboxExecOutput) *FileSystemSandboxViolation {
	if output.ExitCode == 0 {
		return nil
	}
	backend, ok := sandboxViolationBackendForType(sandboxType)
	if !ok {
		return nil
	}
	if reason, section, matched := filesystemReasonFromOutput(output); matched {
		return &FileSystemSandboxViolation{
			Backend:       backend,
			Reason:        reason,
			Path:          extractDeniedPathFromText(section),
			OutputSnippet: outputSnippet(section),
		}
	}
	if output.ExitCode == 2 || output.ExitCode == 126 || output.ExitCode == 127 {
		return nil
	}
	if sigsysExitCode, supported := sandboxSIGSYSExitCode(); supported && sandboxType == SandboxTypeLinuxSeccomp && output.ExitCode == sigsysExitCode {
		return &FileSystemSandboxViolation{
			Backend:       backend,
			Reason:        FileSystemSandboxViolationSignalSyscall,
			OutputSnippet: firstNonEmptyOutputSnippet(output),
		}
	}
	return nil
}

func IsLikelySandboxDenied(sandboxType SandboxType, output SandboxExecOutput) bool {
	return ClassifyFileSystemSandboxViolation(sandboxType, output) != nil
}

func RecordFileSystemSandboxViolation(sandboxType SandboxType, output SandboxExecOutput) *FileSystemSandboxViolation {
	violation := ClassifyFileSystemSandboxViolation(sandboxType, output)
	if violation == nil {
		return nil
	}
	RecordSandboxViolation(*violation)
	return violation
}

func PlatformSandboxType() SandboxType {
	switch runtime.GOOS {
	case "darwin":
		return SandboxTypeMacosSeatbelt
	case "linux":
		return SandboxTypeLinuxSeccomp
	case "windows":
		return SandboxTypeWindowsRestrictedToken
	default:
		return SandboxTypeNone
	}
}

// RecordFileSystemPolicyViolation records denials enforced directly by a Go
// policy check rather than inferred from child-process output.
func RecordFileSystemPolicyViolation(sandboxType SandboxType, path string, output string) *FileSystemSandboxViolation {
	backend, ok := sandboxViolationBackendForType(sandboxType)
	if !ok {
		return nil
	}
	path = strings.TrimSpace(path)
	var deniedPath *string
	if path != "" {
		deniedPath = &path
	}
	violation := FileSystemSandboxViolation{
		Backend:       backend,
		Reason:        FileSystemSandboxViolationPolicyDenied,
		Path:          deniedPath,
		OutputSnippet: outputSnippet(output),
	}
	RecordSandboxViolation(violation)
	return &violation
}

func NetworkSandboxViolationFromBlockedRequest(blocked network.ProxyBlockedRequest) NetworkSandboxViolation {
	return NetworkSandboxViolation{
		Backend:   SandboxViolationBackendManagedNetworkProxy,
		Host:      blocked.Host,
		Reason:    blocked.Reason,
		Client:    optionalString(blocked.Client),
		Method:    optionalString(blocked.Method),
		Mode:      blocked.Mode,
		Protocol:  blocked.Protocol,
		Decision:  optionalString(blocked.Decision),
		Source:    optionalDecisionSource(blocked.Source),
		Port:      blocked.Port,
		Timestamp: blocked.Timestamp,
	}
}

func RecordNetworkSandboxViolation(blocked network.ProxyBlockedRequest) NetworkSandboxViolation {
	violation := NetworkSandboxViolationFromBlockedRequest(blocked)
	RecordSandboxViolation(violation)
	return violation
}

func RecordSandboxViolation(event SandboxViolationEvent) {
	switch violation := event.(type) {
	case FileSystemSandboxViolation:
		path := "unknown"
		if violation.Path != nil {
			path = *violation.Path
		}
		slog.Warn(fmt.Sprintf(
			"recorded sandbox violation: resource=filesystem backend=%s reason=%s path=%s",
			violation.Backend, violation.Reason, path,
		))
	case *FileSystemSandboxViolation:
		if violation != nil {
			RecordSandboxViolation(*violation)
		}
	case NetworkSandboxViolation:
		slog.Warn(fmt.Sprintf(
			"recorded sandbox violation: resource=network backend=%s protocol=%s host=%s port=%s reason=%s method=%s mode=%s client=%s decision=%s source=%s",
			violation.Backend,
			violation.Protocol,
			violation.Host,
			optionalValue(violation.Port),
			violation.Reason,
			optionalQuotedString(violation.Method),
			optionalNetworkMode(violation.Mode),
			optionalQuotedString(violation.Client),
			optionalQuotedString(violation.Decision),
			optionalDecisionSourceValue(violation.Source),
		))
	case *NetworkSandboxViolation:
		if violation != nil {
			RecordSandboxViolation(*violation)
		}
	}
}

func sandboxViolationBackendForType(sandboxType SandboxType) (SandboxViolationBackend, bool) {
	switch sandboxType {
	case SandboxTypeMacosSeatbelt:
		return SandboxViolationBackendSeatbelt, true
	case SandboxTypeLinuxSeccomp:
		return SandboxViolationBackendLinuxSandbox, true
	case SandboxTypeWindowsRestrictedToken:
		return SandboxViolationBackendWindowsSandbox, true
	default:
		return "", false
	}
}

func filesystemReasonFromOutput(output SandboxExecOutput) (FileSystemSandboxViolationReason, string, bool) {
	for _, section := range []string{output.Stderr, output.Stdout, output.AggregatedOutput} {
		lower := strings.ToLower(section)
		for _, keyword := range sandboxDeniedKeywords {
			if strings.Contains(lower, keyword.text) {
				return keyword.reason, section, true
			}
		}
	}
	return "", "", false
}

func extractDeniedPathFromText(text string) *string {
	markers := []string{": operation not permitted", ": permission denied", ": read-only file system"}
	for _, line := range strings.Split(text, "\n") {
		for index := 0; index < len(line); index++ {
			if line[index] != ':' {
				continue
			}
			for _, marker := range markers {
				if len(line)-index < len(marker) || !strings.EqualFold(line[index:index+len(marker)], marker) {
					continue
				}
				candidate := line[:index]
				if separator := strings.LastIndex(candidate, ": "); separator >= 0 {
					candidate = candidate[separator+2:]
				}
				candidate = strings.TrimSpace(candidate)
				candidate = strings.Trim(candidate, "\"")
				candidate = strings.Trim(candidate, "'")
				if strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, "../") {
					return &candidate
				}
			}
		}
	}
	return nil
}

func outputSnippet(output string) string {
	runes := []rune(strings.TrimSpace(output))
	if len(runes) > outputSnippetMaxChars {
		runes = runes[:outputSnippetMaxChars]
	}
	return string(runes)
}

func firstNonEmptyOutputSnippet(output SandboxExecOutput) string {
	for _, section := range []string{output.Stderr, output.Stdout, output.AggregatedOutput} {
		if strings.TrimSpace(section) != "" {
			return outputSnippet(section)
		}
	}
	return ""
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalDecisionSource(value network.ProxyDecisionSource) *network.ProxyDecisionSource {
	if value == "" {
		return nil
	}
	return &value
}

func optionalValue[T any](value *T) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%v)", *value)
}

func optionalQuotedString(value *string) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%q)", *value)
}

func optionalDecisionSourceValue(value *network.ProxyDecisionSource) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%q)", string(*value))
}

func optionalNetworkMode(value *network.ProxyMode) string {
	if value == nil {
		return "None"
	}
	name := string(*value)
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return "Some(" + name + ")"
}

func sandboxSIGSYSExitCode() (int, bool) {
	switch runtime.GOOS {
	case "linux":
		return 128 + 31, true
	case "darwin":
		return 128 + 12, true
	default:
		return 0, false
	}
}
