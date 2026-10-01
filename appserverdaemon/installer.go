package appserverdaemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Guarded installer invocation (Rust app-server-daemon/src/update_loop.rs:
// fetch_installer_script, run_installer_script, InstallerMode).
//
// The published installer only selects a release when the caller supplies the
// matching guard environment variables, and this updater refuses to run a
// script that does not understand them. That keeps a daemon-owned package from
// being replaced by a stray installer run, and makes "restore production
// updates" a deliberate, single-use request instead of an ordinary socket call.

const (
	// InstallerGuardLatest lets the installer advance to the newest release.
	installerGuardLatest = "CODEX_INSTALL_IF_LATEST"
	// InstallerGuardCurrent restricts the installer to the named previous
	// release, which is how a pinned package returns to production updates.
	installerGuardCurrent = "CODEX_INSTALL_IF_CURRENT"
	// InstallerDaemonOnly tells the installer to select the daemon-owned package
	// root instead of the calling CLI's own install location.
	installerDaemonOnly = "CODEX_INSTALL_DAEMON_ONLY"
	// InstallerDeferSelection keeps the installer from publishing a selection.
	installerDeferSelection = "CODEX_INSTALL_DEFER_SELECTION"
	// InstallerRelease selects the release channel the installer may follow.
	installerRelease = "CODEX_RELEASE"
	// InstallerNonInteractive keeps the installer from prompting.
	installerNonInteractive = "CODEX_NON_INTERACTIVE"
	// InstallerUpdateFromRelease names the release an update started from
	// (Rust run_installer_script's CODEX_UPDATE_FROM_RELEASE).
	installerUpdateFromRelease = "CODEX_UPDATE_FROM_RELEASE"
)

type installerModeKind int

const (
	// installerUpdate advances the package to the latest published release.
	installerUpdate installerModeKind = iota
	// installerRestoreProduction returns a pinned package to production updates,
	// but only while the expected release is still selected.
	installerRestoreProduction
	// installerMigration lets a migration program choose the selection itself.
	installerMigration
)

// installerMode is the guard set one installer run is authorized with.
type installerMode struct {
	Kind    installerModeKind
	Release string
}

// env is the guard environment for this mode (Rust run_installer_script).
func (m installerMode) env() map[string]string {
	latest, current, deferSelection := "1", "0", "0"
	switch m.Kind {
	case installerRestoreProduction:
		latest, current = "0", "1"
	case installerMigration:
		latest, deferSelection = "0", "1"
	}
	return map[string]string{
		installerGuardLatest:       latest,
		installerGuardCurrent:      current,
		installerDeferSelection:    deferSelection,
		installerRelease:           "latest",
		installerNonInteractive:    "1",
		installerUpdateFromRelease: m.Release,
	}
}

// requiredMarkers are the substrings the fetched script must contain before this
// updater hands it the guards that select a package.
func (m installerMode) requiredMarkers() []string {
	if m.Kind == installerMigration {
		// Migration never selects a release itself, so the script only has to
		// defer the selection (Rust migration::run's explicit assertion).
		return []string{installerDeferSelection}
	}
	guard := installerGuardLatest
	if m.Kind == installerRestoreProduction {
		guard = installerGuardCurrent
	}
	return []string{guard}
}

// validateInstallerScript mirrors Rust's guard assertions: the script must
// understand the guard it is about to receive, and a daemon-owned package root
// additionally requires daemon-only support.
func validateInstallerScript(script []byte, mode installerMode, packageRoot string) error {
	for _, marker := range mode.requiredMarkers() {
		if !bytes.Contains(script, []byte(marker)) {
			return fmt.Errorf("standalone installer does not support %s guarded updates", marker)
		}
	}
	if strings.HasSuffix(packageRoot, daemonPackagesDirname) &&
		!bytes.Contains(script, []byte(installerDaemonOnly)) {
		return errors.New("installer does not support daemon-owned packages")
	}
	return nil
}

// fetchInstallerScript downloads the published installer the updater runs
// (Rust fetch_installer_script).
func fetchInstallerScript(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, InstallScriptEndpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch standalone Codex updater: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("standalone Codex updater request failed: %s", response.Status)
	}
	script, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read standalone Codex updater: %w", err)
	}
	return script, nil
}

// runInstallerScript runs the fetched installer with this mode's guards
// (Rust run_installer_script). The daemon-owned package root is passed through
// CODEX_HOME so the installer replaces the package this updater manages.
func runInstallerScript(ctx context.Context, script []byte, mode installerMode, packageRoot string, codexHome string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	env := mode.env()
	if strings.TrimSpace(codexHome) != "" {
		env["CODEX_HOME"] = codexHome
	}
	env[installerDaemonOnly] = boolDigit(strings.HasSuffix(packageRoot, daemonPackagesDirname))
	return runInstallerProcess(ctx, script, env)
}

func boolDigit(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// installWithGuards fetches, validates, and runs the installer for one mode
// (Rust update_once's installer half).
func installWithGuards(ctx context.Context, mode installerMode, packageRoot string, codexHome string, fetch func(context.Context) ([]byte, error), run func(context.Context, []byte, installerMode, string, string) error) error {
	if fetch == nil {
		fetch = fetchInstallerScript
	}
	if run == nil {
		run = runInstallerScript
	}
	script, err := fetch(ctx)
	if err != nil {
		return err
	}
	if err := validateInstallerScript(script, mode, packageRoot); err != nil {
		return err
	}
	return run(ctx, script, mode, packageRoot, codexHome)
}
