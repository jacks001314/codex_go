package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultGitHubLatestReleaseURL = "https://api.github.com/repos/openai/codex/releases/latest"
	DefaultHomebrewCaskURL        = "https://formulae.brew.sh/api/cask/codex.json"
	DefaultNPMRegistryURL         = "https://registry.npmjs.org/@openai%2fcodex"
)

type UpdateActionKind string

const (
	UpdateActionNPMGlobalLatest UpdateActionKind = "npm-global-latest"
	UpdateActionBunGlobalLatest UpdateActionKind = "bun-global-latest"
	UpdateActionBrewUpgrade     UpdateActionKind = "brew-upgrade"
	UpdateActionStandaloneUnix  UpdateActionKind = "standalone-unix"
	UpdateActionStandaloneWin   UpdateActionKind = "standalone-windows"
)

type UpdateStatus string

const (
	UpdateStatusUpToDate        UpdateStatus = "up-to-date"
	UpdateStatusUpdateAvailable UpdateStatus = "update-available"
	UpdateStatusUnknown         UpdateStatus = "unknown"
	UpdateStatusUpdated         UpdateStatus = "updated"
)

type UpdateAction struct {
	Kind UpdateActionKind `json:"kind"`
}

type UpdateCheckOptions struct {
	Context        *InstallContext
	CurrentVersion string
	HTTPClient     *http.Client
	GitHubURL      string
	HomebrewURL    string
	NPMRegistryURL string
}

type UpdateResult struct {
	Status         UpdateStatus  `json:"status"`
	InstallMethod  string        `json:"installMethod"`
	CheckedOnline  bool          `json:"checkedOnline"`
	CurrentVersion string        `json:"currentVersion,omitempty"`
	LatestVersion  string        `json:"latestVersion,omitempty"`
	UpdateAction   *UpdateAction `json:"updateAction,omitempty"`
	UpdateCommand  []string      `json:"updateCommand,omitempty"`
	Message        string        `json:"message,omitempty"`
}

type RunUpdateOptions struct {
	Context *InstallContext
	Stdout  interface {
		Write([]byte) (int, error)
	}
	Stderr interface {
		Write([]byte) (int, error)
	}
	CommandRunner CommandRunner
}

type CommandRunner interface {
	Run(ctx context.Context, command string, args []string) error
}

type ExecCommandRunner struct {
	Stdout interface {
		Write([]byte) (int, error)
	}
	Stderr interface {
		Write([]byte) (int, error)
	}
}

func ActionFromContext(context *InstallContext) *UpdateAction {
	if context == nil {
		return nil
	}
	switch context.Method.Kind {
	case InstallNPM:
		return &UpdateAction{Kind: UpdateActionNPMGlobalLatest}
	case InstallBun:
		return &UpdateAction{Kind: UpdateActionBunGlobalLatest}
	case InstallBrew:
		return &UpdateAction{Kind: UpdateActionBrewUpgrade}
	case InstallStandalone:
		if context.Method.Platform != nil && *context.Method.Platform == StandaloneWindows {
			return &UpdateAction{Kind: UpdateActionStandaloneWin}
		}
		return &UpdateAction{Kind: UpdateActionStandaloneUnix}
	default:
		return nil
	}
}

func (a *UpdateAction) CommandArgs() (string, []string) {
	if a == nil {
		return "", nil
	}
	switch a.Kind {
	case UpdateActionNPMGlobalLatest:
		return "npm", []string{"install", "-g", "@openai/codex"}
	case UpdateActionBunGlobalLatest:
		return "bun", []string{"install", "-g", "@openai/codex"}
	case UpdateActionBrewUpgrade:
		return "brew", []string{"upgrade", "--cask", "codex"}
	case UpdateActionStandaloneUnix:
		return "sh", []string{"-c", "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh"}
	case UpdateActionStandaloneWin:
		return "powershell", []string{"-ExecutionPolicy", "Bypass", "-c", "$env:CODEX_NON_INTERACTIVE=1; irm https://chatgpt.com/codex/install.ps1 | iex"}
	default:
		return "", nil
	}
}

func (a *UpdateAction) CommandLine() string {
	command, args := a.CommandArgs()
	if command == "" {
		return ""
	}
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, command)
	parts = append(parts, args...)
	return joinCommandLine(parts)
}

func CheckForUpdate(ctx context.Context, opts *UpdateCheckOptions) (*UpdateResult, error) {
	if opts == nil {
		opts = &UpdateCheckOptions{}
	}
	installContext := opts.Context
	if installContext == nil {
		installContext = Current()
	}
	method := InstallOther
	if installContext != nil && installContext.Method.Kind != "" {
		method = installContext.Method.Kind
	}
	currentVersion := strings.TrimSpace(opts.CurrentVersion)
	action := ActionFromContext(installContext)
	result := &UpdateResult{
		Status:         UpdateStatusUnknown,
		InstallMethod:  string(method),
		CheckedOnline:  true,
		CurrentVersion: currentVersion,
		UpdateAction:   action,
	}
	if action != nil {
		command, args := action.CommandArgs()
		result.UpdateCommand = append([]string{command}, args...)
	}
	latest, err := FetchLatestVersion(ctx, opts)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	result.LatestVersion = latest
	newer := IsNewerVersion(latest, currentVersion)
	switch {
	case newer == nil:
		result.Status = UpdateStatusUnknown
	case *newer:
		result.Status = UpdateStatusUpdateAvailable
	default:
		result.Status = UpdateStatusUpToDate
	}
	return result, nil
}

func OfflineUpdateStatus(context *InstallContext, currentVersion string) *UpdateResult {
	method := InstallOther
	if context != nil && context.Method.Kind != "" {
		method = context.Method.Kind
	}
	action := ActionFromContext(context)
	result := &UpdateResult{
		Status:         UpdateStatusUpToDate,
		InstallMethod:  string(method),
		CheckedOnline:  false,
		CurrentVersion: strings.TrimSpace(currentVersion),
		UpdateAction:   action,
	}
	if action != nil {
		command, args := action.CommandArgs()
		result.UpdateCommand = append([]string{command}, args...)
	}
	if IsSourceBuildVersion(currentVersion) {
		result.Status = UpdateStatusUnknown
		result.Message = "source build version cannot be checked offline"
	}
	return result
}

func RunUpdate(ctx context.Context, opts *RunUpdateOptions) (*UpdateResult, error) {
	if opts == nil {
		opts = &RunUpdateOptions{}
	}
	installContext := opts.Context
	if installContext == nil {
		installContext = Current()
	}
	result := OfflineUpdateStatus(installContext, "")
	action := ActionFromContext(installContext)
	if action == nil {
		result.Status = UpdateStatusUnknown
		result.Message = "could not detect the Codex installation method"
		return result, errors.New("could not detect the Codex installation method; please update manually: https://developers.openai.com/codex/cli/")
	}
	command, args := action.CommandArgs()
	if command == "" {
		result.Status = UpdateStatusUnknown
		result.Message = "update action does not have a command"
		return result, fmt.Errorf("update action %s does not have a command", action.Kind)
	}
	runner := opts.CommandRunner
	if runner == nil {
		runner = &ExecCommandRunner{Stdout: opts.Stdout, Stderr: opts.Stderr}
	}
	if err := runner.Run(ctx, command, args); err != nil {
		result.Status = UpdateStatusUnknown
		result.Message = err.Error()
		return result, fmt.Errorf("`%s` failed: %w", action.CommandLine(), err)
	}
	result.Status = UpdateStatusUpdated
	result.Message = "update ran successfully; restart Codex to use the new version"
	return result, nil
}

func (r *ExecCommandRunner) Run(ctx context.Context, command string, args []string) error {
	actualCommand := command
	actualArgs := append([]string(nil), args...)
	if runtime.GOOS == "windows" && command != "powershell" {
		actualCommand = "cmd"
		actualArgs = []string{"/C", joinCommandLine(append([]string{command}, args...))}
	}
	cmd := exec.CommandContext(ctx, actualCommand, actualArgs...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

func FetchLatestVersion(ctx context.Context, opts *UpdateCheckOptions) (string, error) {
	if opts == nil {
		opts = &UpdateCheckOptions{}
	}
	installContext := opts.Context
	if installContext == nil {
		installContext = Current()
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	switch {
	case installContext != nil && installContext.Method.Kind == InstallBrew:
		homebrewURL := firstNonEmpty(opts.HomebrewURL, DefaultHomebrewCaskURL)
		var info struct {
			Version string `json:"version"`
		}
		if err := getJSON(ctx, client, homebrewURL, &info); err != nil {
			return "", err
		}
		if strings.TrimSpace(info.Version) == "" {
			return "", errors.New("homebrew cask metadata omitted version")
		}
		return strings.TrimSpace(info.Version), nil
	case installContext != nil && (installContext.Method.Kind == InstallNPM || installContext.Method.Kind == InstallBun):
		latest, err := fetchLatestGitHubReleaseVersion(ctx, client, firstNonEmpty(opts.GitHubURL, DefaultGitHubLatestReleaseURL))
		if err != nil {
			return "", err
		}
		npmURL := firstNonEmpty(opts.NPMRegistryURL, DefaultNPMRegistryURL)
		var packageInfo npmPackageInfo
		if err := getJSON(ctx, client, npmURL, &packageInfo); err != nil {
			return "", err
		}
		if err := packageInfo.ensureVersionReady(latest); err != nil {
			return "", err
		}
		return latest, nil
	default:
		return fetchLatestGitHubReleaseVersion(ctx, client, firstNonEmpty(opts.GitHubURL, DefaultGitHubLatestReleaseURL))
	}
}

func ExtractVersionFromLatestTag(tag string) (string, error) {
	version, ok := strings.CutPrefix(strings.TrimSpace(tag), "rust-v")
	if !ok || strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("failed to parse latest tag name %q", tag)
	}
	return strings.TrimSpace(version), nil
}

func IsNewerVersion(latest string, current string) *bool {
	latestVersion, latestOK := parsePlainVersion(latest)
	currentVersion, currentOK := parsePlainVersion(current)
	if !latestOK || !currentOK {
		return nil
	}
	value := compareVersionTriplet(latestVersion, currentVersion) > 0
	return &value
}

func IsSourceBuildVersion(version string) bool {
	parsed, ok := parsePlainVersion(version)
	return ok && parsed == [3]uint64{}
}

func fetchLatestGitHubReleaseVersion(ctx context.Context, client *http.Client, url string) (string, error) {
	var info struct {
		TagName string `json:"tag_name"`
	}
	if err := getJSON(ctx, client, url, &info); err != nil {
		return "", err
	}
	return ExtractVersionFromLatestTag(info.TagName)
}

func getJSON(ctx context.Context, client *http.Client, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GET %s failed: %s", url, response.Status)
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

type npmPackageInfo struct {
	DistTags map[string]string                `json:"dist-tags"`
	Versions map[string]npmPackageVersionInfo `json:"versions"`
}

type npmPackageVersionInfo struct {
	Dist *npmPackageDist `json:"dist"`
}

type npmPackageDist struct {
	Tarball   string `json:"tarball"`
	Integrity string `json:"integrity"`
}

func (p *npmPackageInfo) ensureVersionReady(version string) error {
	version = strings.TrimSpace(version)
	if p == nil {
		return errors.New("npm package metadata is nil")
	}
	latest, ok := p.DistTags["latest"]
	if !ok || latest == "" {
		return errors.New("npm package is missing latest dist-tag")
	}
	if latest != version {
		return fmt.Errorf("npm latest dist-tag points to %s, expected GitHub release %s", latest, version)
	}
	info, ok := p.Versions[version]
	if !ok {
		return fmt.Errorf("npm package version %s is missing", version)
	}
	if info.Dist == nil {
		return fmt.Errorf("npm package version %s is missing dist metadata", version)
	}
	if strings.TrimSpace(info.Dist.Tarball) == "" {
		return fmt.Errorf("npm package version %s is missing dist.tarball", version)
	}
	if strings.TrimSpace(info.Dist.Integrity) == "" {
		return fmt.Errorf("npm package version %s is missing dist.integrity", version)
	}
	return nil
}

func parsePlainVersion(value string) ([3]uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return [3]uint64{}, false
	}
	var parsed [3]uint64
	for i, part := range parts {
		if part == "" || strings.ContainsAny(part, "-+ ") {
			return [3]uint64{}, false
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return [3]uint64{}, false
		}
		parsed[i] = n
	}
	return parsed, true
}

func compareVersionTriplet(a [3]uint64, b [3]uint64) int {
	for i := 0; i < 3; i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

func joinCommandLine(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			quoted = append(quoted, `""`)
			continue
		}
		if strings.ContainsAny(part, " \t\r\n\"'|&;$()<>") {
			quoted = append(quoted, strconv.Quote(part))
			continue
		}
		quoted = append(quoted, part)
	}
	return strings.Join(quoted, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
