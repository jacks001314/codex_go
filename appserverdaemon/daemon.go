package appserverdaemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/codexuds"
	"codex_go/install"
	"codex_go/remotecontrol"
)

const (
	// PIDFileName and UpdatePIDFileName name the pid records of a daemon that
	// owns its package (Rust DAEMON_PID_FILE_NAME / DAEMON_UPDATE_PID_FILE_NAME).
	// A launch that reuses the pre-dedicated package uses the legacy names
	// instead; see PathsForCodexHome.
	PIDFileName           = "daemon.pid"
	UpdatePIDFileName     = "daemon-updater.pid"
	OperationLockFileName = "daemon.lock"
	SettingsFileName      = "settings.json"
	StateDirName          = "app-server-daemon"
	ControlDirName        = "app-server-control"
	ControlSocketFileName = "app-server-control.sock"
	// DaemonShutdownFileEnv names the env var carrying the shutdown-request
	// file a detached pid-managed *updater* must watch: it has no control
	// socket, so its owner requests termination through a file in the private
	// state directory (Rust app-server-transport::daemon_shutdown).
	DaemonShutdownFileEnv = "CODEX_DAEMON_SHUTDOWN_FILE"
	// DaemonShutdownSocketEnv names the env var that tells a pid-managed
	// app-server it may accept the control socket's `/daemon/shutdown` request
	// (Rust app-server-transport::DAEMON_SHUTDOWN_SOCKET_ENV).
	DaemonShutdownSocketEnv = "CODEX_DAEMON_SHUTDOWN_SOCKET"
	// UpdaterPIDFileEnv carries the update-loop PID file to a reexecuted
	// successor updater so it can claim PID ownership (Rust #42392).
	UpdaterPIDFileEnv = "CODEX_UPDATER_PID_FILE"
)

var ErrUnsupportedPlatform = errors.New("codex app-server daemon lifecycle is only supported on Unix and Windows platforms")
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
	// RestartIfBinaryOrVersionChanged restarts when the managed executable is a
	// different binary than the running one, or when its version changed
	// (Rust RestartMode::IfBinaryOrVersionChanged).
	RestartIfBinaryOrVersionChanged RestartMode = "ifBinaryOrVersionChanged"
	RestartAlways                   RestartMode = "always"
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

const (
	// DefaultShutdownGraceSeconds is how long a managed app-server shutdown
	// waits for a graceful exit before forcing termination (Rust #43572).
	DefaultShutdownGraceSeconds = 60
	// MaxShutdownGraceSeconds bounds the configurable grace period.
	MaxShutdownGraceSeconds = 5 * 60
	// DefaultUpdateIntervalMinutes is how often the managed updater checks for a
	// newer package unless settings say otherwise (Rust
	// settings::DEFAULT_UPDATE_INTERVAL_MINUTES).
	DefaultUpdateIntervalMinutes = 60
)

// DaemonUpdaterSettings is the stored `updater` block. Both fields default when
// absent, so a missing block means "update hourly".
type DaemonUpdaterSettings struct {
	AutoUpdateEnabled     *bool `json:"autoUpdateEnabled,omitempty"`
	UpdateIntervalMinutes *int  `json:"updateIntervalMinutes,omitempty"`
}

// AutoUpdateEnabledValue resolves whether the managed updater may replace the
// daemon package on its own.
func (u *DaemonUpdaterSettings) AutoUpdateEnabledValue() bool {
	if u == nil || u.AutoUpdateEnabled == nil {
		return true
	}
	return *u.AutoUpdateEnabled
}

// UpdateIntervalMinutesValue resolves the updater's cadence in minutes.
func (u *DaemonUpdaterSettings) UpdateIntervalMinutesValue() int {
	if u == nil || u.UpdateIntervalMinutes == nil {
		return DefaultUpdateIntervalMinutes
	}
	return *u.UpdateIntervalMinutes
}

func (u *DaemonUpdaterSettings) validate() error {
	if u == nil || u.UpdateIntervalMinutes == nil {
		return nil
	}
	if *u.UpdateIntervalMinutes <= 0 {
		return errors.New("update interval must be positive")
	}
	return nil
}

type DaemonSettings struct {
	RemoteControlEnabled bool `json:"remoteControlEnabled"`
	// FeatureOverrides are the `features.<name>=<bool>` switches the managed
	// app-server is launched with, so a shared server keeps the services the
	// launch that started it asked for (Rust DaemonSettings::feature_overrides).
	FeatureOverrides map[string]bool `json:"featureOverrides,omitempty"`
	// ShutdownGraceSeconds bounds the managed app-server shutdown grace period.
	// Nil means the default; valid values are 0 through MaxShutdownGraceSeconds.
	ShutdownGraceSeconds *int `json:"shutdownGraceSeconds,omitempty"`
	// Updater mirrors the stored updater block. Nil means the defaults.
	Updater *DaemonUpdaterSettings `json:"updater,omitempty"`
}

// AutoUpdateEnabled reports whether the managed updater may replace the daemon
// package on its own.
func (s *DaemonSettings) AutoUpdateEnabled() bool {
	if s == nil {
		return true
	}
	return s.Updater.AutoUpdateEnabledValue()
}

// UpdateInterval resolves the managed updater's cadence.
func (s *DaemonSettings) UpdateInterval() time.Duration {
	if s == nil {
		return time.Duration(DefaultUpdateIntervalMinutes) * time.Minute
	}
	return time.Duration(s.Updater.UpdateIntervalMinutesValue()) * time.Minute
}

// CloneFeatureOverrides returns a copy so callers cannot mutate a stored map.
func CloneFeatureOverrides(overrides map[string]bool) map[string]bool {
	if len(overrides) == 0 {
		return nil
	}
	clone := make(map[string]bool, len(overrides))
	for name, enabled := range overrides {
		clone[name] = enabled
	}
	return clone
}

// FeatureOverridesEqual reports whether two override sets agree, treating an
// empty set and a missing one as equal (Rust's BTreeMap equality).
func FeatureOverridesEqual(a map[string]bool, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name, enabled := range a {
		if other, ok := b[name]; !ok || other != enabled {
			return false
		}
	}
	return true
}

// daemonSettingsEqual compares the launch settings a restart may replace
// (Rust DaemonSettings: PartialEq).
func daemonSettingsEqual(a *DaemonSettings, b *DaemonSettings) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.RemoteControlEnabled != b.RemoteControlEnabled ||
		!FeatureOverridesEqual(a.FeatureOverrides, b.FeatureOverrides) {
		return false
	}
	if (a.ShutdownGraceSeconds == nil) != (b.ShutdownGraceSeconds == nil) {
		return false
	}
	if a.ShutdownGraceSeconds != nil && *a.ShutdownGraceSeconds != *b.ShutdownGraceSeconds {
		return false
	}
	return a.AutoUpdateEnabled() == b.AutoUpdateEnabled() &&
		a.UpdateInterval() == b.UpdateInterval()
}

// ShutdownGraceSecondsValue resolves the configured grace period, falling back
// to the default for a missing or out-of-range value.
func (s *DaemonSettings) ShutdownGraceSecondsValue() int {
	if s == nil || s.ShutdownGraceSeconds == nil {
		return DefaultShutdownGraceSeconds
	}
	seconds := *s.ShutdownGraceSeconds
	if seconds < 0 || seconds > MaxShutdownGraceSeconds {
		return DefaultShutdownGraceSeconds
	}
	return seconds
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
	// LogDiagnostics routes startup diagnostics to the caller's logging instead
	// of stderr, which a live TUI owns (Rust Daemon::log_diagnostics).
	LogDiagnostics bool
}

// EnsureSupportedPlatform mirrors Rust ensure_supported_platform: the managed
// lifecycle runs on Unix and Windows.
func EnsureSupportedPlatform() error {
	switch runtime.GOOS {
	case "windows", "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
		"linux", "netbsd", "openbsd", "solaris":
		return nil
	default:
		return ErrUnsupportedPlatform
	}
}

func PathsForCodexHome(codexHome string) *Paths {
	stateDir := filepath.Join(codexHome, StateDirName)
	pidFileName, updatePIDFileName := PIDFileName, UpdatePIDFileName
	if legacyPackageSelection(codexHome) {
		// A CLI that reuses the pre-dedicated standalone package keeps writing
		// the legacy pid records so both clients agree on one backend
		// (Rust Daemon::from_environment).
		pidFileName, updatePIDFileName = LegacyPIDFileName, LegacyUpdatePIDFileName
	}
	return &Paths{
		CodexHome:         codexHome,
		SocketPath:        AppServerControlSocketPath(codexHome),
		PIDFile:           filepath.Join(stateDir, pidFileName),
		UpdatePIDFile:     filepath.Join(stateDir, updatePIDFileName),
		OperationLockFile: filepath.Join(stateDir, OperationLockFileName),
		SettingsFile:      filepath.Join(stateDir, SettingsFileName),
		ManagedCodexBin:   ManagedCodexBin(codexHome),
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
	if err := settings.Updater.validate(); err != nil {
		return nil, err
	}
	if settings.ShutdownGraceSeconds != nil && (*settings.ShutdownGraceSeconds < 0 || *settings.ShutdownGraceSeconds > MaxShutdownGraceSeconds) {
		return nil, fmt.Errorf("shutdown grace must be between 0 and %d seconds", MaxShutdownGraceSeconds)
	}
	return &settings, nil
}

// LoadSettingsForStop reads the shutdown grace period tolerantly so `daemon
// stop` still works with unreadable or partially edited settings (Rust #43572).
func LoadSettingsForStop(path string) *DaemonSettings {
	settings, err := LoadSettings(path)
	if err != nil {
		return &DaemonSettings{}
	}
	return settings
}

func SaveSettings(path string, settings *DaemonSettings) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: settings file is empty", ErrDaemonPathsRequired)
	}
	if settings == nil {
		settings = &DaemonSettings{}
	}
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("failed to create daemon settings directory %s: %w", filepath.Dir(path), err)
	}
	// Read-modify-write so settings written by other versions (for example an
	// updater block or a custom shutdown grace) survive a save (Rust #43572).
	values := map[string]any{}
	if existing, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(existing, &values)
	}
	if values == nil {
		values = map[string]any{}
	}
	values["remoteControlEnabled"] = settings.RemoteControlEnabled
	if settings.ShutdownGraceSeconds != nil {
		values["shutdownGraceSeconds"] = *settings.ShutdownGraceSeconds
	}
	if len(settings.FeatureOverrides) == 0 {
		// Rust removes the key instead of storing an empty map.
		delete(values, "featureOverrides")
	} else {
		values["featureOverrides"] = settings.FeatureOverrides
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize settings: %w", err)
	}
	data = append(data, '\n')
	// Publish atomically so a crash cannot leave settings half-written
	// (Rust DaemonSettings::save writes a temporary file and renames it).
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("failed to write daemon settings %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("failed to replace daemon settings %s: %w", path, err)
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

// RestartDecisionFor mirrors Rust `restart_decision`: IfBinaryOrVersionChanged
// is resolved by the running backend's executable identity before this call, so
// only the version comparison reaches it.
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

// Diagnostic reports a startup diagnostic to stderr, or suppresses it while a
// live front end owns the terminal (Rust Daemon::diagnostic).
func (d *Daemon) Diagnostic(format string, args ...any) {
	if d != nil && d.LogDiagnostics {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func NewDaemonForCodexHome(codexHome string, cliVersion string) *Daemon {
	return NewDaemon(PathsForCodexHome(codexHome), cliVersion)
}

func (d *Daemon) BackendPaths(settings *DaemonSettings) BackendPaths {
	if d == nil || d.Paths == nil {
		return BackendPaths{}
	}
	remoteControlEnabled := false
	var featureOverrides map[string]bool
	if settings != nil {
		remoteControlEnabled = settings.RemoteControlEnabled
		featureOverrides = settings.FeatureOverrides
	}
	return BackendPaths{
		CodexBin:             d.Paths.ManagedCodexBin,
		PIDFile:              d.Paths.PIDFile,
		UpdatePIDFile:        d.Paths.UpdatePIDFile,
		RemoteControlEnabled: remoteControlEnabled,
		FeatureOverrides:     featureOverrides,
	}
}

func (d *Daemon) LoadSettings() (*DaemonSettings, error) {
	if d == nil || d.Paths == nil {
		return &DaemonSettings{}, nil
	}
	return LoadSettings(d.Paths.SettingsFile)
}

// LoadSettingsForStop reads settings tolerantly for the stop command.
func (d *Daemon) LoadSettingsForStop() *DaemonSettings {
	if d == nil || d.Paths == nil {
		return &DaemonSettings{}
	}
	return LoadSettingsForStop(d.Paths.SettingsFile)
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
