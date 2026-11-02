package app

import (
	"errors"

	"codex_go/appserverdaemon"
	"codex_go/cli"
)

// Rust #57267: codex-rs/tui/src/app_server_session.rs:361 ThreadParamsMode names
// the transport that owns a conversation's execution settings. The transport,
// not a CLI flag or a URL heuristic, selects the mode.
type ThreadParamsMode int

const (
	// ThreadParamsModeEmbedded runs the app server in this installation (Rust
	// AppServerTarget::Embedded and LocalDaemon). The client may send its
	// configured settings as request overrides.
	ThreadParamsModeEmbedded ThreadParamsMode = iota
	// ThreadParamsModeRemote talks to an app server on another host
	// (AppServerTarget::Remote). The client still owns the request overrides.
	ThreadParamsModeRemote
	// ThreadParamsModeCloud is a native hosted session: the server owns the
	// execution settings and the workspace.
	ThreadParamsModeCloud
)

func (m ThreadParamsMode) String() string {
	switch m {
	case ThreadParamsModeEmbedded:
		return "embedded"
	case ThreadParamsModeRemote:
		return "remote"
	case ThreadParamsModeCloud:
		return "cloud"
	default:
		return "unknown"
	}
}

// ThreadSettingsOwner mirrors Rust thread_settings_owner
// (app_server_session.rs:368): it identifies which side supplies execution
// settings for a conversation.
type ThreadSettingsOwner int

const (
	// ThreadSettingsOwnerClient lets the client send configured settings as
	// request overrides.
	ThreadSettingsOwnerClient ThreadSettingsOwner = iota
	// ThreadSettingsOwnerServer retains the server's settings unless they change
	// through a dedicated API.
	ThreadSettingsOwnerServer
)

// SettingsOwner mirrors Rust ThreadParamsMode::settings_owner
// (app_server_session.rs:388-394).
func (m ThreadParamsMode) SettingsOwner() ThreadSettingsOwner {
	if m == ThreadParamsModeCloud {
		return ThreadSettingsOwnerServer
	}
	return ThreadSettingsOwnerClient
}

// UsesRemoteWorkspace mirrors Rust uses_remote_workspace
// (app_server_session.rs:692): the workspace lives on the server, so client
// config paths must not be forwarded.
func (m ThreadParamsMode) UsesRemoteWorkspace() bool {
	return m == ThreadParamsModeRemote || m == ThreadParamsModeCloud
}

// cloudThreadForkUnsupportedMessage is the exact Rust bail text
// (app_server_session.rs:1196).
const cloudThreadForkUnsupportedMessage = "Cloud conversations cannot be forked in the TUI yet."

// cloudThreadForkError mirrors the fork guard at app_server_session.rs:1195.
func cloudThreadForkError(mode ThreadParamsMode) error {
	if mode == ThreadParamsModeCloud {
		return errors.New(cloudThreadForkUnsupportedMessage)
	}
	return nil
}

// appServerTransportKind names the app-server connection backing a TUI session.
// Rust's AppServerTarget (codex-rs/tui/src/lib.rs) has the same three shapes,
// plus AppServerClient::Cloud for hosted sessions.
type appServerTransportKind int

const (
	appServerTransportEmbedded appServerTransportKind = iota
	appServerTransportRemote
	appServerTransportCloud
)

// threadParamsModeForTransport mirrors how Rust constructs AppServerSession:
// the transport decides the mode (app_server_session.rs:491-498).
func threadParamsModeForTransport(kind appServerTransportKind) ThreadParamsMode {
	switch kind {
	case appServerTransportRemote:
		return ThreadParamsModeRemote
	case appServerTransportCloud:
		return ThreadParamsModeCloud
	default:
		return ThreadParamsModeEmbedded
	}
}

// interactiveAppServerTransport mirrors Rust AppServerTarget::thread_params_mode
// (lib.rs:400-406): only an explicit `--remote` target is Remote. Go has no
// cloud app-server transport yet, so the launch path never reports Cloud.
func interactiveAppServerTransport(root *cli.RootOptions) appServerTransportKind {
	if interactiveRemoteEndpointIsExplicitRemote(root) {
		return appServerTransportRemote
	}
	return appServerTransportEmbedded
}

// threadParamsModeForLaunch is the launch-path mode decision.
func threadParamsModeForLaunch(root *cli.RootOptions) ThreadParamsMode {
	return threadParamsModeForTransport(interactiveAppServerTransport(root))
}

// endpointAppServerTransport resolves the transport from an already-opened
// endpoint. It is the fallback for clients created without launch options;
// Embedded and Remote produce identical request parameters, so the distinction
// only documents intent until the cloud transport lands.
func endpointAppServerTransport(endpoint *appserverdaemon.RemoteAppServerEndpoint) appServerTransportKind {
	if endpoint == nil || interactiveRemoteEndpointIsLocal(endpoint) {
		return appServerTransportEmbedded
	}
	return appServerTransportRemote
}

// threadParamsModeForEndpoint resolves the mode for clients opened from an
// endpoint alone.
func threadParamsModeForEndpoint(endpoint *appserverdaemon.RemoteAppServerEndpoint) ThreadParamsMode {
	return threadParamsModeForTransport(endpointAppServerTransport(endpoint))
}
