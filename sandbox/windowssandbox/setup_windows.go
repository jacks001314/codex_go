//go:build windows

package windowssandbox

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	json "github.com/goccy/go-json"
	"golang.org/x/sys/windows"
)

const (
	windowsSandboxSetupFlag = "--codex-run-as-windows-sandbox-setup"
	setupModeFull           = "full"
	setupModeProvisionOnly  = "provision-only"
	createNoWindow          = 0x08000000

	errorCancelled        windows.Errno = 1223
	seeMaskNoCloseProcess               = 0x00000040
	// seeMaskNoAsync mirrors Rust #39971: sandbox setup runs on a thread without
	// a Windows message loop, so ShellExecuteExW requires synchronous activation.
	seeMaskNoAsync        = 0x00000001
	shellExecuteShowHide                = 0
)

var (
	modShell32          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = modShell32.NewProc("ShellExecuteExW")
)

type setupElevationPayload struct {
	Version           int      `json:"version"`
	OfflineUsername   string   `json:"offline_username"`
	OnlineUsername    string   `json:"online_username"`
	CodexHome         string   `json:"codex_home"`
	CommandCWD        string   `json:"command_cwd"`
	ReadRoots         []string `json:"read_roots"`
	WriteRoots        []string `json:"write_roots"`
	DenyReadPaths     []string `json:"deny_read_paths,omitempty"`
	DenyWritePaths    []string `json:"deny_write_paths,omitempty"`
	ProxyPorts        []uint16 `json:"proxy_ports"`
	AllowLocalBinding bool     `json:"allow_local_binding,omitempty"`
	Otel              any      `json:"otel,omitempty"`
	RealUser          string   `json:"real_user"`
	Mode              string   `json:"mode"`
	RefreshOnly       bool     `json:"refresh_only,omitempty"`
}

func setupExecutableArgs(payloadB64 string, useDispatchFlag bool) []string {
	if useDispatchFlag {
		return []string{windowsSandboxSetupFlag, payloadB64}
	}
	return []string{payloadB64}
}

func quoteWindowsArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, QuoteWindowsArg(arg))
	}
	return quoted
}

type shellExecuteInfoW struct {
	CbSize       uint32
	FMask        uint32
	Hwnd         uintptr
	LpVerb       *uint16
	LpFile       *uint16
	LpParameters *uint16
	LpDirectory  *uint16
	NShow        int32
	HInstApp     windows.Handle
	LpIDList     uintptr
	LpClass      *uint16
	HkeyClass    windows.Handle
	DwHotKey     uint32
	HIcon        windows.Handle
	HProcess     windows.Handle
}

func runElevatedSetup(req *SandboxSetupRequest) error {
	if req.Permissions == nil || !req.Permissions.IsEnforceableByWindowsSandbox() {
		return fmt.Errorf("unsupported filesystem permissions for Windows sandbox setup")
	}
	sbxDir := SandboxDir(req.CodexHome)
	if err := os.MkdirAll(sbxDir, 0o700); err != nil {
		return setupFailuref(
			SetupErrorOrchestratorSandboxDirCreateFailed,
			"failed to create sandbox dir %s: %v", sbxDir, err,
		)
	}
	readRoots, writeRoots := BuildPayloadRoots(req, req.Overrides)
	networkIdentity := SandboxNetworkIdentityFromPermissions(req.Permissions, req.ProxyEnforced)
	offlineProxySettings := offlineProxySettingsForRequest(req, networkIdentity)
	payload := &setupElevationPayload{
		Version:           SetupVersion,
		OfflineUsername:   OfflineUsername,
		OnlineUsername:    OnlineUsername,
		CodexHome:         req.CodexHome,
		CommandCWD:        req.CommandCWD,
		ReadRoots:         readRoots,
		WriteRoots:        writeRoots,
		DenyReadPaths:     BuildPayloadDenyReadPaths(req.Overrides.DenyReadPaths),
		DenyWritePaths:    BuildPayloadDenyWritePaths(req, req.Overrides.DenyWritePaths),
		ProxyPorts:        offlineProxySettings.ProxyPorts,
		AllowLocalBinding: offlineProxySettings.AllowLocalBinding,
		RealUser:          setupRealUser(req.RealUser),
		Mode:              setupModeFull,
	}
	elevated, err := isElevated()
	if err != nil {
		return setupFailuref(SetupErrorOrchestratorElevationCheckFailed, "failed to determine elevation state: %v", err)
	}
	return runSetupExe(payload, !elevated, req.CodexHome, true)
}

func runElevatedProvisioningSetup(req *SandboxSetupRequest) error {
	if req.CodexHome == "" {
		return ErrInvalidRequest
	}
	sbxDir := SandboxDir(req.CodexHome)
	if err := os.MkdirAll(sbxDir, 0o700); err != nil {
		return setupFailuref(
			SetupErrorOrchestratorSandboxDirCreateFailed,
			"failed to create sandbox dir %s: %v", sbxDir, err,
		)
	}
	elevated, err := isElevated()
	if err != nil {
		return setupFailuref(SetupErrorOrchestratorElevationCheckFailed, "failed to determine elevation state: %v", err)
	}
	if !elevated {
		return setupFailuref(
			SetupErrorOrchestratorElevationRequired,
			"sandbox provisioning setup must be run from an elevated process",
		)
	}
	payload := &setupElevationPayload{
		Version:         SetupVersion,
		OfflineUsername: OfflineUsername,
		OnlineUsername:  OnlineUsername,
		CodexHome:       req.CodexHome,
		CommandCWD:      req.CodexHome,
		ReadRoots:       []string{},
		WriteRoots:      []string{},
		ProxyPorts:      []uint16{},
		RealUser:        setupRealUser(req.RealUser),
		Mode:            setupModeProvisionOnly,
	}
	return runSetupExe(payload, false, req.CodexHome, true)
}

func runSetupRefresh(codexHome string) error {
	if codexHome == "" {
		return ErrInvalidRequest
	}
	marker, err := ReadSetupMarker(codexHome)
	if err != nil {
		return err
	}
	proxySettings := OfflineProxySettings{}
	if marker != nil {
		proxySettings = marker.OfflineProxySettings()
	}
	payload := &setupElevationPayload{
		Version:           SetupVersion,
		OfflineUsername:   OfflineUsername,
		OnlineUsername:    OnlineUsername,
		CodexHome:         codexHome,
		CommandCWD:        codexHome,
		ReadRoots:         []string{},
		WriteRoots:        []string{},
		DenyReadPaths:     []string{},
		DenyWritePaths:    []string{},
		ProxyPorts:        proxySettings.ProxyPorts,
		AllowLocalBinding: proxySettings.AllowLocalBinding,
		RealUser:          setupRealUser(""),
		Mode:              setupModeFull,
		RefreshOnly:       true,
	}
	return runSetupExe(payload, false, codexHome, true)
}

func runSetupRefreshForRequest(req *SandboxSetupRequest) error {
	if req.CodexHome == "" || req.Permissions == nil || !req.Permissions.IsEnforceableByWindowsSandbox() {
		return ErrInvalidRequest
	}
	sbxDir := SandboxDir(req.CodexHome)
	if err := os.MkdirAll(sbxDir, 0o700); err != nil {
		return setupFailuref(
			SetupErrorOrchestratorSandboxDirCreateFailed,
			"failed to create sandbox dir %s: %v", sbxDir, err,
		)
	}
	readRoots, writeRoots := BuildPayloadRoots(req, req.Overrides)
	networkIdentity := SandboxNetworkIdentityFromPermissions(req.Permissions, req.ProxyEnforced)
	offlineProxySettings := offlineProxySettingsForRequest(req, networkIdentity)
	payload := &setupElevationPayload{
		Version:           SetupVersion,
		OfflineUsername:   OfflineUsername,
		OnlineUsername:    OnlineUsername,
		CodexHome:         req.CodexHome,
		CommandCWD:        req.CommandCWD,
		ReadRoots:         readRoots,
		WriteRoots:        writeRoots,
		DenyReadPaths:     BuildPayloadDenyReadPaths(req.Overrides.DenyReadPaths),
		DenyWritePaths:    BuildPayloadDenyWritePaths(req, req.Overrides.DenyWritePaths),
		ProxyPorts:        offlineProxySettings.ProxyPorts,
		AllowLocalBinding: offlineProxySettings.AllowLocalBinding,
		RealUser:          setupRealUser(req.RealUser),
		Mode:              setupModeFull,
		RefreshOnly:       true,
	}
	return runSetupExe(payload, false, req.CodexHome, true)
}

func runSetupExe(payload *setupElevationPayload, needsElevation bool, codexHome string, verifyComplete bool) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return setupFailuref(SetupErrorOrchestratorPayloadSerializeFailed, "failed to serialize elevation payload: %v", err)
	}
	payloadB64 := base64.StdEncoding.EncodeToString(payloadJSON)
	clearedReport := true
	if err := ClearSetupErrorReport(codexHome); err != nil {
		clearedReport = false
		_ = LogNoteInDir(SandboxDir(codexHome), fmt.Sprintf("setup orchestrator: failed to clear setup_error.json before launch: %v", err))
	}
	exe, err := os.Executable()
	if err != nil {
		return setupFailuref(SetupErrorOrchestratorHelperLaunchFailed, "failed to locate setup helper: %v", err)
	}
	useDispatchFlag := true
	if bundledSetupExe := BundledExecutablePathForExe(exe, HelperWindowsSandboxSetup.FileName()); bundledSetupExe != "" {
		exe = bundledSetupExe
		useDispatchFlag = false
	}
	if !needsElevation {
		status, err := runSetupExeNonElevated(exe, payloadB64, useDispatchFlag)
		if err != nil {
			return setupFailuref(SetupErrorOrchestratorHelperLaunchFailed, "failed to launch setup helper (non-elevated): %v", err)
		}
		if !status.Success() {
			return reportHelperFailure(codexHome, clearedReport, status.ExitCode())
		}
		if verifyComplete {
			if err := verifySetupCompleted(codexHome); err != nil {
				return err
			}
		}
		if err := ClearSetupErrorReport(codexHome); err != nil {
			_ = LogNoteInDir(SandboxDir(codexHome), fmt.Sprintf("setup orchestrator: failed to clear setup_error.json after success: %v", err))
		}
		return nil
	}
	exitCode, err := runSetupExeElevated(exe, payloadB64, useDispatchFlag)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return reportHelperFailure(codexHome, clearedReport, exitCode)
	}
	if verifyComplete {
		if err := verifySetupCompleted(codexHome); err != nil {
			return err
		}
	}
	if err := ClearSetupErrorReport(codexHome); err != nil {
		_ = LogNoteInDir(SandboxDir(codexHome), fmt.Sprintf("setup orchestrator: failed to clear setup_error.json after success: %v", err))
	}
	return nil
}

func runSetupExeNonElevated(exe string, payloadB64 string, useDispatchFlag bool) (*os.ProcessState, error) {
	cmd := exec.Command(exe, setupExecutableArgs(payloadB64, useDispatchFlag)...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return nilIfNoState(cmd)
}

func nilIfNoState(cmd *exec.Cmd) (*os.ProcessState, error) {
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState, nil
	}
	return nil, err
}

func runSetupExeElevated(exe string, payloadB64 string, useDispatchFlag bool) (int, error) {
	exeW, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 1, err
	}
	params := strings.Join(quoteWindowsArgs(setupExecutableArgs(payloadB64, useDispatchFlag)), " ")
	paramsW, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return 1, err
	}
	verbW, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 1, err
	}
	sei := shellExecuteInfoW{
		CbSize:       uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		FMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		LpVerb:       verbW,
		LpFile:       exeW,
		LpParameters: paramsW,
		NShow:        shellExecuteShowHide,
	}
	r1, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if r1 == 0 || sei.HProcess == 0 {
		code := SetupErrorOrchestratorHelperLaunchFailed
		if errno, ok := callErr.(windows.Errno); ok && errno == errorCancelled {
			code = SetupErrorOrchestratorHelperLaunchCanceled
		}
		return 1, setupFailuref(code, "ShellExecuteExW failed to launch setup helper: %v", callErr)
	}
	defer windows.CloseHandle(sei.HProcess)
	_, _ = windows.WaitForSingleObject(sei.HProcess, windows.INFINITE)
	var exitCode uint32 = 1
	if err := windows.GetExitCodeProcess(sei.HProcess, &exitCode); err != nil {
		return 1, setupFailuref(SetupErrorOrchestratorHelperExitNonzero, "GetExitCodeProcess failed for setup helper: %v", err)
	}
	return int(exitCode), nil
}

func reportHelperFailure(codexHome string, clearedReport bool, exitCode int) error {
	exitDetail := fmt.Sprintf("setup helper exited with status %d", exitCode)
	if !clearedReport {
		return setupFailuref(SetupErrorOrchestratorHelperExitNonzero, "%s", exitDetail)
	}
	report, err := ReadSetupErrorReport(codexHome)
	if err != nil {
		return setupFailuref(
			SetupErrorOrchestratorHelperReportReadFailed,
			"%s; failed to read setup_error.json: %v",
			exitDetail,
			err,
		)
	}
	if report != nil {
		return SetupFailureFromReport(*report)
	}
	return setupFailuref(SetupErrorOrchestratorHelperExitNonzero, "%s", exitDetail)
}

func verifySetupCompleted(codexHome string) error {
	complete, err := SandboxSetupIsComplete(codexHome)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}
	return setupFailuref(
		SetupErrorOrchestratorHelperIncomplete,
		"setup helper exited successfully before setup completed",
	)
}

func isElevated() (bool, error) {
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, err
	}
	return windows.Token(0).IsMember(adminSID)
}

func setupRealUser(value string) string {
	if value != "" {
		return value
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "Administrators"
}

func offlineProxySettingsForRequest(req *SandboxSetupRequest, networkIdentity SandboxNetworkIdentity) OfflineProxySettings {
	if req != nil && req.OfflineProxySettings != nil {
		return OfflineProxySettings{
			ProxyPorts:        append([]uint16(nil), req.OfflineProxySettings.ProxyPorts...),
			AllowLocalBinding: req.OfflineProxySettings.AllowLocalBinding,
		}
	}
	if req == nil {
		return OfflineProxySettings{}
	}
	return OfflineProxySettingsFromEnv(req.Env, networkIdentity)
}

func setupFailuref(code SetupErrorCode, format string, args ...interface{}) *SetupFailure {
	return NewSetupFailure(code, fmt.Sprintf(format, args...))
}
