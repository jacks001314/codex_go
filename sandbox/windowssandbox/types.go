package windowssandbox

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	coresandbox "codex_go/sandbox"
)

var (
	ErrWindowsOnly              = errors.New("windows sandbox is only available on Windows")
	ErrBackendNotImplemented    = errors.New("windows sandbox backend is unavailable")
	ErrHostUnsupported          = errors.New("windows sandbox host does not support the required feature")
	ErrInvalidRequest           = errors.New("invalid windows sandbox request")
	ErrSetupElevationDisallowed = errors.New("windows sandbox setup elevation is disabled for this command")
)

func Unsupported(feature string) error {
	return unsupported(feature)
}

func IsUnsupported(err error) bool {
	return errors.Is(err, ErrWindowsOnly) ||
		errors.Is(err, ErrBackendNotImplemented) ||
		errors.Is(err, ErrHostUnsupported)
}

type CancellationToken struct {
	IsCancelled func() bool
}

func (t CancellationToken) Cancelled() bool {
	return t.IsCancelled != nil && t.IsCancelled()
}

type ProxySettingsMode string

const (
	ProxySettingsReconcile ProxySettingsMode = "reconcile"
	ProxySettingsPreserve  ProxySettingsMode = "preserve"
)

type CaptureRequest struct {
	PermissionProfileID              string
	PermissionProfile                *coresandbox.PermissionProfile
	WorkspaceRoots                   []string
	CodexHome                        string
	Command                          []string
	CWD                              string
	Env                              map[string]string
	TimeoutMS                        *int64
	Cancellation                     CancellationToken
	UsePrivateDesktop                bool
	TTY                              bool
	StdinOpen                        bool
	ProxyEnforced                    bool
	ProxySettingsMode                ProxySettingsMode
	ReadRootsOverride                []string
	ReadRootsOverrideSet             bool
	ReadRootsIncludePlatformDefaults bool
	WriteRootsOverride               []string
	WriteRootsOverrideSet            bool
	DenyReadPaths                    []string
	DenyWritePaths                   []string
	DisallowSetupElevation           bool
}

func (r *CaptureRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: capture request is nil", ErrInvalidRequest)
	}
	if len(r.Command) == 0 || strings.TrimSpace(r.Command[0]) == "" {
		return fmt.Errorf("%w: command is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.CWD) == "" {
		return fmt.Errorf("%w: cwd is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.CodexHome) == "" {
		return fmt.Errorf("%w: codex home is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.PermissionProfileID) == "" && r.PermissionProfile == nil {
		return fmt.Errorf("%w: permission profile is required", ErrInvalidRequest)
	}
	return nil
}

type CaptureResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	TimedOut bool
}

func unsupported(feature string) error {
	feature = strings.TrimSpace(feature)
	if runtime.GOOS != "windows" {
		if feature == "" {
			return ErrWindowsOnly
		}
		return fmt.Errorf("%w: %s", ErrWindowsOnly, feature)
	}
	if feature == "" {
		return ErrBackendNotImplemented
	}
	return fmt.Errorf("%w: %s", ErrBackendNotImplemented, feature)
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneEnv(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
