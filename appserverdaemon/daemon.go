package appserverdaemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/install"
	"codex_go/remotecontrol"
)

const (
	PIDFileName           = "app-server.pid"
	UpdatePIDFileName     = "app-server-updater.pid"
	OperationLockFileName = "daemon.lock"
	SettingsFileName      = "settings.json"
	StateDirName          = "app-server-daemon"
	ControlDirName        = "app-server-control"
	ControlSocketFileName = "app-server-control.sock"
)

var ErrUnsupportedPlatform = errors.New("codex app-server daemon lifecycle is only supported on Unix platforms")
var ErrDaemonPathsRequired = errors.New("app-server daemon paths are required")

type BackendKind string

const BackendPID BackendKind = "pid"

type LifecycleCommand string

const (
	LifecycleStart   LifecycleCommand = "start"
	LifecycleRestart LifecycleCommand = "restart"
	LifecycleStop    LifecycleCommand = "stop"
	LifecycleVersion LifecycleCommand = "version"
)

func ParseLifecycleCommand(value string) (LifecycleCommand, error) {
	switch strings.TrimSpace(value) {
	case "start":
		return LifecycleStart, nil
	case "restart":
		return LifecycleRestart, nil
	case "stop":
		return LifecycleStop, nil
	case "version":
		return LifecycleVersion, nil
	default:
		return "", fmt.Errorf("unknown app-server daemon command %q", value)
	}
}

type LifecycleStatus string

const (
	StatusAlreadyRunning LifecycleStatus = "alreadyRunning"
	StatusStarted        LifecycleStatus = "started"
	StatusRestarted      LifecycleStatus = "restarted"
	StatusStopped        LifecycleStatus = "stopped"
	StatusNotRunning     LifecycleStatus = "notRunning"
	StatusRunning        LifecycleStatus = "running"
)

type LifecycleOutput struct {
	Status              LifecycleStatus `json:"status"`
	Backend             *BackendKind    `json:"backend,omitempty"`
	PID                 *uint32         `json:"pid,omitempty"`
	ManagedCodexPath    string          `json:"managedCodexPath"`
	ManagedCodexVersion *string         `json:"managedCodexVersion,omitempty"`
	SocketPath          string          `json:"socketPath"`
	CLIVersion          *string         `json:"cliVersion,omitempty"`
	AppServerVersion    *string         `json:"appServerVersion,omitempty"`
}

type BootstrapStatus string

const BootstrapBootstrapped BootstrapStatus = "bootstrapped"

type BootstrapOptions struct {
	RemoteControlEnabled bool `json:"remoteControlEnabled"`
}

type BootstrapOutput struct {
	Status               BootstrapStatus `json:"status"`
	Backend              BackendKind     `json:"backend"`
	AutoUpdateEnabled    bool            `json:"autoUpdateEnabled"`
	RemoteControlEnabled bool            `json:"remoteControlEnabled"`
	ManagedCodexPath     string          `json:"managedCodexPath"`
	ManagedCodexVersion  *string         `json:"managedCodexVersion,omitempty"`
	SocketPath           string          `json:"socketPath"`
	CLIVersion           string          `json:"cliVersion"`
	AppServerVersion     string          `json:"appServerVersion"`
}

type RemoteControlMode string

const (
	RemoteControlEnabled  RemoteControlMode = "enabled"
	RemoteControlDisabled RemoteControlMode = "disabled"
)

func (m RemoteControlMode) Enabled() bool {
	return m == RemoteControlEnabled
}

type RemoteControlStatus string

const (
	RemoteStatusEnabled         RemoteControlStatus = "enabled"
	RemoteStatusDisabled        RemoteControlStatus = "disabled"
	RemoteStatusAlreadyEnabled  RemoteControlStatus = "alreadyEnabled"
	RemoteStatusAlreadyDisabled RemoteControlStatus = "alreadyDisabled"
)

type RemoteControlOutput struct {
	Status               RemoteControlStatus `json:"status"`
	Backend              *BackendKind        `json:"backend,omitempty"`
	RemoteControlEnabled bool                `json:"remoteControlEnabled"`
	SocketPath           string              `json:"socketPath"`
	CLIVersion           string              `json:"cliVersion"`
	AppServerVersion     *string             `json:"appServerVersion,omitempty"`
}

type RemoteControlReadyStatus struct {
	Status        remotecontrol.ConnectionStatus `json:"status"`
	ServerName    string                         `json:"serverName"`
	EnvironmentID *string                        `json:"environmentId,omitempty"`
	TimedOut      bool                           `json:"timedOut"`
}

type RemoteControlReadyOutput struct {
	Daemon        *RemoteControlStartOutput `json:"daemon"`
	RemoteControl RemoteControlReadyStatus  `json:"remoteControl"`
}

type RemoteControlStartOutput struct {
	Bootstrap *BootstrapOutput
	Start     *LifecycleOutput
}

func (o *RemoteControlStartOutput) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	if o.Bootstrap != nil {
		return json.Marshal(o.Bootstrap)
	}
	return json.Marshal(o.Start)
}

type RestartIfRunningOutcome string

const (
	RestartBusy           RestartIfRunningOutcome = "busy"
	RestartNotRunning     RestartIfRunningOutcome = "notRunning"
	RestartNotReady       RestartIfRunningOutcome = "notReady"
	RestartAlreadyCurrent RestartIfRunningOutcome = "alreadyCurrent"
	RestartRestarted      RestartIfRunningOutcome = "restarted"
)

type RestartMode string

const (
	RestartIfVersionChanged RestartMode = "ifVersionChanged"
	RestartAlways           RestartMode = "always"
)

type UpdaterRefreshMode string

const (
	UpdaterRefreshNone                         UpdaterRefreshMode = "none"
	UpdaterRefreshReexecIfManagedBinaryChanged UpdaterRefreshMode = "reexecIfManagedBinaryChanged"
)

type RestartDecision string

const (
	DecisionNotReady       RestartDecision = "notReady"
	DecisionAlreadyCurrent RestartDecision = "alreadyCurrent"
	DecisionRestart        RestartDecision = "restart"
)

type DaemonSettings struct {
	RemoteControlEnabled bool `json:"remoteControlEnabled"`
}

type Paths struct {
	CodexHome         string
	SocketPath        string
	PIDFile           string
	UpdatePIDFile     string
	OperationLockFile string
	SettingsFile      string
	ManagedCodexBin   string
}

type Daemon struct {
	Paths      *Paths
	CLIVersion string
}

func EnsureSupportedPlatform() error {
	if runtime.GOOS == "windows" {
		return ErrUnsupportedPlatform
	}
	return nil
}

func PathsForCodexHome(codexHome string) *Paths {
	stateDir := filepath.Join(codexHome, StateDirName)
	return &Paths{
		CodexHome:         codexHome,
		SocketPath:        AppServerControlSocketPath(codexHome),
		PIDFile:           filepath.Join(stateDir, PIDFileName),
		UpdatePIDFile:     filepath.Join(stateDir, UpdatePIDFileName),
		OperationLockFile: filepath.Join(stateDir, OperationLockFileName),
		SettingsFile:      filepath.Join(stateDir, SettingsFileName),
		ManagedCodexBin:   install.ManagedCodexBin(codexHome),
	}
}

func AppServerControlSocketPath(codexHome string) string {
	return filepath.Join(codexHome, ControlDirName, ControlSocketFileName)
}

func LoadSettings(path string) (*DaemonSettings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &DaemonSettings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read daemon settings %s: %w", path, err)
	}
	var settings DaemonSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse daemon settings %s: %w", path, err)
	}
	return &settings, nil
}

func SaveSettings(path string, settings *DaemonSettings) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: settings file is empty", ErrDaemonPathsRequired)
	}
	if settings == nil {
		settings = &DaemonSettings{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create daemon settings directory %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write daemon settings %s: %w", path, err)
	}
	return nil
}

func ParseManagedCodexVersion(output string) (string, error) {
	return install.ParseCodexVersion(output)
}

func ExecutableIdentityFromBytes(data []byte) install.ExecutableIdentity {
	return install.ExecutableIdentityFromBytes(data)
}

func UpdateModesForIdentities(running *install.ExecutableIdentity, managed *install.ExecutableIdentity) (RestartMode, UpdaterRefreshMode) {
	if running != nil && managed != nil && *running == *managed {
		return RestartIfVersionChanged, UpdaterRefreshNone
	}
	return RestartAlways, UpdaterRefreshReexecIfManagedBinaryChanged
}

func RestartDecisionFor(mode RestartMode, appServerVersion *string, managedVersion *string) RestartDecision {
	if mode == RestartAlways {
		return DecisionRestart
	}
	if appServerVersion == nil {
		return DecisionNotReady
	}
	if managedVersion != nil && strings.TrimSpace(*appServerVersion) == strings.TrimSpace(*managedVersion) {
		return DecisionAlreadyCurrent
	}
	return DecisionRestart
}

func RemoteControlStatusForMode(mode RemoteControlMode) RemoteControlStatus {
	if mode.Enabled() {
		return RemoteStatusEnabled
	}
	return RemoteStatusDisabled
}

func ParseRemoteControlMode(value string) (RemoteControlMode, error) {
	switch strings.TrimSpace(value) {
	case "enable", "enabled":
		return RemoteControlEnabled, nil
	case "disable", "disabled":
		return RemoteControlDisabled, nil
	default:
		return "", fmt.Errorf("unknown remote-control mode %q", value)
	}
}

func AlreadyRemoteControlStatusForMode(mode RemoteControlMode) RemoteControlStatus {
	if mode.Enabled() {
		return RemoteStatusAlreadyEnabled
	}
	return RemoteStatusAlreadyDisabled
}

func ReadyStatusFromEnable(response *remotecontrol.EnableResponse) RemoteControlReadyStatus {
	if response == nil {
		return RemoteControlReadyStatus{}
	}
	return RemoteControlReadyStatus{
		Status:        response.Status,
		ServerName:    response.ServerName,
		EnvironmentID: cloneString(response.EnvironmentID),
	}
}

func ReadyStatusFromDisable(response *remotecontrol.DisableResponse) RemoteControlReadyStatus {
	if response == nil {
		return RemoteControlReadyStatus{}
	}
	return RemoteControlReadyStatus{
		Status:        response.Status,
		ServerName:    response.ServerName,
		EnvironmentID: cloneString(response.EnvironmentID),
	}
}

func ReadyStatusFromNotification(notification *remotecontrol.StatusChangedNotification) RemoteControlReadyStatus {
	if notification == nil {
		return RemoteControlReadyStatus{}
	}
	return RemoteControlReadyStatus{
		Status:        notification.Status,
		ServerName:    notification.ServerName,
		EnvironmentID: cloneString(notification.EnvironmentID),
	}
}

func BuildLifecycleOutput(paths *Paths, status LifecycleStatus, backend *BackendKind, pid *uint32, cliVersion *string, appServerVersion *string, managedVersion *string) *LifecycleOutput {
	if paths == nil {
		paths = &Paths{}
	}
	return &LifecycleOutput{
		Status:              status,
		Backend:             backend,
		PID:                 pid,
		ManagedCodexPath:    paths.ManagedCodexBin,
		ManagedCodexVersion: cloneString(managedVersion),
		SocketPath:          paths.SocketPath,
		CLIVersion:          cloneString(cliVersion),
		AppServerVersion:    cloneString(appServerVersion),
	}
}

func BuildRemoteControlOutput(paths *Paths, status RemoteControlStatus, backend *BackendKind, enabled bool, cliVersion string, appServerVersion *string) *RemoteControlOutput {
	if paths == nil {
		paths = &Paths{}
	}
	return &RemoteControlOutput{
		Status:               status,
		Backend:              backend,
		RemoteControlEnabled: enabled,
		SocketPath:           paths.SocketPath,
		CLIVersion:           cliVersion,
		AppServerVersion:     cloneString(appServerVersion),
	}
}

func NewDaemon(paths *Paths, cliVersion string) *Daemon {
	if paths == nil {
		paths = &Paths{}
	}
	return &Daemon{Paths: paths, CLIVersion: cliVersion}
}

func NewDaemonForCodexHome(codexHome string, cliVersion string) *Daemon {
	return NewDaemon(PathsForCodexHome(codexHome), cliVersion)
}

func (d *Daemon) BackendPaths(settings *DaemonSettings) BackendPaths {
	if d == nil || d.Paths == nil {
		return BackendPaths{}
	}
	remoteControlEnabled := false
	if settings != nil {
		remoteControlEnabled = settings.RemoteControlEnabled
	}
	return BackendPaths{
		CodexBin:             d.Paths.ManagedCodexBin,
		PIDFile:              d.Paths.PIDFile,
		UpdatePIDFile:        d.Paths.UpdatePIDFile,
		RemoteControlEnabled: remoteControlEnabled,
	}
}

func (d *Daemon) LoadSettings() (*DaemonSettings, error) {
	if d == nil || d.Paths == nil {
		return &DaemonSettings{}, nil
	}
	return LoadSettings(d.Paths.SettingsFile)
}

func (d *Daemon) SaveSettings(settings *DaemonSettings) error {
	if d == nil || d.Paths == nil {
		return ErrDaemonPathsRequired
	}
	return SaveSettings(d.Paths.SettingsFile, settings)
}

func (d *Daemon) LifecycleOutput(status LifecycleStatus, backend *BackendKind, pid *uint32, appServerVersion *string, managedVersion *string) *LifecycleOutput {
	var cliVersion *string
	if d != nil && d.CLIVersion != "" {
		cliVersion = &d.CLIVersion
	}
	return BuildLifecycleOutput(daemonPaths(d), status, backend, pid, cliVersion, appServerVersion, managedVersion)
}

func (d *Daemon) RemoteControlOutput(status RemoteControlStatus, backend *BackendKind, enabled bool, appServerVersion *string) *RemoteControlOutput {
	cliVersion := ""
	if d != nil {
		cliVersion = d.CLIVersion
	}
	return BuildRemoteControlOutput(daemonPaths(d), status, backend, enabled, cliVersion, appServerVersion)
}

func (d *Daemon) BootstrapOutput(settings *DaemonSettings, appServerVersion string, managedVersion *string) *BootstrapOutput {
	paths := daemonPaths(d)
	remoteControlEnabled := false
	if settings != nil {
		remoteControlEnabled = settings.RemoteControlEnabled
	}
	cliVersion := ""
	if d != nil {
		cliVersion = d.CLIVersion
	}
	return &BootstrapOutput{
		Status:               BootstrapBootstrapped,
		Backend:              BackendPID,
		AutoUpdateEnabled:    true,
		RemoteControlEnabled: remoteControlEnabled,
		ManagedCodexPath:     paths.ManagedCodexBin,
		ManagedCodexVersion:  cloneString(managedVersion),
		SocketPath:           paths.SocketPath,
		CLIVersion:           cliVersion,
		AppServerVersion:     appServerVersion,
	}
}

func (d *Daemon) RemoteControlStartFromLifecycle(output *LifecycleOutput) *RemoteControlStartOutput {
	return &RemoteControlStartOutput{Start: output}
}

func (d *Daemon) RemoteControlStartFromBootstrap(output *BootstrapOutput) *RemoteControlStartOutput {
	return &RemoteControlStartOutput{Bootstrap: output}
}

func (d *Daemon) RemoteControlReadyOutput(daemon *RemoteControlStartOutput, status RemoteControlReadyStatus) *RemoteControlReadyOutput {
	return &RemoteControlReadyOutput{Daemon: daemon, RemoteControl: status}
}

func (d *Daemon) AppServerNotReadyContext(managedVersion *string, stderrTail *PIDLogTail) string {
	paths := daemonPaths(d)
	context := fmt.Sprintf("app server did not become ready on %s\n\nDaemon used app-server:\n  path: %s\n  version: %s", paths.SocketPath, paths.ManagedCodexBin, stringValue(managedVersion, "unknown"))
	stderrTail.AppendToContext(&context)
	return context
}

func daemonPaths(d *Daemon) *Paths {
	if d == nil || d.Paths == nil {
		return &Paths{}
	}
	return d.Paths
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func stringValue(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}
