package app

import (
	"context"
	"time"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/doctor"
	"codex_go/otelinit"
	"codex_go/shell"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/startup_orchestration.rs and
// daemon_telemetry.rs (#53859). The interactive TUI builds its own OTEL
// provider, records one `codex.tui.start` observation carrying the bounded
// daemon settings, terminal size buckets, daemon selection reason and terminal
// category, then keeps the size-free launch tags as the context for later
// `codex.tui.terminal_size` observations.

const tuiTelemetryShutdownTimeout = 2 * time.Second

// tuiMetricSink is the part of the metrics client the TUI launch counter uses.
type tuiMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
}

// openTUIMetricSink builds the TUI's process metrics provider. It is injectable
// so tests can pin the launch tags without an exporter.
var openTUIMetricSink = buildTUIMetricSink

// buildTUIMetricSink installs the provider's metrics client as the
// process-global recorder, which is what `codex.tui.terminal_size` records to.
func buildTUIMetricSink(cfg *config.Config) (tuiMetricSink, func(), bool) {
	provider, err := otelinit.BuildProvider(otelinit.Options{
		Config:                  cfg,
		ServiceVersion:          doctor.Version(),
		DefaultAnalyticsEnabled: true,
	})
	if err != nil || provider == nil || provider.Metrics() == nil {
		return nil, nil, false
	}
	return provider.Metrics(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), tuiTelemetryShutdownTimeout)
		defer cancel()
		_ = provider.Shutdown(ctx)
	}, true
}

// interactiveTUITelemetry is the launch context captured at TUI startup.
type interactiveTUITelemetry struct {
	context          map[string]string
	exporter         config.OtelExporterKind
	analyticsEnabled *bool
	shutdown         func()
}

func (t interactiveTUITelemetry) close() {
	if t.shutdown != nil {
		t.shutdown()
	}
}

// startInteractiveTUITelemetry mirrors Rust's startup orchestration: build the
// TUI's OTEL provider, record `codex.tui.start`, and return the launch context
// without the terminal size buckets.
func startInteractiveTUITelemetry(root *cli.RootOptions, selectionReason string, appServerMode string) interactiveTUITelemetry {
	telemetry := interactiveTUITelemetry{}
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return telemetry
	}
	telemetry.exporter = loaded.Otel().MetricsExporter
	telemetry.analyticsEnabled = loaded.AnalyticsEnabledValue()
	sink, shutdown, ok := openTUIMetricSink(loaded)
	if !ok || sink == nil {
		return telemetry
	}
	telemetry.shutdown = shutdown
	tags := appserverdaemon.DaemonSettingsTagMap(auth.DefaultCodexHome())
	width, height := codextui.TerminalSizeRead()
	for key, value := range codextui.TerminalSizeTags(width, height) {
		tags[key] = value
	}
	tags["daemon_selection_reason"] = selectionReason
	tags["daemon_auto_start"] = "disabled"
	if daemonAutoStartFeature(root) {
		tags["daemon_auto_start"] = "enabled"
	}
	tags["app_server_mode"] = appServerMode
	terminalInfo := shell.CurrentTerminalInfo()
	tags["terminal_name"] = codextui.TerminalTelemetryName(terminalInfo)
	tags["multiplexer"] = codextui.TerminalMultiplexerTag(terminalInfo)
	// Pass a copy: the context below drops the size buckets from the same map.
	sink.Counter(codextui.TUIStartMetric, 1, cloneTUITelemetryTags(tags))
	// Rust #57268: an embedded TUI records why the shared app server was not
	// used, with a bounded tag value.
	if appServerMode == "in_process" {
		sink.Counter(codextui.TUIEmbeddedStartMetric, 1, map[string]string{
			"reason": interactiveEmbeddedTUIStartReason(root, selectionReason),
		})
	}
	// Rust returns the launch tags without the size buckets so the size-free
	// context is reused by every terminal-size observation.
	delete(tags, codextui.TerminalColumnsTag)
	delete(tags, codextui.TerminalRowsTag)
	telemetry.context = tags
	return telemetry
}

// interactiveEmbeddedTUIStartReason mirrors Rust #57268's embedded_start reason
// match. The values stay bounded so the tag cannot leak launch details.
func interactiveEmbeddedTUIStartReason(root *cli.RootOptions, selectionReason string) string {
	exclusion := daemonAutoStartExclusionReason
	if exclusion == "" {
		exclusion = daemonStartupExclusion(root, false)
	}
	switch {
	case daemonElevatedLaunchExcluded(root, false):
		return "windows_elevated"
	case exclusion == "--no-daemon":
		return "explicit_no_daemon"
	case exclusion == "--profile":
		return "profile"
	case exclusion == "--oss":
		return "oss"
	case exclusion == "--strict-config":
		return "strict_config"
	case exclusion == "--dangerously-bypass-hook-trust":
		return "bypass_hook_trust"
	case exclusion == "workload identity":
		return "workload_identity"
	case exclusion == "executor selection (CODEX_EXEC_SERVER_URL)":
		return "exec_server_url"
	case exclusion == daemonConfigOverrideExclusion:
		if daemonSearchForcesEmbedded(root) {
			return "search"
		}
		return "config_override"
	case exclusion == "Bedrock sign-in":
		return "bedrock_sign_in"
	case exclusion == wslDrvfsExclusion:
		return "wsl_drvfs"
	case exclusion == detachedLaunchRestrictionExclusion:
		return "windows_detach_restricted"
	case exclusion == "daemon feature settings":
		return "daemon_compatibility"
	case exclusion != "":
		return "unclassified"
	}
	switch selectionReason {
	case "auto_start_disabled":
		return "auto_start_disabled_no_daemon"
	case "auto_start", "existing_daemon":
		return "daemon_connection_failed"
	case "agents":
		return "agents_no_daemon_discovered"
	default:
		return "unclassified"
	}
}

func cloneTUITelemetryTags(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

// interactiveDaemonSelectionReason mirrors Rust's selection_reason match in
// startup_orchestration.rs.
func interactiveDaemonSelectionReason(root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, explicitRemote bool) string {
	if explicitRemote {
		return "explicit_remote"
	}
	if daemonElevatedLaunchExcluded(root, false) {
		return "elevated_windows"
	}
	exclusion := daemonAutoStartExclusionReason
	if exclusion == "" {
		exclusion = daemonStartupExclusion(root, false)
	}
	switch exclusion {
	case "--no-daemon":
		return "explicit_no_daemon"
	case "":
		// Fall through to the auto-start / reuse decision.
	default:
		return "incompatible_option"
	}
	if daemonAutoStartRequested(root) {
		return "auto_start"
	}
	if endpoint != nil {
		return "existing_daemon"
	}
	return "auto_start_disabled"
}
