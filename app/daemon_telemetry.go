package app

import (
	"context"
	"os"
	"strings"
	"time"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/doctor"
	"codex_go/otelinit"
)

// Foreground daemon observations use the CLI's existing consent and identity
// handling (Rust cli/src/daemon_telemetry.rs). The detached updater entry points
// never call this module.

const (
	// daemonUpdateMetric mirrors Rust's `codex.daemon.update` counter.
	daemonUpdateMetric = "codex.daemon.update"
	// daemonStartMetric mirrors Rust's `codex.daemon.start` counter.
	daemonStartMetric = "codex.daemon.start"
	// daemonStartInitiationSource is the TUI auto-start initiation source.
	daemonStartInitiationSource = "tui_auto_start"
	// daemonUpdateTargetThisCLI is the update target of `daemon update --from-cli`.
	daemonUpdateTargetThisCLI = "this_cli"
	// daemonUpdateTargetPublicStable is the update target of `daemon update`.
	daemonUpdateTargetPublicStable = "public_stable"
	// daemonTelemetryShutdownTimeout bounds the short-lived provider's shutdown
	// (Rust's `shutdown_with_timeout(2s)`).
	daemonTelemetryShutdownTimeout = 2 * time.Second
)

// daemonMetricSink is the part of the metrics client the daemon counters use.
type daemonMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
}

// openDaemonMetricSink builds the short-lived provider's metrics sink. It is
// injectable so tests can pin the emission without an exporter.
var openDaemonMetricSink = buildDaemonMetricSink

// buildDaemonMetricSink loads the consent-aware configuration and builds the
// metrics sink the daemon counters report to, or reports false so the caller
// skips telemetry instead of failing the command.
func buildDaemonMetricSink(root *cli.RootOptions, analyticsDefaultEnabled bool, codexHome string) (daemonMetricSink, func(), bool) {
	var rawOverrides, enableFeatures, disableFeatures []string
	if root != nil {
		rawOverrides = append(rawOverrides, root.ConfigOverrides...)
		enableFeatures = append(enableFeatures, root.EnableFeatures...)
		disableFeatures = append(disableFeatures, root.DisableFeatures...)
	}
	loaded, err := config.LoadEffective(codexHome, rawOverrides, enableFeatures, disableFeatures, "")
	if err != nil || loaded == nil {
		return nil, nil, false
	}
	provider, err := otelinit.BuildProvider(otelinit.Options{
		Config:                  loaded,
		ServiceVersion:          doctor.Version(),
		DefaultAnalyticsEnabled: analyticsDefaultEnabled,
		// This provider is short-lived and shut down immediately, so it must not
		// replace the process-global metrics client (Rust's TUI uses a builder
		// that does not install one for the same reason).
		SkipGlobalMetricsInstall: true,
	})
	if err != nil || provider == nil || provider.Metrics() == nil {
		return nil, nil, false
	}
	return provider.Metrics(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), daemonTelemetryShutdownTimeout)
		defer cancel()
		_ = provider.Shutdown(ctx)
	}, true
}

// recordDaemonUpdateTelemetry mirrors Rust daemon_telemetry::record_command: a
// foreground handoff marker suppresses the report, the outcome is classified,
// and the bounded settings tags plus the CLI initiation source travel with the
// counter.
func recordDaemonUpdateTelemetry(root *cli.RootOptions, analyticsDefaultEnabled bool, target string, output *appserverdaemon.UpdateOutput, updateErr error) {
	if daemonTelemetryHandedOff() {
		return
	}
	codexHome := auth.DefaultCodexHome()
	sink, shutdown, ok := openDaemonMetricSink(root, analyticsDefaultEnabled, codexHome)
	if !ok {
		return
	}
	defer shutdown()
	tags := appserverdaemon.DaemonSettingsTagMap(codexHome)
	tags["initiation_source"] = "cli"
	tags["update_target"] = target
	tags["outcome"] = daemonUpdateOutcome(output, updateErr)
	sink.Counter(daemonUpdateMetric, 1, tags)
}

// daemonTelemetryHandedOff reports whether the foreground front end owns the
// daemon observation, in which case this process must not report its own.
func daemonTelemetryHandedOff() bool {
	return strings.TrimSpace(os.Getenv(appserverdaemon.TelemetryHandoffEnv)) != ""
}

// daemonUpdateOutcome mirrors Rust's outcome match: an error is unconfirmed, a
// cancelled install is "cancelled", and a result maps to its update status.
func daemonUpdateOutcome(output *appserverdaemon.UpdateOutput, updateErr error) string {
	if updateErr != nil {
		return "unconfirmed"
	}
	if output == nil {
		return "cancelled"
	}
	switch output.Status {
	case appserverdaemon.UpdateUpdated:
		return "updated"
	case appserverdaemon.UpdateNoUpdate:
		return "no_update"
	default:
		return "unsupported"
	}
}

// recordDaemonStartTelemetry mirrors Rust tui/src/daemon_telemetry.rs's
// record_start: the TUI owns the foreground observation, so no handoff marker is
// consulted, and the outcome is the lifecycle status the auto-start reached.
func recordDaemonStartTelemetry(root *cli.RootOptions, output *appserverdaemon.LifecycleOutput, startErr error) {
	codexHome := auth.DefaultCodexHome()
	// The TUI's short-lived provider uses the same consent contract as its
	// startup telemetry, with analytics enabled by default.
	sink, shutdown, ok := openDaemonMetricSink(root, true, codexHome)
	if !ok {
		return
	}
	defer shutdown()
	tags := appserverdaemon.DaemonSettingsTagMap(codexHome)
	tags["initiation_source"] = daemonStartInitiationSource
	tags["outcome"] = daemonStartOutcome(output, startErr)
	sink.Counter(daemonStartMetric, 1, tags)
}

// daemonStartOutcome mirrors Rust's lifecycle-status match: an error is
// "failed", and the statuses that do not describe a started server are
// "unconfirmed".
func daemonStartOutcome(output *appserverdaemon.LifecycleOutput, startErr error) string {
	if startErr != nil {
		return "failed"
	}
	if output == nil {
		return "unconfirmed"
	}
	switch output.Status {
	case appserverdaemon.StatusStarted:
		return "started"
	case appserverdaemon.StatusAlreadyRunning:
		return "already_running"
	case appserverdaemon.StatusRestarted:
		return "restarted"
	default:
		return "unconfirmed"
	}
}
