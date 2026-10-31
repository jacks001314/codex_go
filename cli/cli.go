package cli

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Command string

const (
	CommandInteractive       Command = "interactive"
	CommandExec              Command = "exec"
	CommandReview            Command = "review"
	CommandLogin             Command = "login"
	CommandLogout            Command = "logout"
	CommandMCP               Command = "mcp"
	CommandPlugin            Command = "plugin"
	CommandAppServer         Command = "app-server"
	CommandApp               Command = "app"
	CommandRemoteControl     Command = "remote-control"
	CommandCompletion        Command = "completion"
	CommandUpdate            Command = "update"
	CommandDoctor            Command = "doctor"
	CommandSandbox           Command = "sandbox"
	CommandDebug             Command = "debug"
	CommandExecpolicy        Command = "execpolicy"
	CommandApply             Command = "apply"
	CommandResume            Command = "resume"
	CommandArchive           Command = "archive"
	CommandDelete            Command = "delete"
	CommandUnarchive         Command = "unarchive"
	CommandFork              Command = "fork"
	CommandCloud             Command = "cloud"
	CommandResponsesAPIProxy Command = "responses-api-proxy"
	CommandStdioToUDS        Command = "stdio-to-uds"
	CommandExecServer        Command = "exec-server"
	CommandFeatures          Command = "features"
	CommandMigrateRollouts   Command = "migrate-rollouts"
	CommandMigrateNFSDB      Command = "migrate-nfs-db"
	CommandAgents            Command = "agents"
	CommandQueue             Command = "queue"
	CommandTcpTunnel         Command = "tcp-tunnel"
)

var knownCommands = map[string]Command{
	"exec":                CommandExec,
	"e":                   CommandExec,
	"review":              CommandReview,
	"login":               CommandLogin,
	"logout":              CommandLogout,
	"mcp":                 CommandMCP,
	"plugin":              CommandPlugin,
	"app-server":          CommandAppServer,
	"remote-control":      CommandRemoteControl,
	"app":                 CommandApp,
	"completion":          CommandCompletion,
	"update":              CommandUpdate,
	"doctor":              CommandDoctor,
	"sandbox":             CommandSandbox,
	"debug":               CommandDebug,
	"execpolicy":          CommandExecpolicy,
	"apply":               CommandApply,
	"a":                   CommandApply,
	"resume":              CommandResume,
	"archive":             CommandArchive,
	"delete":              CommandDelete,
	"unarchive":           CommandUnarchive,
	"fork":                CommandFork,
	"cloud":               CommandCloud,
	"cloud-tasks":         CommandCloud,
	"responses-api-proxy": CommandResponsesAPIProxy,
	"stdio-to-uds":        CommandStdioToUDS,
	"exec-server":         CommandExecServer,
	"features":            CommandFeatures,
	"migrate-rollouts":    CommandMigrateRollouts,
	"migrateRollouts":     CommandMigrateRollouts,
	"migrate-nfs-db":      CommandMigrateNFSDB,
	"migrateNfsDb":        CommandMigrateNFSDB,
	"agents":              CommandAgents,
	"queue":               CommandQueue,
	"tcp-tunnel":          CommandTcpTunnel,
}

type SharedOptions struct {
	Images               []string
	Model                string
	ModelReasoningEffort string
	OSS                  bool
	OSSProvider          string
	Profile              string
	Sandbox              string
	ApprovalPolicy       string
	Search               bool
	NoAltScreen          bool
	// NoDaemon mirrors Rust TuiCli.no_daemon (#46088): run without the shared
	// background server, even if it is already running.
	NoDaemon                             bool
	DangerouslyBypassApprovalsAndSandbox bool
	DangerouslyBypassHookTrust           bool
	// ApproveForMe mirrors Rust SharedOptions::auto_review (#46271): the
	// `--approve-for-me` / `--not-so-yolo` flag selects automatic approval
	// review on top of the workspace-write sandbox.
	ApproveForMe bool
	CWD          string
	AddDirs      []string
	Worktree     bool
}

type RootOptions struct {
	ConfigOverrides []string
	EnableFeatures  []string
	DisableFeatures []string
	Remote          string
	RemoteAuthEnv   string
	StrictConfig    bool
	Shared          SharedOptions
	Prompt          string
}

type ExecOptions struct {
	StrictConfig          bool
	Shared                SharedOptions
	ThreadSource          string
	CyberAccessProgram    string
	SkipGitRepoCheck      bool
	Ephemeral             bool
	IgnoreUserConfig      bool
	IgnoreRules           bool
	OutputSchema          string
	Color                 string
	JSON                  bool
	StreamAssistantDeltas bool
	LastMessageFile       string
	Prompt                string
	ConfigOverrides       []string
	// Help records a `--help`/`-h`/`-V`/`--version` request; the CLI prints the
	// page and exits before any configuration or model work (Rust clap action).
	Help       *ExecHelpRequest
	Subcommand string
	SubArgs    []string
	Review     ReviewOptions
	Resume     ExecResumeOptions
	Fork       ExecForkOptions
}

// ExecHelpRequest is the exec family's clap help/version action.
type ExecHelpRequest struct {
	// Kind selects the page: the exec root or one of its subcommands.
	Kind ExecHelpKind
	// Short selects clap's `-h` summary layout instead of `--help`.
	Short bool
	// Version prints `codex-cli-exec <version>` instead of a help page.
	Version bool
}

// GroupHelpRequest mirrors clap's help action for a group command. Rust
// declares these commands with `arg_required_else_help`, so a bare invocation
// writes the short page to stderr and exits 2, `-h` writes the short page and
// `--help` / `help` write the long page to stdout.
type GroupHelpRequest struct {
	// Kind selects the page of the group command.
	Kind GroupHelpKind
	// Short selects clap's `-h` summary layout instead of `--help`.
	Short bool
	// Required marks the bare invocation that clap answers with help on
	// stderr and exit status 2.
	Required bool
}

type ExecResumeOptions struct {
	SessionID string
	Last      bool
	All       bool
	Prompt    string
}

type ExecForkOptions struct {
	SessionID string
	Images    []string
	Prompt    string
}

type LoginOptions struct {
	WithAPIKey      bool
	WithAccessToken bool
	APIKey          *string
	DeviceAuth      bool
	IssuerBaseURL   string
	ClientID        string
	Action          string
	ConfigOverrides []string
}

type FeatureOptions struct {
	Action  string
	Feature string
	// ConfigOverrides collects clap's global `-c`/`--config`; the feature
	// subcommands accept the flag but never read the values.
	ConfigOverrides []string
}

type MCPOptions struct {
	Action                  string
	Name                    string
	JSON                    bool
	URL                     string
	BearerTokenEnvVar       string
	OAuthClientID           string
	OAuthClientSecret       string
	OAuthResource           string
	OAuthClientRegistration string
	Env                     map[string]string
	Command                 []string
	Scopes                  []string
	ConfigOverrides         []string
	// NoBrowser prints the authorization URL and accepts a pasted callback
	// instead of launching a browser (Rust #44629).
	NoBrowser bool
}

type PluginOptions struct {
	Action          string
	Plugin          string
	MarketplaceName string
	JSON            bool
	Available       bool
	Marketplace     PluginMarketplaceOptions
	ConfigOverrides []string
}

type PluginMarketplaceOptions struct {
	Action          string
	Source          string
	Name            string
	JSON            bool
	RefName         string
	SparsePaths     []string
	ConfigOverrides []string
}

type SandboxOptions struct {
	Setup                 bool
	Elevated              bool
	User                  string
	CurrentUser           bool
	CodexHome             string
	Uninstall             bool
	UninstallHelp         bool
	PermissionProfile     string
	ConfigProfile         string
	CWD                   string
	IncludeManagedConfig  bool
	SandboxStateJSON      string
	SandboxReadableRoots  []string
	SandboxDisableNetwork bool
	AllowUnixSockets      []string
	LogDenials            bool
	ConfigOverrides       []string
	Command               []string

	// setupUserFirst records that `--user` was supplied before
	// `--current-user`, so the nested setup parser names the arguments in
	// clap's order when it reports the conflict.
	setupUserFirst bool
	// setupUserSet records that `--user` was supplied at all: clap's required
	// group and conflicts fire on presence, so `--user=` still counts.
	setupUserSet bool
	// setupEchoOptions records the standalone nested-setup options in argv
	// order; clap echoes them in the usage block of its diagnostics.
	setupEchoOptions []string
	// setupAnyOption records that the nested parser consumed any option, which
	// makes clap drop the [OPTIONS] placeholder from the usage block.
	setupAnyOption bool
}

// SetupUserProvided reports whether `--user` was supplied, including with an
// empty value: clap's required group and `requires` relations fire on presence.
func (o *SandboxOptions) SetupUserProvided() bool {
	return o != nil && o.setupUserSet
}

type ExecpolicyOptions struct {
	Action                 string
	Rules                  []string
	Pretty                 bool
	ResolveHostExecutables bool
	Command                []string
}

type CloudOptions struct {
	Action          string
	Query           string
	Environment     string
	Attempts        int
	Branch          string
	TaskID          string
	Attempt         int
	Limit           int64
	Cursor          string
	JSON            bool
	ConfigOverrides []string
}

type ResponsesAPIProxyOptions struct {
	Port         *uint16
	ServerInfo   string
	HTTPShutdown bool
	UpstreamURL  string
	DumpDir      string
}

type StdioToUDSOptions struct {
	SocketPath string
}

type ExecServerOptions struct {
	StrictConfig bool
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
	Listen          string
	ListenSet       bool
	Remote          string
	Forward         string
	// ForwardBound records the literal `forward` token: clap then requires
	// --remote, --environment-id and --connect on that subcommand.
	ForwardBound     bool
	RemoteSet        bool
	EnvironmentIDSet bool
	ConnectSet       bool
	// AWSFlagSet records any of the --aws-profile/region/service options: clap
	// then requires --aws-sigv4 as well.
	AWSFlagSet           bool
	EnvironmentID        string
	Name                 string
	UseAgentIdentityAuth bool
	ExitOnStdinClose     bool
	// ExecutorCredentialStdin mirrors Rust #54434
	// (cli/src/exec_server_command.rs `--executor-credential-stdin`): read
	// rotating scoped executor credentials from stdin and exit when the pipe
	// closes. It requires --remote and cannot be combined with
	// --use-agent-identity-auth.
	ExecutorCredentialStdin bool
	// ProxyPrivateIPsViaUpstream mirrors Rust #48568
	// (cli/src/exec_server_command.rs `--proxy-private-ips-via-upstream`): the
	// trusted executor-local startup routing that lets permitted private IP
	// destinations use the configured upstream proxy. Destination policy still
	// decides access, and the setting defaults to disabled.
	ProxyPrivateIPsViaUpstream bool
	// RemoteTransport mirrors Rust #42781 `--remote-transport`: "noise" (the
	// default rendezvous transport) or "direct" (plain JSON-RPC over an
	// authenticated WebSocket).
	RemoteTransport    string
	RemoteTransportSet bool
	// AWSSigV4 mirrors Rust #42781 `--aws-sigv4`: sign Direct registration and
	// WebSocket handshake requests with AWS SigV4.
	AWSSigV4      bool
	AWSProfile    string
	AWSRegion     string
	AWSService    string
	AWSServiceSet bool
	// LinuxSandboxPIDNamespace mirrors the global
	// `--linux-sandbox-pid-namespace <MODE>` flag: "isolate" (the default) or
	// "inherit". The Go executor validates the value at parse time; the Linux
	// bubblewrap planner owns the per-platform effect.
	LinuxSandboxPIDNamespace string
	// ConcurrentRequests mirrors `--concurrent-requests <COUNT>`, the
	// RequestDispatchMode clap parses as a NonZeroUsize (1 = inline).
	ConcurrentRequests int
	WebSocketAuthOptions
}

const execServerExitOnStdinCloseEnv = "CODEX_EXEC_SERVER_EXIT_ON_STDIN_CLOSE"

// execServerProxyPrivateIPsViaUpstreamEnv mirrors Rust #48568's clap
// `env = "CODEX_EXEC_SERVER_PROXY_PRIVATE_IPS_VIA_UPSTREAM"`: unset keeps the
// flag/default, a value is parsed as a boolean, and a set-but-unparseable value
// (including the empty string) is rejected like the sibling
// CODEX_EXEC_SERVER_EXIT_ON_STDIN_CLOSE.
const execServerProxyPrivateIPsViaUpstreamEnv = "CODEX_EXEC_SERVER_PROXY_PRIVATE_IPS_VIA_UPSTREAM"

// WebSocketAuthOptions mirrors Rust's shared WebsocketAuthArgs (#47447): the
// `--ws-*` flag family accepted by both `codex app-server` and
// `codex exec-server`.
type WebSocketAuthOptions struct {
	WSAuth                string
	WSAuthSet             bool
	WSTokenFile           string
	WSTokenFileSet        bool
	WSTokenSHA256         string
	WSTokenSHA256Set      bool
	WSSharedSecretFile    string
	WSSharedSecretFileSet bool
	WSIssuer              string
	WSIssuerSet           bool
	WSAudience            string
	WSAudienceSet         bool
	WSMaxClockSkewSeconds *uint64
}

// applyWebSocketAuthFlag parses one `--ws-*` argument. handled reports whether
// the flag belongs to the shared family; next is the index the caller continues
// from.
func applyWebSocketAuthFlag(args []string, index int, arg string, options *WebSocketAuthOptions) (bool, int, error) {
	if options == nil {
		return false, index, nil
	}
	switch {
	case arg == "--ws-auth":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		if err := setWebSocketAuthMode(options, value); err != nil {
			return true, index, err
		}
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-auth="):
		if err := setWebSocketAuthMode(options, strings.TrimPrefix(arg, "--ws-auth=")); err != nil {
			return true, index, err
		}
		return true, index, nil
	case arg == "--ws-token-file":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		options.WSTokenFile = value
		options.WSTokenFileSet = true
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-token-file="):
		value := strings.TrimPrefix(arg, "--ws-token-file=")
		if value == "" {
			return true, index, valueRequiredError("--ws-token-file")
		}
		options.WSTokenFile = value
		options.WSTokenFileSet = true
		return true, index, nil
	case arg == "--ws-token-sha256":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		options.WSTokenSHA256 = value
		options.WSTokenSHA256Set = true
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-token-sha256="):
		options.WSTokenSHA256 = strings.TrimPrefix(arg, "--ws-token-sha256=")
		options.WSTokenSHA256Set = true
		return true, index, nil
	case arg == "--ws-shared-secret-file":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		options.WSSharedSecretFile = value
		options.WSSharedSecretFileSet = true
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-shared-secret-file="):
		value := strings.TrimPrefix(arg, "--ws-shared-secret-file=")
		if value == "" {
			return true, index, valueRequiredError("--ws-shared-secret-file")
		}
		options.WSSharedSecretFile = value
		options.WSSharedSecretFileSet = true
		return true, index, nil
	case arg == "--ws-issuer":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		options.WSIssuer = value
		options.WSIssuerSet = true
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-issuer="):
		options.WSIssuer = strings.TrimPrefix(arg, "--ws-issuer=")
		options.WSIssuerSet = true
		return true, index, nil
	case arg == "--ws-audience":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		options.WSAudience = value
		options.WSAudienceSet = true
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-audience="):
		options.WSAudience = strings.TrimPrefix(arg, "--ws-audience=")
		options.WSAudienceSet = true
		return true, index, nil
	case arg == "--ws-max-clock-skew-seconds":
		value, next, err := requireValue(args, index, arg)
		if err != nil {
			return true, index, err
		}
		parsed, err := parseUint64Flag(arg, value)
		if err != nil {
			return true, index, err
		}
		options.WSMaxClockSkewSeconds = &parsed
		return true, next, nil
	case strings.HasPrefix(arg, "--ws-max-clock-skew-seconds="):
		value := strings.TrimPrefix(arg, "--ws-max-clock-skew-seconds=")
		parsed, err := parseUint64Flag("--ws-max-clock-skew-seconds", value)
		if err != nil {
			return true, index, err
		}
		options.WSMaxClockSkewSeconds = &parsed
		return true, index, nil
	default:
		return false, index, nil
	}
}

type AppServerOptions struct {
	Subcommand []string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
	Daemon          AppServerDaemonOptions
	Proxy           AppServerProxyOptions
	Generate        AppServerGenerateOptions
	StrictConfig    bool
	Listen          string
	Stdio           bool
	RemoteControl   bool
	// PrintStartupConfig validates configuration and emits startup requirements
	// as JSON without starting the server (Rust #55300).
	PrintStartupConfig bool
	// StartupSignOut removes managed credentials before startup discovery.
	StartupSignOut bool
	// StartupClientInfo is the JSON ClientInfo used for startup discovery.
	StartupClientInfo       json.RawMessage
	AnalyticsDefaultEnabled bool
	CodeModeHostURL         string
	// CloudSkillsSnapshot is the host-owned frozen cloud skill snapshot
	// (Rust #55631). It is loaded during app-server startup, so a missing or
	// malformed file fails the process.
	CloudSkillsSnapshot string
	WebSocketAuthOptions
}

type AppServerDaemonOptions struct {
	Action string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
	RemoteControl   bool
	// FromCLI marks `app-server daemon update --from-cli`, which copies and pins
	// this CLI's package (Rust AppServerDaemonSubcommand::Update).
	FromCLI bool
	// Yes confirms the copy without an interactive prompt; it requires FromCLI.
	Yes bool
	// RestoreRelease authorizes one production restore for the internal
	// pid-update-loop launch (Rust PidUpdateLoop::restore_release).
	RestoreRelease string
	// CheckPackageOwnership asks the internal pid-update-loop launch to report
	// whether daemon-owned packages are supported instead of running the loop.
	CheckPackageOwnership bool
	// Help prints the daemon subcommand help (clap's -h/--help). The managed
	// updater probe relies on `pid-update-loop --help` succeeding.
	Help bool
}

type AppServerProxyOptions struct {
	SocketPath string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
}

type AppServerGenerateOptions struct {
	Action string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
	OutDir          string
	Prettier        string
	Experimental    bool
}

type AppOptions struct {
	Path        string
	DownloadURL string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
}

type UpdateOptions struct {
	JSON bool
	// ConfigOverrides collects clap's global `-c`/`--config`; `codex update`
	// accepts the flag but never reads the values.
	ConfigOverrides []string
}

type CompletionOptions struct {
	Shell string
	// ConfigOverrides collects clap's global `-c`/`--config`; `codex
	// completion` accepts the flag but never reads the values.
	ConfigOverrides []string
}

type DoctorOptions struct {
	JSON     bool
	Summary  bool
	All      bool
	NoColor  bool
	ASCII    bool
	Feedback bool
	// NetworkOnly restricts the report to config, network environment,
	// provider HTTP reachability and Responses WebSocket checks (Rust #57606).
	NetworkOnly bool
	// ProbeFilesystemPath is the hidden, internal isolated filesystem probe
	// (Rust #46543): doctor exits with the probe's status before loading
	// configuration.
	ProbeFilesystemPath string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
}

// MigrateNFSDBOptions mirrors Rust cli/src/migrate_nfs_db.rs
// MigrateNfsDbCommand (#57225): `--staging-dir` defaults to the system
// temporary directory.
type MigrateNFSDBOptions struct {
	StagingDir string
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
}

type MigrateRolloutsOptions struct {
	Apply           bool
	Threads         []string
	MaxMibPerSecond uint64
	JSON            bool
	Verbose         bool
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
}

type ApplyOptions struct {
	Patch           string
	ConfigOverrides []string
}

type RemoteControlOptions struct {
	JSON bool
	// ConfigOverrides collects clap's global `-c`/`--config`.
	ConfigOverrides []string
	// NoDaemon mirrors Rust RemoteControlCommand.no_daemon (#50803): run the
	// embedded app-server in the foreground until Ctrl-C instead of the managed
	// daemon. The top-level --no-daemon also sets it, matching Rust cli_main's
	// `remote_control_cli.no_daemon |= interactive.no_daemon`.
	NoDaemon   bool
	Subcommand string
}

type SessionOptions struct {
	Target                string
	Last                  bool
	All                   bool
	IncludeNonInteractive bool
	Force                 bool
	Remote                string
	RemoteAuthEnv         string
	Shared                SharedOptions
	StrictConfig          bool
	ConfigOverrides       []string
	Prompt                string
}

// QueueOptions mirrors Rust cli/src/queue_cmd.rs QueueCommand (#39092):
// submit a text message through thread/queue/add for an existing session.
type QueueOptions struct {
	Thread string
	// ThreadSet records that --thread was supplied, including the empty inline
	// form clap accepts as a present (if empty) value.
	ThreadSet bool
	Message   string
	// Resume mirrors Rust queue_cmd.rs `--resume` (#57232): after queueing,
	// ask the daemon or explicit --remote server to resume an unloaded thread.
	Resume          bool
	Remote          string
	RemoteAuthEnv   string
	Shared          SharedOptions
	StrictConfig    bool
	ConfigOverrides []string
}

// AgentsOptions mirrors Rust cli/src/main.rs codex agents dashboard command
// (#39114): open the shared agents overview against a local or remote
// app server without creating a new session.
type AgentsOptions struct {
	Remote          string
	RemoteAuthEnv   string
	StrictConfig    bool
	ConfigOverrides []string
	Cwd             string
	NoAltScreen     bool
	// NoDaemon is the agents-only spelling Rust declares on AgentsCommand
	// (#58239). It merges into the interactive launch, so the command center
	// runs against an embedded app server instead of the shared one.
	NoDaemon bool
	Shared   SharedOptions
}

type DebugOptions struct {
	Subcommand       string
	AppServerAction  string
	Prompt           string
	Images           []string
	BundledModels    bool
	TraceBundle      string
	TraceOutput      string
	AppServerMessage string
	Args             []string
	// ConfigOverrides collects clap's global `-c`/`--config`; Rust's debug
	// subcommands accept it but never read the values.
	ConfigOverrides []string
}

type ReviewOptions struct {
	Uncommitted bool
	// StrictConfig mirrors ReviewCommand's own `--strict-config` flag: the
	// top-level `codex review` declares it on the subcommand instead of
	// inheriting the root parser's copy.
	StrictConfig bool
	Base         string
	Commit       string
	CommitTitle  string
	Prompt       string
}

type Parsed struct {
	Command           Command
	Root              RootOptions
	Exec              ExecOptions
	Login             LoginOptions
	Features          FeatureOptions
	MCP               MCPOptions
	Plugin            PluginOptions
	Sandbox           SandboxOptions
	Execpolicy        ExecpolicyOptions
	Cloud             CloudOptions
	ResponsesAPIProxy ResponsesAPIProxyOptions
	StdioToUDS        StdioToUDSOptions
	ExecServer        ExecServerOptions
	AppServer         AppServerOptions
	App               AppOptions
	Update            UpdateOptions
	Completion        CompletionOptions
	Doctor            DoctorOptions
	MigrateRollouts   MigrateRolloutsOptions
	MigrateNFSDB      MigrateNFSDBOptions
	Apply             ApplyOptions
	RemoteControl     RemoteControlOptions
	Session           SessionOptions
	Queue             QueueOptions
	Agents            AgentsOptions
	Debug             DebugOptions
	TcpTunnel         TcpTunnelOptions
	GroupHelp         *GroupHelpRequest
	SubcommandHelp    *SubcommandHelpRequest
	RootVersion       bool
	RawSubcommand     []string
}

func Parse(args []string) (*Parsed, error) {
	p := &Parsed{
		Command: CommandInteractive,
		Root: RootOptions{
			Shared: SharedOptions{},
		},
		Exec: ExecOptions{
			Color: "auto",
		},
		AppServer: AppServerOptions{
			Listen: "stdio://",
		},
		App: AppOptions{
			Path: ".",
		},
		Completion: CompletionOptions{
			Shell: "bash",
		},
	}

	rest, promptOnly, err := parseRoot(args, &p.Root)
	if signal, ok := err.(*rootMetaSignal); ok {
		if signal.kind == rootMetaVersion {
			p.RootVersion = true
			return p, nil
		}
		p.SubcommandHelp = &SubcommandHelpRequest{Path: signal.topic, Short: signal.short}
		return p, nil
	}
	if err != nil {
		return nil, decorateRootPendingValueError(err, args)
	}
	if len(rest) == 0 {
		return p, p.Validate()
	}
	if cmd, ok := knownCommands[rest[0]]; ok && !promptOnly {
		p.Command = cmd
		p.RawSubcommand = append([]string(nil), rest...)
		// Rust parses the subcommand first (so clap's own diagnostics win) and
		// only then runs the post-parse gates; Parsed.Validate applies the
		// --strict-config and --worktree rejections in that order.
		if _, err := parseSubcommand(p, rest[1:]); err != nil {
			return nil, decorateSubcommandClapUsage(p.Command, err, rest[1:])
		}
		if err := p.validateExecAutoReview(); err != nil {
			return nil, err
		}
		p.applyExecAutoReviewOverrides()
		p.mergeRootConfigOverrides()
		return p, p.Validate()
	}
	p.Root.Prompt = strings.Join(rest, " ")
	return p, p.Validate()
}

// validateExecAutoReview mirrors clap's conflicts_with_all on Rust's
// `--approve-for-me`: it cannot be combined with an explicit sandbox mode or
// with the approvals-and-sandbox bypass.
func (p *Parsed) validateExecAutoReview() error {
	if p == nil || p.Command != CommandExec || !p.Exec.Shared.ApproveForMe {
		return nil
	}
	if p.Exec.Shared.Sandbox != "" {
		return &UsageError{
			Message: "the argument '--approve-for-me' cannot be used with '--sandbox <SANDBOX_MODE>'",
			Usage:   execUsageText,
		}
	}
	if p.Exec.Shared.DangerouslyBypassApprovalsAndSandbox {
		return &UsageError{
			Message: "the argument '--approve-for-me' cannot be used with '--dangerously-bypass-approvals-and-sandbox'",
			Usage:   execUsageText,
		}
	}
	return nil
}

// applyExecAutoReviewOverrides mirrors Rust's
// SharedOptions::take_auto_review_config_overrides: the flag appends the
// auto-review trio after the user's own overrides, so the flag wins exactly
// like a later clap config override.
func (p *Parsed) applyExecAutoReviewOverrides() {
	if p == nil || p.Command != CommandExec || !p.Exec.Shared.ApproveForMe {
		return
	}
	p.Exec.ConfigOverrides = append(p.Exec.ConfigOverrides,
		`approvals_reviewer="auto_review"`,
		`approval_policy="on-request"`,
		`sandbox_mode="workspace-write"`,
	)
}

// mergeRootConfigOverrides keeps `-c`/`--config` values from every command
// scope in parse order (Rust #56022). Clap's global arguments only keep the
// deepest scope's values, so Rust reparses each scope and concatenates them
// root-first. Commands whose runner also receives RootOptions merge the root
// scope themselves; the nested CLI commands below only see their own options,
// so the root overrides are prepended here. Child options keep their existing
// precedence, matching Rust's unchanged child copies.
func (p *Parsed) mergeRootConfigOverrides() {
	if len(p.Root.ConfigOverrides) == 0 {
		return
	}
	p.MCP.ConfigOverrides = prependConfigOverrides(p.Root.ConfigOverrides, p.MCP.ConfigOverrides)
	p.Plugin.ConfigOverrides = prependConfigOverrides(p.Root.ConfigOverrides, p.Plugin.ConfigOverrides)
	if p.Plugin.Action == "marketplace" {
		p.Plugin.Marketplace.ConfigOverrides = prependConfigOverrides(p.Plugin.ConfigOverrides, p.Plugin.Marketplace.ConfigOverrides)
	}
	p.Cloud.ConfigOverrides = prependConfigOverrides(p.Root.ConfigOverrides, p.Cloud.ConfigOverrides)
}

func prependConfigOverrides(parent, child []string) []string {
	if len(parent) == 0 {
		return child
	}
	merged := make([]string, 0, len(parent)+len(child))
	merged = append(merged, parent...)
	merged = append(merged, child...)
	return merged
}

func parseSubcommand(p *Parsed, args []string) (*Parsed, error) {
	if request := groupHelpRequestFor(p.Command, args); request != nil {
		p.GroupHelp = request
		return p, nil
	}
	// The exec family renders its own pages (and `-V`) from exec_help.go.
	if p.Command != CommandExec {
		if request := SubcommandHelpRequestFor(string(p.Command), args); request != nil {
			if request.UnexpectedValue != nil {
				return nil, &UsageError{
					Message: "unexpected value '" + *request.UnexpectedValue + "' for '--help' found; no more were expected",
					Usage:   subcommandHelpValueUsages[request.Path],
				}
			}
			p.SubcommandHelp = request
			return p, nil
		}
	}
	remaining, err := p.splitSubcommandFeatureFlags(args)
	if err != nil {
		return nil, err
	}
	args = remaining
	args, err = p.splitGroupConfigOverrides(args)
	if err != nil {
		return nil, err
	}
	switch p.Command {
	case CommandExec:
		return p, parseExec(args, &p.Exec)
	case CommandReview:
		p.Command = CommandReview
		p.Exec.Subcommand = "review"
		return p, parseReview(args, &p.Exec.Review, nil, &p.Exec.ConfigOverrides)
	case CommandLogin:
		return p, parseLogin(args, &p.Login)
	case CommandLogout:
		return p, parseLogout(args, &p.Login)
	case CommandFeatures:
		return p, parseFeatures(args, &p.Features)
	case CommandMCP:
		return p, parseMCP(args, &p.MCP)
	case CommandPlugin:
		return p, parsePlugin(args, &p.Plugin)
	case CommandSandbox:
		return p, parseSandbox(args, &p.Sandbox)
	case CommandExecpolicy:
		return p, parseExecpolicy(args, &p.Execpolicy)
	case CommandCloud:
		return p, parseCloud(args, &p.Cloud)
	case CommandResponsesAPIProxy:
		return p, parseResponsesAPIProxy(args, &p.ResponsesAPIProxy)
	case CommandStdioToUDS:
		return p, parseStdioToUDS(args, &p.StdioToUDS)
	case CommandExecServer:
		return p, parseExecServer(args, &p.ExecServer)
	case CommandAppServer:
		return p, parseAppServer(args, &p.AppServer)
	case CommandApp:
		return p, parseApp(args, &p.App)
	case CommandUpdate:
		return p, parseUpdate(args, &p.Update)
	case CommandCompletion:
		return p, parseCompletion(args, &p.Completion)
	case CommandDoctor:
		return p, parseDoctor(args, &p.Doctor)
	case CommandMigrateRollouts:
		return p, parseMigrateRollouts(args, &p.MigrateRollouts)
	case CommandMigrateNFSDB:
		return p, parseMigrateNFSDB(args, &p.MigrateNFSDB)
	case CommandApply:
		return p, parseApply(args, &p.Apply)
	case CommandRemoteControl:
		return p, parseRemoteControl(args, &p.RemoteControl)
	case CommandResume:
		return p, parseSessionSelection(args, &p.Session, true)
	case CommandFork:
		return p, parseSessionSelection(args, &p.Session, false)
	case CommandArchive:
		return p, parseSessionMutation(args, &p.Session, string(CommandArchive), false)
	case CommandUnarchive:
		return p, parseSessionMutation(args, &p.Session, string(CommandUnarchive), false)
	case CommandDelete:
		return p, parseSessionMutation(args, &p.Session, string(CommandDelete), true)
	case CommandQueue:
		return p, parseQueue(args, &p.Queue)
	case CommandAgents:
		return p, parseAgents(args, &p.Agents)
	case CommandTcpTunnel:
		return p, parseTcpTunnel(args, &p.TcpTunnel)
	case CommandDebug:
		return p, parseDebug(args, &p.Debug)
	default:
		p.RawSubcommand = append(p.RawSubcommand[:1:1], args...)
		return p, nil
	}
}

func (p *Parsed) Validate() error {
	// Rust runs reject_unsupported_worktree_for_subcommand before the strict
	// config and remote checks and before exec validates its own positionals.
	if err := p.validateWorktreeSupport(); err != nil {
		return err
	}
	if err := validateTUISharedOptions(p.Root.Shared); err != nil {
		return err
	}
	switch p.Command {
	case CommandResume, CommandFork:
		if err := validateTUISharedOptions(p.Session.Shared); err != nil {
			return err
		}
	case CommandQueue:
		remoteSpecified := strings.TrimSpace(p.Root.Remote) != "" || strings.TrimSpace(p.Queue.Remote) != ""
		if err := validateQueueSharedOptions(p.Root.Shared, remoteSpecified); err != nil {
			return err
		}
	}
	if p.Root.StrictConfig {
		if name := p.unsupportedStrictConfigSubcommandName(); name != "" {
			return fmt.Errorf("`--strict-config` is not supported for `codex %s`", name)
		}
	}
	if p.Root.Remote != "" || p.Root.RemoteAuthEnv != "" {
		if name := p.remoteUnsupportedSubcommandName(); name != "" {
			if p.Root.Remote != "" {
				return fmt.Errorf("`--remote %s` is only supported for interactive TUI commands, not `codex %s`", p.Root.Remote, name)
			}
			return fmt.Errorf("`--remote-auth-token-env` is only supported for interactive TUI commands, not `codex %s`", name)
		}
	}
	return nil
}

// validateWorktreeSupport mirrors reject_unsupported_worktree_for_subcommand
// (codex-rs/cli/src/main.rs): `--worktree` only creates a fresh managed
// worktree, so it is accepted for new interactive sessions, `codex fork <ID>`,
// `codex exec` and `codex exec fork` and rejected everywhere else.
func (p *Parsed) validateWorktreeSupport() error {
	worktree := p.Root.Shared.Worktree
	switch p.Command {
	case CommandExec:
		worktree = worktree || p.Exec.Shared.Worktree
	case CommandResume, CommandFork, CommandArchive, CommandUnarchive, CommandDelete:
		worktree = worktree || p.Session.Shared.Worktree
	case CommandQueue:
		worktree = worktree || p.Queue.Shared.Worktree
	}
	if !worktree {
		return nil
	}
	switch p.Command {
	case CommandInteractive:
		return nil
	case CommandFork:
		if p.Session.Target != "" && !p.Session.Last {
			return nil
		}
		return errors.New("`codex fork --worktree` requires an explicit session ID")
	case CommandExec:
		switch p.Exec.Subcommand {
		case "":
			return nil
		case "fork":
			return nil
		case "resume":
			return errors.New("`--worktree` cannot resume an existing session; use `codex exec fork --worktree`")
		case "review":
			return errors.New("`--worktree` is not supported for code review")
		}
	}
	return errors.New("`--worktree` supports new interactive sessions, `codex fork`, `codex exec`, and `codex exec fork`")
}

func validateTUISharedOptions(shared SharedOptions) error {
	if shared.DangerouslyBypassApprovalsAndSandbox && strings.TrimSpace(shared.ApprovalPolicy) != "" {
		return errors.New("`--dangerously-bypass-approvals-and-sandbox` conflicts with `--ask-for-approval`")
	}
	return nil
}

func (p *Parsed) unsupportedStrictConfigSubcommandName() string {
	switch p.Command {
	case CommandInteractive, CommandExec, CommandReview, CommandExecServer,
		CommandResume, CommandArchive, CommandDelete, CommandUnarchive, CommandFork, CommandDoctor, CommandQueue,
		CommandAgents:
		return ""
	case CommandAppServer:
		if len(p.AppServer.Subcommand) == 0 {
			return ""
		}
		return "app-server " + strings.Join(p.AppServer.Subcommand, " ")
	case "app":
		return "app"
	case CommandRemoteControl:
		return p.remoteControlSubcommandName()
	default:
		if p.Command == "" {
			return ""
		}
		return string(p.Command)
	}
}

// remoteControlSubcommandName mirrors RemoteControlCommand::subcommand_name:
// the bare command or `remote-control <start|stop|pair>`.
func (p *Parsed) remoteControlSubcommandName() string {
	if p.RemoteControl.Subcommand == "" {
		return "remote-control"
	}
	return "remote-control " + p.RemoteControl.Subcommand
}

func (p *Parsed) remoteUnsupportedSubcommandName() string {
	switch p.Command {
	case CommandInteractive, CommandResume, CommandArchive, CommandDelete, CommandUnarchive, CommandFork, CommandQueue, CommandAgents:
		return ""
	case CommandAppServer:
		if len(p.AppServer.Subcommand) == 0 {
			return "app-server"
		}
		return "app-server " + strings.Join(p.AppServer.Subcommand, " ")
	case CommandRemoteControl:
		return p.remoteControlSubcommandName()
	default:
		if p.Command == "" {
			return ""
		}
		return string(p.Command)
	}
}

// rootMetaKind names the root-level actions clap performs while parsing the
// root command, before any subcommand runs.
type rootMetaKind int

const (
	rootMetaHelp rootMetaKind = iota
	rootMetaVersion
)

// rootMetaSignal carries a root-level help/version action out of parseRoot.
// clap fires these as soon as the flag is parsed, so the remaining arguments
// never reach a subcommand.
type rootMetaSignal struct {
	kind  rootMetaKind
	short bool
	topic string
}

func (s *rootMetaSignal) Error() string { return "root meta action" }

// rootHelpTopic resolves clap's `help [TOPIC...]` subcommand: a known path
// prints that page, and an unknown segment is reported against the deepest
// command clap managed to resolve.
func rootHelpTopic(args []string) error {
	path := ""
	index := 0
	for index < len(args) {
		candidate := args[index]
		if path != "" {
			candidate = path + " " + candidate
		}
		if _, ok := subcommandHelpPages[candidate]; !ok {
			break
		}
		path = candidate
		index++
	}
	if index == len(args) {
		return &rootMetaSignal{kind: rootMetaHelp, topic: path}
	}
	usage := rootUsageText
	if path != "" {
		usage = subcommandHelpUsages[path]
	}
	return unrecognizedSubcommandError(args[index], usage)
}

func parseRoot(args []string, root *RootOptions) ([]string, bool, error) {
	var rest []string
	var deferred error
	promptSet := false
	appendPrompt := func(value string) error {
		if promptSet {
			return unexpectedPositionalError(value, rootUsageText)
		}
		promptSet = true
		rest = append(rest, value)
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOptionVariadicImages(args, &i, &root.Shared); err != nil {
			if deferClapValueError(args, &i, &deferred, err) {
				continue
			}
			return nil, false, err
		} else if handled {
			continue
		}
		if handled, err := parseTUIOnlyOption(args, &i, &root.Shared); err != nil {
			if deferClapValueError(args, &i, &deferred, err) {
				continue
			}
			return nil, false, err
		} else if handled {
			continue
		}
		if arg == "--" {
			for _, value := range args[i+1:] {
				if err := appendPrompt(value); err != nil {
					return nil, false, err
				}
			}
			return rest, true, nil
		}
		if !promptSet {
			if _, ok := knownCommands[arg]; ok {
				if deferred != nil {
					return nil, false, deferred
				}
				return append(rest, args[i:]...), false, nil
			}
		}
		switch {
		case arg == "--help":
			return nil, false, &rootMetaSignal{kind: rootMetaHelp}
		case strings.HasPrefix(arg, "--help="):
			return nil, false, &UsageError{
				Message: "unexpected value '" + strings.TrimPrefix(arg, "--help=") + "' for '--help' found; no more were expected",
				Usage:   rootUsageText,
			}
		case arg == "-h" || (len(arg) > 2 && strings.HasPrefix(arg, "-h") && !strings.HasPrefix(arg, "--")):
			return nil, false, &rootMetaSignal{kind: rootMetaHelp, short: true}
		case arg == "--version" || arg == "-V" || (len(arg) > 2 && strings.HasPrefix(arg, "-V") && !strings.HasPrefix(arg, "--")):
			return nil, false, &rootMetaSignal{kind: rootMetaVersion}
		case strings.HasPrefix(arg, "--version="):
			return nil, false, &UsageError{
				Message: "unexpected value '" + strings.TrimPrefix(arg, "--version=") + "' for '--version' found; no more were expected",
				Usage:   rootUsageText,
			}
		case arg == "help" && !promptSet:
			return nil, false, rootHelpTopic(args[i+1:])
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, false, err
			}
			root.ConfigOverrides = append(root.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "-c") && arg != "-C" && arg != "--config":
			root.ConfigOverrides = append(root.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "--config="):
			root.ConfigOverrides = append(root.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case arg == "--enable":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, false, err
			}
			root.EnableFeatures = append(root.EnableFeatures, value)
			i = next
		case strings.HasPrefix(arg, "--enable="):
			root.EnableFeatures = append(root.EnableFeatures, strings.TrimPrefix(arg, "--enable="))
		case arg == "--disable":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, false, err
			}
			root.DisableFeatures = append(root.DisableFeatures, value)
			i = next
		case strings.HasPrefix(arg, "--disable="):
			root.DisableFeatures = append(root.DisableFeatures, strings.TrimPrefix(arg, "--disable="))
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, false, err
			}
			root.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			root.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--remote-auth-token-env":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, false, err
			}
			root.RemoteAuthEnv = value
			i = next
		case strings.HasPrefix(arg, "--remote-auth-token-env="):
			root.RemoteAuthEnv = strings.TrimPrefix(arg, "--remote-auth-token-env=")
		case arg == "--strict-config":
			root.StrictConfig = true
		case strings.HasPrefix(arg, "-"):
			return nil, false, unexpectedRootArgumentError(arg, args[i+1:])
		default:
			if err := appendPrompt(arg); err != nil {
				return nil, false, err
			}
		}
	}
	if deferred != nil {
		return nil, false, deferred
	}
	return rest, false, nil
}

func parseExec(args []string, exec *ExecOptions) error {
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOptionVariadicImages(args, &i, &exec.Shared); err != nil {
			if deferClapValueError(args, &i, &deferred, err) {
				continue
			}
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			// clap still binds one PROMPT after `--` and rejects the rest.
			for _, candidate := range args[i+1:] {
				if exec.Prompt == "" {
					exec.Prompt = candidate
					continue
				}
				return unexpectedPositionalError(candidate, execUsageText)
			}
			return nil
		}
		switch {
		case arg == "-":
			if exec.Prompt != "" {
				return unexpectedPositionalError(arg, execUsageText)
			}
			exec.Prompt = "-"
		case arg == "--help":
			exec.Help = &ExecHelpRequest{Kind: ExecHelpExec}
			return nil
		case arg == "-h":
			exec.Help = &ExecHelpRequest{Kind: ExecHelpExec, Short: true}
			return nil
		case strings.HasPrefix(arg, "--help="):
			return execHelpValueError(arg, execUsageText)
		case len(arg) > 2 && strings.HasPrefix(arg, "-h") && !strings.HasPrefix(arg, "--"):
			exec.Help = &ExecHelpRequest{Kind: ExecHelpExec, Short: true}
			return nil
		case arg == "-V" || arg == "--version":
			exec.Help = &ExecHelpRequest{Version: true}
			return nil
		case arg == "help" && exec.Prompt == "":
			request := &ExecHelpRequest{Kind: ExecHelpExec}
			if i+1 < len(args) {
				kind, ok := ExecHelpKindForSubcommand(args[i+1])
				if !ok {
					return unrecognizedSubcommandError(args[i+1], execUsageText)
				}
				request.Kind = kind
			}
			exec.Help = request
			return nil
		case arg == "resume" && exec.Prompt == "":
			exec.Subcommand = arg
			return parseExecResume(args[i+1:], exec)
		case arg == "fork" && exec.Prompt == "":
			exec.Subcommand = arg
			return parseExecFork(args[i+1:], exec)
		case arg == "review" && exec.Prompt == "":
			exec.Subcommand = arg
			return parseReview(args[i+1:], &exec.Review, exec, &exec.ConfigOverrides)
		case arg == "--strict-config":
			exec.StrictConfig = true
		case arg == "--thread-source":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			exec.ThreadSource = value
			return parseExec(args[next:], exec)
		case strings.HasPrefix(arg, "--thread-source="):
			exec.ThreadSource = strings.TrimPrefix(arg, "--thread-source=")
		case arg == "--cyber-access-program":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := setExecCyberAccessProgram(exec, value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			}
			i = next
		case strings.HasPrefix(arg, "--cyber-access-program="):
			if err := setExecCyberAccessProgram(exec, strings.TrimPrefix(arg, "--cyber-access-program=")); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			}
		case arg == "--skip-git-repo-check":
			exec.SkipGitRepoCheck = true
		case arg == "--ephemeral":
			exec.Ephemeral = true
		case arg == "--ignore-user-config":
			exec.IgnoreUserConfig = true
		case arg == "--ignore-rules":
			exec.IgnoreRules = true
		case arg == "--output-schema":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			exec.OutputSchema = value
			i = next
		case strings.HasPrefix(arg, "--output-schema="):
			value := strings.TrimPrefix(arg, "--output-schema=")
			if value == "" {
				return valueRequiredError("--output-schema")
			}
			exec.OutputSchema = value
		case arg == "--color":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return missingValueError("--color <COLOR>", colorValues)
			}
			if err := setExecColor(exec, value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			}
			i = next
		case strings.HasPrefix(arg, "--color="):
			if err := setExecColor(exec, strings.TrimPrefix(arg, "--color=")); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			}
		case arg == "--json" || arg == "--experimental-json":
			exec.JSON = true
		case arg == "--output-last-message" || arg == "-o":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			exec.LastMessageFile = value
			i = next
		case strings.HasPrefix(arg, "--output-last-message="):
			value := strings.TrimPrefix(arg, "--output-last-message=")
			if value == "" {
				return valueRequiredError("--output-last-message")
			}
			exec.LastMessageFile = value
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			exec.ConfigOverrides = append(exec.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			exec.ConfigOverrides = append(exec.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && arg != "-C":
			exec.ConfigOverrides = append(exec.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return clapUnexpectedLongFlagError("exec", arg, args[:i])
		default:
			// Rust declares a single PROMPT positional, so a second positional
			// is a clap "unexpected argument" diagnostic instead of more prompt
			// text. Flags may still follow the prompt.
			if exec.Prompt != "" {
				return unexpectedPositionalError(arg, execUsageText)
			}
			exec.Prompt = arg
		}
	}
	if deferred != nil {
		return deferred
	}
	return nil
}

func parseExecResume(args []string, exec *ExecOptions) error {
	var positionals []string
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			exec.ConfigOverrides = append(exec.ConfigOverrides, value)
			i = next
			continue
		}
		// ResumeArgsRaw declares --image itself; the other SharedCliOptions
		// entries stay local to `codex exec`, so only the exec globals reach a
		// nested subcommand.
		if arg == "--image" || arg == "-i" {
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--image", "<FILE>", args, i)
			}
			exec.Shared.Images = append(exec.Shared.Images, splitComma(value)...)
			i = next
			continue
		}
		if strings.HasPrefix(arg, "--image=") {
			value := strings.TrimPrefix(arg, "--image=")
			if value == "" {
				return missingValueNamedError("--image", "<FILE>", nil)
			}
			exec.Shared.Images = append(exec.Shared.Images, splitComma(value)...)
			continue
		}
		if handled, err := parseExecGlobalOption(args, &i, exec, &deferred); handled {
			if err != nil {
				return err
			}
			continue
		}
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		switch {
		case arg == "-":
			// Rust's resume prompt is a positional, so `resume --last -` forces
			// stdin the same way the root `exec -` sentinel does.
			positionals = append(positionals, arg)
		case arg == "--help":
			exec.Help = &ExecHelpRequest{Kind: ExecHelpResume}
			return nil
		case arg == "-h":
			exec.Help = &ExecHelpRequest{Kind: ExecHelpResume, Short: true}
			return nil
		case strings.HasPrefix(arg, "--help="):
			return execHelpValueError(arg, execResumeHelpValueUsageText)
		case len(arg) > 2 && strings.HasPrefix(arg, "-h") && !strings.HasPrefix(arg, "--"):
			exec.Help = &ExecHelpRequest{Kind: ExecHelpResume, Short: true}
			return nil
		case arg == "-V" || arg == "--version":
			exec.Help = &ExecHelpRequest{Version: true}
			return nil
		case arg == "--last":
			exec.Resume.Last = true
		case arg == "--all":
			exec.Resume.All = true
		case strings.HasPrefix(arg, "-"):
			return clapUnexpectedLongFlagError("exec resume", arg, args[:i])
		default:
			positionals = append(positionals, arg)
		}
	}
	if deferred != nil {
		return deferred
	}
	// Rust's ResumeArgsRaw binds SESSION_ID then PROMPT and lets clap reject a
	// third positional, while `From<ResumeArgsRaw>` turns the single positional
	// of `--last` into the prompt.
	if exec.Resume.Last && len(positionals) == 1 {
		exec.Resume.Prompt = positionals[0]
		return nil
	}
	if len(positionals) > 2 {
		return unexpectedPositionalError(positionals[2], execResumeUsageText)
	}
	if len(positionals) >= 1 {
		exec.Resume.SessionID = positionals[0]
	}
	if len(positionals) == 2 {
		exec.Resume.Prompt = positionals[1]
	}
	return nil
}

func parseExecFork(args []string, exec *ExecOptions) error {
	var positionals []string
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			exec.ConfigOverrides = append(exec.ConfigOverrides, value)
			i = next
			continue
		}
		// ForkArgs declares --image itself; the other SharedCliOptions entries
		// stay local to `codex exec`, so only the exec globals reach a nested
		// subcommand.
		if arg == "--image" || arg == "-i" {
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--image", "<FILE>", args, i)
			}
			exec.Fork.Images = append(exec.Fork.Images, splitComma(value)...)
			i = next
			continue
		}
		if strings.HasPrefix(arg, "--image=") {
			value := strings.TrimPrefix(arg, "--image=")
			if value == "" {
				return missingValueNamedError("--image", "<FILE>", nil)
			}
			exec.Fork.Images = append(exec.Fork.Images, splitComma(value)...)
			continue
		}
		if handled, err := parseExecGlobalOption(args, &i, exec, &deferred); handled {
			if err != nil {
				return err
			}
			continue
		}
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		switch {
		case arg == "-":
			positionals = append(positionals, arg)
		case arg == "--help":
			exec.Help = &ExecHelpRequest{Kind: ExecHelpFork}
			return nil
		case arg == "-h":
			exec.Help = &ExecHelpRequest{Kind: ExecHelpFork, Short: true}
			return nil
		case strings.HasPrefix(arg, "--help="):
			return execHelpValueError(arg, execForkHelpValueUsageText)
		case len(arg) > 2 && strings.HasPrefix(arg, "-h") && !strings.HasPrefix(arg, "--"):
			exec.Help = &ExecHelpRequest{Kind: ExecHelpFork, Short: true}
			return nil
		case arg == "-V" || arg == "--version":
			exec.Help = &ExecHelpRequest{Version: true}
			return nil
		case strings.HasPrefix(arg, "-"):
			return clapUnexpectedLongFlagError("exec fork", arg, args[:i])
		default:
			positionals = append(positionals, arg)
		}
	}
	if deferred != nil {
		return deferred
	}
	if len(positionals) == 0 {
		return missingRequiredArgumentsError("exec fork", "<SESSION_ID>")
	}
	if len(positionals) > 2 {
		return unexpectedPositionalError(positionals[2], execForkUsageText)
	}
	exec.Fork.SessionID = positionals[0]
	if len(positionals) == 2 {
		exec.Fork.Prompt = positionals[1]
	}
	return nil
}

func parseLogin(args []string, login *LoginOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--with-api-key":
			login.WithAPIKey = true
		case arg == "--with-access-token":
			login.WithAccessToken = true
		case arg == "--api-key":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				value := args[i+1]
				login.APIKey = &value
				i++
			} else {
				value := ""
				login.APIKey = &value
			}
		case strings.HasPrefix(arg, "--api-key="):
			value := strings.TrimPrefix(arg, "--api-key=")
			login.APIKey = &value
		case arg == "--device-auth":
			login.DeviceAuth = true
		case arg == "--experimental_issuer":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			login.IssuerBaseURL = value
			i = next
		case strings.HasPrefix(arg, "--experimental_issuer="):
			login.IssuerBaseURL = strings.TrimPrefix(arg, "--experimental_issuer=")
		case arg == "--experimental_client-id":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			login.ClientID = value
			i = next
		case strings.HasPrefix(arg, "--experimental_client-id="):
			login.ClientID = strings.TrimPrefix(arg, "--experimental_client-id=")
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			login.ConfigOverrides = append(login.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			login.ConfigOverrides = append(login.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			login.ConfigOverrides = append(login.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case arg == "status":
			login.Action = "status"
		case strings.HasPrefix(arg, "-"):
			if login.Action == "status" {
				return unexpectedSubcommandArgumentError("login status", arg)
			}
			return unexpectedSubcommandArgumentError("login", arg)
		default:
			if login.Action == "status" {
				return unexpectedSubcommandArgumentError("login status", arg)
			}
			return unrecognizedSubcommandWithSuggestion(arg, subcommandUsageTexts["login"].usage, subcommandCandidates["login"])
		}
	}
	return nil
}

func parseReview(args []string, review *ReviewOptions, exec *ExecOptions, overrides *[]string) error {
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			if overrides != nil {
				*overrides = append(*overrides, value)
			}
			i = next
			continue
		}
		if exec != nil {
			// `codex exec` marks model, the dangerous bypasses and worktree global
			// (mark_exec_global_args) and declares the rest of its options with
			// clap's `global = true`, so they stay valid after `review`.
			if handled, err := parseExecGlobalOption(args, &i, exec, &deferred); handled {
				if err != nil {
					return err
				}
				continue
			}
		}
		switch {
		case exec != nil && (arg == "--help" || arg == "-h"):
			// `codex exec review --help` prints the exec review page; the
			// top-level `codex review` command has its own surface.
			exec.Help = &ExecHelpRequest{Kind: ExecHelpReview, Short: arg == "-h"}
			return nil
		case exec != nil && strings.HasPrefix(arg, "--help="):
			return execHelpValueError(arg, execReviewHelpValueUsageText)
		case exec != nil && len(arg) > 2 && strings.HasPrefix(arg, "-h") && !strings.HasPrefix(arg, "--"):
			exec.Help = &ExecHelpRequest{Kind: ExecHelpReview, Short: true}
			return nil
		case exec != nil && (arg == "-V" || arg == "--version"):
			exec.Help = &ExecHelpRequest{Version: true}
			return nil
		case arg == "--strict-config":
			// ReviewCommand declares --strict-config itself; inside `codex exec
			// review` the exec Cli marks the same flag global.
			review.StrictConfig = true
		case arg == "--uncommitted":
			if review.Base != "" || review.Commit != "" || review.Prompt != "" {
				return errors.New("--uncommitted conflicts with --base, --commit, and PROMPT")
			}
			review.Uncommitted = true
		case arg == "--base":
			if review.Uncommitted || review.Commit != "" || review.Prompt != "" {
				return errors.New("--base conflicts with --uncommitted, --commit, and PROMPT")
			}
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			review.Base = value
			i = next
		case strings.HasPrefix(arg, "--base="):
			if review.Uncommitted || review.Commit != "" || review.Prompt != "" {
				return errors.New("--base conflicts with --uncommitted, --commit, and PROMPT")
			}
			review.Base = strings.TrimPrefix(arg, "--base=")
		case arg == "--commit":
			if review.Uncommitted || review.Base != "" || review.Prompt != "" {
				return errors.New("--commit conflicts with --uncommitted, --base, and PROMPT")
			}
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			review.Commit = value
			i = next
		case strings.HasPrefix(arg, "--commit="):
			if review.Uncommitted || review.Base != "" || review.Prompt != "" {
				return errors.New("--commit conflicts with --uncommitted, --base, and PROMPT")
			}
			review.Commit = strings.TrimPrefix(arg, "--commit=")
		case arg == "--title":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			review.CommitTitle = value
			i = next
		case strings.HasPrefix(arg, "--title="):
			review.CommitTitle = strings.TrimPrefix(arg, "--title=")
		case arg == "-":
			if review.Uncommitted || review.Base != "" || review.Commit != "" {
				return errors.New("PROMPT conflicts with --uncommitted, --base, and --commit")
			}
			review.Prompt = "-"
			return nil
		case strings.HasPrefix(arg, "-"):
			if exec != nil {
				return clapUnexpectedLongFlagError("exec review", arg, args[:i])
			}
			return clapUnexpectedLongFlagError("review", arg, args[:i])
		default:
			if review.Uncommitted || review.Base != "" || review.Commit != "" {
				return errors.New("PROMPT conflicts with --uncommitted, --base, and --commit")
			}
			// Rust's review takes a single PROMPT positional.
			if review.Prompt != "" {
				if exec != nil {
					return unexpectedPositionalError(arg, execReviewUsageText)
				}
				return fmt.Errorf("unexpected argument '%s' found", arg)
			}
			review.Prompt = arg
		}
	}
	if deferred != nil {
		return deferred
	}
	if review.CommitTitle != "" && review.Commit == "" {
		return errors.New("--title requires --commit")
	}
	return nil
}

// isExecGlobalSharedArg lists the exec `SharedCliOptions` entries Rust marks
// global (mark_exec_global_args): model, the two dangerous bypasses and
// worktree.
func isExecGlobalSharedArg(arg string) bool {
	switch {
	case arg == "--worktree":
		return true
	case arg == "--model" || arg == "-m" || strings.HasPrefix(arg, "--model="):
		return true
	case strings.HasPrefix(arg, "-m") && arg != "-m":
		return true
	case arg == "--dangerously-bypass-approvals-and-sandbox":
		return true
	case arg == "--dangerously-bypass-hook-trust":
		return true
	}
	return false
}

// parseExecGlobalOption handles the options clap marks global on `codex exec`:
// the Cli-level `global = true` arguments plus mark_exec_global_args. clap
// accepts them after a nested subcommand, so `codex exec review --json` and
// `codex exec fork --thread-source x` parse. The exec-local shared options such
// as --sandbox or --profile stay local to `codex exec` itself, which is why the
// nested parsers no longer flatten the whole SharedCliOptions table.
func parseExecGlobalOption(args []string, i *int, exec *ExecOptions, deferred *error) (bool, error) {
	arg := args[*i]
	switch {
	case arg == "--strict-config":
		exec.StrictConfig = true
	case arg == "--thread-source":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return true, err
		}
		exec.ThreadSource = value
		*i = next
	case strings.HasPrefix(arg, "--thread-source="):
		exec.ThreadSource = strings.TrimPrefix(arg, "--thread-source=")
	case arg == "--cyber-access-program":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return true, err
		}
		if err := setExecCyberAccessProgram(exec, value); err != nil {
			if !deferClapValueError(args, i, deferred, err) {
				return true, err
			}
		}
		*i = next
	case strings.HasPrefix(arg, "--cyber-access-program="):
		if err := setExecCyberAccessProgram(exec, strings.TrimPrefix(arg, "--cyber-access-program=")); err != nil {
			if !deferClapValueError(args, i, deferred, err) {
				return true, err
			}
		}
	case arg == "--skip-git-repo-check":
		exec.SkipGitRepoCheck = true
	case arg == "--ephemeral":
		exec.Ephemeral = true
	case arg == "--ignore-user-config":
		exec.IgnoreUserConfig = true
	case arg == "--ignore-rules":
		exec.IgnoreRules = true
	case arg == "--output-schema":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return true, err
		}
		exec.OutputSchema = value
		*i = next
	case strings.HasPrefix(arg, "--output-schema="):
		value := strings.TrimPrefix(arg, "--output-schema=")
		if value == "" {
			return true, valueRequiredError("--output-schema")
		}
		exec.OutputSchema = value
	case arg == "--json" || arg == "--experimental-json":
		exec.JSON = true
	case arg == "--output-last-message" || arg == "-o":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return true, err
		}
		exec.LastMessageFile = value
		*i = next
	case strings.HasPrefix(arg, "--output-last-message="):
		value := strings.TrimPrefix(arg, "--output-last-message=")
		if value == "" {
			return true, valueRequiredError("--output-last-message")
		}
		exec.LastMessageFile = value
	case isExecGlobalSharedArg(arg):
		handled, err := parseSharedOption(args, i, &exec.Shared)
		if err != nil {
			return true, err
		}
		return handled, nil
	default:
		return false, nil
	}
	return true, nil
}

// deferClapValueError records a clap value-parser failure and keeps scanning
// argv. clap matches every token before it validates option values, so an
// undeclared flag later in argv wins with the unknown-argument diagnostic; the
// recorded error is reported once the scan finishes. It reports whether err is
// such a value diagnostic, in which case the value token is skipped.
func deferClapValueError(args []string, i *int, deferred *error, err error) bool {
	usageErr, ok := err.(*UsageError)
	if !ok {
		return false
	}
	// Raw diagnostics and clap's empty-value diagnostic are value-parser
	// errors too: they defer so an undeclared flag later in argv still wins.
	missingValue := strings.HasPrefix(usageErr.Message, "a value is required for '")
	if !usageErr.Raw && !missingValue && !strings.HasPrefix(usageErr.Message, "invalid value '") {
		return false
	}
	// An inline `--flag=value` is one token that clap validates while it
	// matches, so both the empty-value and the value-parser diagnostics report
	// before the unknown-argument pass. Only the space form defers.
	if *i < len(args) && strings.Contains(args[*i], "=") {
		return false
	}
	if *deferred == nil {
		*deferred = err
	}
	if *i < len(args) && !strings.Contains(args[*i], "=") && *i+1 < len(args) {
		*i++
	}
	return true
}

func parseLogout(args []string, login *LoginOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			login.ConfigOverrides = append(login.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			login.ConfigOverrides = append(login.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			login.ConfigOverrides = append(login.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		default:
			return unexpectedSubcommandArgumentError("logout", arg)
		}
	}
	return nil
}

func parseFeatures(args []string, features *FeatureOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpFeatures)
	}
	features.Action = args[0]
	switch features.Action {
	case "list":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				features.ConfigOverrides = append(features.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				features.ConfigOverrides = append(features.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				features.ConfigOverrides = append(features.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			default:
				return unexpectedSubcommandArgumentError("features list", arg)
			}
		}
	case "enable", "disable":
		command := "features " + features.Action
		bound := false
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if arg == "-c" || arg == "--config" {
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				features.ConfigOverrides = append(features.ConfigOverrides, value)
				i = next
				continue
			}
			if strings.HasPrefix(arg, "--config=") {
				features.ConfigOverrides = append(features.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
				continue
			}
			// clap rejects an unknown flag before it counts the positional, so
			// the flag diagnostic wins over the "too many values" one.
			if arg != "-" && strings.HasPrefix(arg, "-") {
				return clapUnexpectedLongFlagError(command, arg, args[1:i])
			}
			if bound {
				return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts[command].usage)
			}
			features.Feature = arg
			bound = true
		}
		if !bound {
			return missingRequiredArgumentsError(command, "<FEATURE>")
		}
	default:
		return unrecognizedSubcommandForGroup("features", features.Action)
	}
	return nil
}

// mcpAddFlagSeen reports whether a value flag occurs before the `--`
// terminator, mirroring clap's presence checks: `mcp add` treats an empty value
// as present even though Go stores it as the zero value.
func mcpAddFlagSeen(args []string, flag string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

// mcpAddRequiresError reproduces clap's `requires` diagnostics for the
// streamable HTTP options. A relation that leaves --url as the only missing
// argument is reported against the root command with the bare argument id,
// while `requires_all` violations keep the `mcp add` usage block.
func mcpAddRequiresError(args []string, mcp *MCPOptions) error {
	clientIDSeen := mcpAddFlagSeen(args, "--oauth-client-id")
	secretSeen := mcpAddFlagSeen(args, "--oauth-client-secret")
	urlMissing := mcp.URL == "" && (clientIDSeen || secretSeen ||
		mcpAddFlagSeen(args, "--oauth-resource") ||
		mcpAddFlagSeen(args, "--oauth-client-registration") ||
		mcpAddFlagSeen(args, "--bearer-token-env-var"))
	clientIDMissing := secretSeen && !clientIDSeen
	switch {
	case urlMissing && clientIDMissing:
		return &UsageError{
			Message: requiredArgumentsMessage("--url <URL>", "--oauth-client-id <CLIENT_ID>"),
			Usage:   subcommandUsageTexts["mcp add"].usage,
		}
	case urlMissing:
		return &UsageError{
			Message: "the following required argument was not provided: url",
			Usage:   rootUsageText,
		}
	case clientIDMissing:
		return &UsageError{
			Message: requiredArgumentsMessage("--oauth-client-id <CLIENT_ID>"),
			Usage:   subcommandUsageTexts["mcp add"].usage,
		}
	}
	if secretSeen && strings.TrimSpace(mcp.OAuthClientSecret) == "" {
		return errors.New("--oauth-client-secret must not be empty")
	}
	return nil
}

func parseMCP(args []string, mcp *MCPOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpMCP)
	}
	mcp.Action = args[0]
	if mcp.Env == nil {
		mcp.Env = map[string]string{}
	}
	switch mcp.Action {
	case "list":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				mcp.JSON = true
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("mcp list", arg)
			default:
				return unexpectedSubcommandArgumentError("mcp list", arg)
			}
		}
	case "get":
		// clap stops option parsing at `--`, so every later token is a
		// positional: `mcp get -- --json` names the server `--json`.
		afterSeparator := false
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case !afterSeparator && arg == "--":
				afterSeparator = true
			case !afterSeparator && arg == "--json":
				mcp.JSON = true
			case !afterSeparator && (arg == "-c" || arg == "--config"):
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, value)
				i = next
			case !afterSeparator && strings.HasPrefix(arg, "--config="):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case !afterSeparator && strings.HasPrefix(arg, "-c"):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case !afterSeparator && strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("mcp get", arg)
			default:
				if mcp.Name != "" {
					return unexpectedPositionalError(arg, subcommandUsageTexts["mcp get"].usage)
				}
				mcp.Name = arg
			}
		}
		if mcp.Name == "" {
			return missingRequiredArgumentsError("mcp get", "<NAME>")
		}
	case "add":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--":
				rest := args[i+1:]
				if len(rest) > 0 && mcp.Name == "" {
					mcp.Name = rest[0]
					rest = rest[1:]
				}
				mcp.Command = append(mcp.Command, rest...)
				i = len(args)
			case arg == "--url":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.URL = value
				i = next
			case strings.HasPrefix(arg, "--url="):
				mcp.URL = strings.TrimPrefix(arg, "--url=")
			case arg == "--env":
				if i+1 >= len(args) {
					return &UsageError{Message: "a value is required for '--env <KEY=VALUE>' but none was supplied"}
				}
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				key, envValue, err := parseKeyValueFlag(value, "--env")
				if err != nil {
					return err
				}
				mcp.Env[key] = envValue
				i = next
			case strings.HasPrefix(arg, "--env="):
				key, envValue, err := parseKeyValueFlag(strings.TrimPrefix(arg, "--env="), "--env")
				if err != nil {
					return err
				}
				mcp.Env[key] = envValue
			case arg == "--bearer-token-env-var":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.BearerTokenEnvVar = value
				i = next
			case strings.HasPrefix(arg, "--bearer-token-env-var="):
				mcp.BearerTokenEnvVar = strings.TrimPrefix(arg, "--bearer-token-env-var=")
			case arg == "--oauth-client-id":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.OAuthClientID = value
				i = next
			case strings.HasPrefix(arg, "--oauth-client-id="):
				mcp.OAuthClientID = strings.TrimPrefix(arg, "--oauth-client-id=")
			case arg == "--oauth-client-secret":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.OAuthClientSecret = value
				i = next
			case strings.HasPrefix(arg, "--oauth-client-secret="):
				mcp.OAuthClientSecret = strings.TrimPrefix(arg, "--oauth-client-secret=")
			case arg == "--oauth-resource":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.OAuthResource = value
				i = next
			case strings.HasPrefix(arg, "--oauth-resource="):
				mcp.OAuthResource = strings.TrimPrefix(arg, "--oauth-resource=")
			case arg == "--oauth-client-registration":
				if i+1 >= len(args) || clapLooksLikeOption(args[i+1]) {
					return missingValueError(mcpOAuthClientRegistrationFlag, mcpOAuthClientRegistrationValues)
				}
				if err := validateMCPOAuthClientRegistration(args[i+1]); err != nil {
					return err
				}
				mcp.OAuthClientRegistration = args[i+1]
				i++
			case strings.HasPrefix(arg, "--oauth-client-registration="):
				value := strings.TrimPrefix(arg, "--oauth-client-registration=")
				if err := validateMCPOAuthClientRegistration(value); err != nil {
					return err
				}
				mcp.OAuthClientRegistration = value
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-") && arg != "-":
				return unexpectedSubcommandArgumentError("mcp add", arg)
			default:
				if mcp.Name == "" {
					mcp.Name = arg
					continue
				}
				mcp.Command = append(mcp.Command, args[i:]...)
				i = len(args)
			}
		}
		if mcp.Name == "" || (mcp.URL == "" && len(mcp.Command) == 0) {
			var missing []string
			if mcp.URL == "" && len(mcp.Command) == 0 {
				missing = append(missing, "<COMMAND|--url <URL>>")
			}
			if mcp.Name == "" {
				missing = append(missing, "<NAME>")
			}
			return missingRequiredArgumentsError("mcp add", missing...)
		}
		if err := mcpAddRequiresError(args, mcp); err != nil {
			return err
		}
		if mcp.URL != "" && len(mcp.Command) > 0 {
			return &UsageError{
				Message: "the argument '--url <URL>' cannot be used with '[COMMAND]...'",
				Usage:   subcommandUsageTexts["mcp add"].usage,
			}
		}
		// `--env` belongs to the stdio argument group, so clap selects that
		// transport even when --url is present and then finds no command.
		if mcp.URL != "" && len(mcp.Env) > 0 {
			return errors.New("command is required")
		}
	case "remove", "logout":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("mcp "+mcp.Action, arg)
			default:
				if mcp.Name != "" {
					return fmt.Errorf("mcp %s accepts exactly one NAME", mcp.Action)
				}
				mcp.Name = arg
			}
		}
		if mcp.Name == "" {
			return missingRequiredArgumentsError("mcp "+mcp.Action, "<NAME>")
		}
	case "login":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--scopes":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.Scopes = append(mcp.Scopes, splitComma(value)...)
				i = next
			case strings.HasPrefix(arg, "--scopes="):
				mcp.Scopes = append(mcp.Scopes, splitComma(strings.TrimPrefix(arg, "--scopes="))...)
			case arg == "--no-browser":
				mcp.NoBrowser = true
			case arg == "--oauth-client-registration":
				if i+1 >= len(args) || clapLooksLikeOption(args[i+1]) {
					return missingValueError(mcpOAuthClientRegistrationFlag, mcpOAuthClientRegistrationValues)
				}
				if err := validateMCPOAuthClientRegistration(args[i+1]); err != nil {
					return err
				}
				mcp.OAuthClientRegistration = args[i+1]
				i++
			case strings.HasPrefix(arg, "--oauth-client-registration="):
				value := strings.TrimPrefix(arg, "--oauth-client-registration=")
				if err := validateMCPOAuthClientRegistration(value); err != nil {
					return err
				}
				mcp.OAuthClientRegistration = value
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				mcp.ConfigOverrides = append(mcp.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("mcp login", arg)
			default:
				if mcp.Name != "" {
					return errors.New("mcp login accepts exactly one NAME")
				}
				mcp.Name = arg
			}
		}
		if mcp.Name == "" {
			return missingRequiredArgumentsError("mcp login", "<NAME>")
		}
	default:
		return unrecognizedSubcommandForGroup("mcp", mcp.Action)
	}
	return nil
}

func parsePlugin(args []string, plugin *PluginOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpPlugin)
	}
	plugin.Action = args[0]
	switch plugin.Action {
	case "add", "remove":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				plugin.JSON = true
			case arg == "--marketplace" || arg == "-m":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				plugin.MarketplaceName = value
				i = next
			case strings.HasPrefix(arg, "--marketplace="):
				plugin.MarketplaceName = strings.TrimPrefix(arg, "--marketplace=")
			case strings.HasPrefix(arg, "-m") && arg != "-m":
				plugin.MarketplaceName = strings.TrimPrefix(arg, "-m")
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				plugin.ConfigOverrides = append(plugin.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				plugin.ConfigOverrides = append(plugin.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				plugin.ConfigOverrides = append(plugin.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return clapUnexpectedLongFlagError("plugin "+plugin.Action, arg, args[1:i])
			default:
				if plugin.Plugin != "" {
					return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts["plugin "+plugin.Action].usage)
				}
				plugin.Plugin = arg
			}
		}
		if plugin.Plugin == "" {
			return missingRequiredArgumentsError("plugin "+plugin.Action, "<PLUGIN[@MARKETPLACE]>")
		}
	case "list":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				plugin.JSON = true
			case arg == "--available":
				plugin.Available = true
			case arg == "--marketplace" || arg == "-m":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				plugin.MarketplaceName = value
				i = next
			case strings.HasPrefix(arg, "--marketplace="):
				plugin.MarketplaceName = strings.TrimPrefix(arg, "--marketplace=")
			case strings.HasPrefix(arg, "-m") && arg != "-m":
				plugin.MarketplaceName = strings.TrimPrefix(arg, "-m")
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				plugin.ConfigOverrides = append(plugin.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				plugin.ConfigOverrides = append(plugin.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				plugin.ConfigOverrides = append(plugin.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("plugin list", arg)
			default:
				return unexpectedSubcommandArgumentError("plugin list", arg)
			}
		}
		if plugin.Available && !plugin.JSON {
			return errors.New("plugin list --available requires --json")
		}
	case "marketplace":
		return parsePluginMarketplace(args[1:], &plugin.Marketplace)
	default:
		return unrecognizedSubcommandForGroup("plugin", plugin.Action)
	}
	return nil
}

func parsePluginMarketplace(args []string, marketplace *PluginMarketplaceOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpPluginMarketplace)
	}
	marketplace.Action = args[0]
	switch marketplace.Action {
	case "add":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				marketplace.JSON = true
			case arg == "--ref":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				marketplace.RefName = value
				i = next
			case strings.HasPrefix(arg, "--ref="):
				marketplace.RefName = strings.TrimPrefix(arg, "--ref=")
			case arg == "--sparse":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				marketplace.SparsePaths = append(marketplace.SparsePaths, value)
				i = next
			case strings.HasPrefix(arg, "--sparse="):
				marketplace.SparsePaths = append(marketplace.SparsePaths, strings.TrimPrefix(arg, "--sparse="))
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("plugin marketplace add", arg)
			default:
				if marketplace.Source != "" {
					return errors.New("plugin marketplace add accepts exactly one SOURCE")
				}
				marketplace.Source = arg
			}
		}
		if marketplace.Source == "" {
			return missingRequiredArgumentsError("plugin marketplace add", "<SOURCE>")
		}
	case "list":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				marketplace.JSON = true
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("plugin marketplace list", arg)
			default:
				return unexpectedSubcommandArgumentError("plugin marketplace list", arg)
			}
		}
	case "upgrade":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				marketplace.JSON = true
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("plugin marketplace upgrade", arg)
			default:
				if marketplace.Name != "" {
					return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts["plugin marketplace upgrade"].usage)
				}
				marketplace.Name = arg
			}
		}
	case "remove":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--json":
				marketplace.JSON = true
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				marketplace.ConfigOverrides = append(marketplace.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("plugin marketplace remove", arg)
			default:
				if marketplace.Name != "" {
					return errors.New("plugin marketplace remove accepts exactly one MARKETPLACE_NAME")
				}
				marketplace.Name = arg
			}
		}
		if marketplace.Name == "" {
			return missingRequiredArgumentsError("plugin marketplace remove", "<MARKETPLACE_NAME>")
		}
	default:
		return unrecognizedSubcommandForGroup("plugin marketplace", marketplace.Action)
	}
	return nil
}

func parseSandbox(args []string, sandbox *SandboxOptions) error {
	// Rust #50437: `codex sandbox uninstall` is a clap subcommand, so it is
	// recognized only before a command separator; commands after `--` (for
	// example `codex sandbox -- uninstall --help`) are still forwarded to the
	// sandbox as the command to run.
	if len(args) > 0 && args[0] == "uninstall" {
		sandbox.Uninstall = true
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
				if err != nil {
					return err
				}
				sandbox.ConfigOverrides = append(sandbox.ConfigOverrides, value)
				i = next
				continue
			}
			switch {
			case arg == "-h" || arg == "--help":
				sandbox.UninstallHelp = true
			case strings.HasPrefix(arg, "-"):
				return clapUnexpectedLongFlagError("sandbox uninstall", arg, args[1:i])
			default:
				return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts["sandbox uninstall"].usage)
			}
		}
		return nil
	}
	if len(args) > 0 && args[0] == "setup" {
		sandbox.Setup = true
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--elevated":
				sandbox.Elevated = true
				sandbox.setupEchoOptions = append(sandbox.setupEchoOptions, "--elevated")
				sandbox.setupAnyOption = true
			case arg == "--current-user":
				if !sandbox.setupUserSet {
					sandbox.setupUserFirst = false
				}
				sandbox.CurrentUser = true
				sandbox.setupAnyOption = true
			case arg == "--user":
				if i+1 >= len(args) {
					return sandboxSetupMissingValueError("--user <USER>")
				}
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				sandbox.User = value
				sandbox.setupUserSet = true
				sandbox.setupAnyOption = true
				if !sandbox.CurrentUser {
					sandbox.setupUserFirst = true
				}
				i = next
			case strings.HasPrefix(arg, "--user="):
				sandbox.User = strings.TrimPrefix(arg, "--user=")
				sandbox.setupUserSet = true
				sandbox.setupAnyOption = true
				if !sandbox.CurrentUser {
					sandbox.setupUserFirst = true
				}
			case arg == "--codex-home":
				if i+1 >= len(args) {
					return sandboxSetupMissingValueError("--codex-home <DIR>")
				}
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				if value == "" {
					return sandboxSetupMissingValueError("--codex-home <DIR>")
				}
				sandbox.CodexHome = value
				sandbox.setupEchoOptions = append(sandbox.setupEchoOptions, "--codex-home <DIR>")
				sandbox.setupAnyOption = true
				i = next
			case strings.HasPrefix(arg, "--codex-home="):
				value := strings.TrimPrefix(arg, "--codex-home=")
				if value == "" {
					return sandboxSetupMissingValueError("--codex-home <DIR>")
				}
				sandbox.CodexHome = value
				sandbox.setupEchoOptions = append(sandbox.setupEchoOptions, "--codex-home <DIR>")
				sandbox.setupAnyOption = true
			default:
				return sandboxSetupArgumentError(sandbox, arg)
			}
		}
		return validateSandboxSetupParseDependencies(sandbox)
	}
	sandboxStateJSONSet := false
	validate := func() error {
		return validateSandboxOptionDependencies(sandbox, sandboxStateJSONSet)
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			sandbox.Command = append(sandbox.Command, args[i+1:]...)
			return validate()
		case arg == "--permission-profile" || arg == "--permissions-profile" || arg == "-P":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--permission-profile", "<NAME>", args, i)
			}
			sandbox.PermissionProfile = value
			i = next
		case strings.HasPrefix(arg, "--permission-profile="):
			sandbox.PermissionProfile = strings.TrimPrefix(arg, "--permission-profile=")
		case strings.HasPrefix(arg, "--permissions-profile="):
			sandbox.PermissionProfile = strings.TrimPrefix(arg, "--permissions-profile=")
		case strings.HasPrefix(arg, "-P") && arg != "-P":
			sandbox.PermissionProfile = strings.TrimPrefix(arg, "-P")
		case arg == "--profile" || arg == "-p":
			if i+1 >= len(args) {
				return &UsageError{Message: "a value is required for '--profile <CONFIG_PROFILE>' but none was supplied"}
			}
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			sandbox.ConfigProfile = value
			i = next
		case strings.HasPrefix(arg, "--profile="):
			value := strings.TrimPrefix(arg, "--profile=")
			if value == "" {
				return clapProfileValueError("--profile", "<CONFIG_PROFILE>", value)
			}
			sandbox.ConfigProfile = value
		case strings.HasPrefix(arg, "-p") && arg != "-p":
			sandbox.ConfigProfile = strings.TrimPrefix(arg, "-p")
		case arg == "--cd" || arg == "-C":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			sandbox.CWD = filepath.Clean(value)
			i = next
		case strings.HasPrefix(arg, "--cd="):
			value := strings.TrimPrefix(arg, "--cd=")
			if value == "" {
				return valueRequiredError("--cd")
			}
			sandbox.CWD = filepath.Clean(value)
		case strings.HasPrefix(arg, "-C") && arg != "-C":
			sandbox.CWD = filepath.Clean(strings.TrimPrefix(arg, "-C"))
		case arg == "--include-managed-config":
			sandbox.IncludeManagedConfig = true
		case arg == "--sandbox-state-json":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			sandbox.SandboxStateJSON = value
			sandboxStateJSONSet = true
			i = next
		case strings.HasPrefix(arg, "--sandbox-state-json="):
			sandbox.SandboxStateJSON = strings.TrimPrefix(arg, "--sandbox-state-json=")
			sandboxStateJSONSet = true
		case arg == "--sandbox-state-readable-root":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--sandbox-state-readable-root", "<SANDBOX_STATE_READABLE_ROOT>", args, i)
			}
			path, err := absoluteCLIPath(value)
			if err != nil {
				return err
			}
			sandbox.SandboxReadableRoots = append(sandbox.SandboxReadableRoots, path)
			i = next
		case strings.HasPrefix(arg, "--sandbox-state-readable-root="):
			path, err := absoluteCLIPath(strings.TrimPrefix(arg, "--sandbox-state-readable-root="))
			if err != nil {
				return err
			}
			sandbox.SandboxReadableRoots = append(sandbox.SandboxReadableRoots, path)
		case arg == "--sandbox-state-disable-network":
			sandbox.SandboxDisableNetwork = true
		case arg == "--allow-unix-socket":
			if runtime.GOOS != "darwin" {
				return unexpectedSubcommandArgumentError("sandbox", arg)
			}
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			path, err := absoluteCLIPath(value)
			if err != nil {
				return err
			}
			sandbox.AllowUnixSockets = append(sandbox.AllowUnixSockets, path)
			i = next
		case strings.HasPrefix(arg, "--allow-unix-socket="):
			if runtime.GOOS != "darwin" {
				return unexpectedSubcommandArgumentError("sandbox", arg)
			}
			path, err := absoluteCLIPath(strings.TrimPrefix(arg, "--allow-unix-socket="))
			if err != nil {
				return err
			}
			sandbox.AllowUnixSockets = append(sandbox.AllowUnixSockets, path)
		case arg == "--log-denials":
			if runtime.GOOS != "darwin" {
				return unexpectedSubcommandArgumentError("sandbox", arg)
			}
			sandbox.LogDenials = true
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			sandbox.ConfigOverrides = append(sandbox.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			sandbox.ConfigOverrides = append(sandbox.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			sandbox.ConfigOverrides = append(sandbox.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return unexpectedSubcommandArgumentError("sandbox", arg)
		default:
			sandbox.Command = append(sandbox.Command, args[i:]...)
			return validate()
		}
	}
	return validate()
}

func validateSandboxOptionDependencies(sandbox *SandboxOptions, sandboxStateJSONSet bool) error {
	if sandbox == nil {
		return nil
	}
	if sandboxStateJSONSet {
		if strings.TrimSpace(sandbox.PermissionProfile) != "" {
			return errors.New("`--sandbox-state-json` conflicts with `--permission-profile`")
		}
		if strings.TrimSpace(sandbox.CWD) != "" {
			return errors.New("`--sandbox-state-json` conflicts with `--cd`")
		}
		if sandbox.IncludeManagedConfig {
			return errors.New("`--sandbox-state-json` conflicts with `--include-managed-config`")
		}
	}
	if strings.TrimSpace(sandbox.PermissionProfile) == "" {
		if strings.TrimSpace(sandbox.CWD) != "" {
			return errors.New("`--cd` requires `--permission-profile`")
		}
		if sandbox.IncludeManagedConfig {
			return errors.New("`--include-managed-config` requires `--permission-profile`")
		}
	}
	if !sandboxStateJSONSet {
		if len(sandbox.SandboxReadableRoots) > 0 {
			return errors.New("`--sandbox-state-readable-root` requires `--sandbox-state-json`")
		}
		if sandbox.SandboxDisableNetwork {
			return errors.New("`--sandbox-state-disable-network` requires `--sandbox-state-json`")
		}
	}
	return nil
}

func absoluteCLIPath(raw string) (string, error) {
	path := filepath.Clean(raw)
	if filepath.IsAbs(path) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func validateSandboxSetupParseDependencies(sandbox *SandboxOptions) error {
	if sandbox == nil {
		return nil
	}
	if sandbox.CurrentUser && sandbox.setupUserSet {
		if sandbox.setupUserFirst {
			return sandboxSetupConflictError("--user <USER>", "--current-user", sandbox)
		}
		return sandboxSetupConflictError("--current-user", "--user <USER>", sandbox)
	}
	if !sandbox.CurrentUser && !sandbox.setupUserSet {
		return sandboxSetupMissingUserError(sandbox)
	}
	// `--user` requires a codex home; clap keys off the parsed value, so a
	// whitespace-only path satisfies the requirement like Rust's PathBuf.
	if sandbox.setupUserSet && sandbox.CodexHome == "" {
		return sandboxSetupMissingCodexHomeError(sandbox)
	}
	return nil
}

func parseExecpolicy(args []string, execpolicy *ExecpolicyOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpExecpolicy)
	}
	execpolicy.Action = args[0]
	switch execpolicy.Action {
	case "check":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--rules" || arg == "-r":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				execpolicy.Rules = append(execpolicy.Rules, value)
				i = next
			case strings.HasPrefix(arg, "--rules="):
				execpolicy.Rules = append(execpolicy.Rules, strings.TrimPrefix(arg, "--rules="))
			case strings.HasPrefix(arg, "-r") && arg != "-r":
				execpolicy.Rules = append(execpolicy.Rules, strings.TrimPrefix(arg, "-r"))
			case arg == "--pretty":
				execpolicy.Pretty = true
			case arg == "--resolve-host-executables":
				execpolicy.ResolveHostExecutables = true
			case arg == "--":
				execpolicy.Command = append(execpolicy.Command, args[i+1:]...)
				i = len(args)
			case strings.HasPrefix(arg, "-"):
				execpolicy.Command = append(execpolicy.Command, args[i:]...)
				i = len(args)
			default:
				execpolicy.Command = append(execpolicy.Command, args[i:]...)
				i = len(args)
			}
		}
		if err := execpolicyCheckMissingRequired(execpolicy); err != nil {
			return err
		}
	default:
		return unrecognizedSubcommandForGroup("execpolicy", execpolicy.Action)
	}
	return nil
}

// execpolicyCheckUsage renders clap's usage for `execpolicy check`: the
// required `--rules` placeholder, then every boolean clap already consumed, then
// the variadic command.
func execpolicyCheckUsage(execpolicy *ExecpolicyOptions) string {
	usage := "Usage: codex execpolicy check --rules <PATH>"
	if execpolicy.Pretty {
		usage += " --pretty"
	}
	if execpolicy.ResolveHostExecutables {
		usage += " --resolve-host-executables"
	}
	return usage + " <COMMAND>..."
}

// execpolicyCheckMissingRequired mirrors clap's block for the two required
// arguments of `execpolicy check`, in declaration order.
func execpolicyCheckMissingRequired(execpolicy *ExecpolicyOptions) error {
	var missing []string
	if len(execpolicy.Rules) == 0 {
		missing = append(missing, "--rules <PATH>")
	}
	if len(execpolicy.Command) == 0 {
		missing = append(missing, "<COMMAND>...")
	}
	if len(missing) == 0 {
		return nil
	}
	return &UsageError{
		Message: requiredArgumentsMessage(missing...),
		Usage:   execpolicyCheckUsage(execpolicy),
	}
}

func parseCloud(args []string, cloud *CloudOptions) error {
	cloud.Attempts = 1
	cloud.Limit = 20
	// clap declares `-c/--config` global, so it may precede the subcommand.
	for len(args) > 0 {
		value, next, handled, err := clapConfigOverrideArg(args, 0)
		if !handled {
			break
		}
		if err != nil {
			return err
		}
		cloud.ConfigOverrides = append(cloud.ConfigOverrides, value)
		args = args[next+1:]
	}
	if len(args) == 0 {
		cloud.Action = "tui"
		return nil
	}
	cloud.Action = args[0]
	switch cloud.Action {
	case "exec":
		var deferred error
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--env":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				cloud.Environment = value
				i = next
			case strings.HasPrefix(arg, "--env="):
				cloud.Environment = strings.TrimPrefix(arg, "--env=")
			case arg == "--attempts":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				attempts, err := clapBoundedIntValue(value, "--attempts", "<ATTEMPTS>", "attempts", 1, 4)
				if err != nil {
					if !deferClapValueError(args, &i, &deferred, err) {
						return err
					}
				} else {
					cloud.Attempts = attempts
				}
				i = next
			case strings.HasPrefix(arg, "--attempts="):
				attempts, err := clapBoundedIntValue(strings.TrimPrefix(arg, "--attempts="), "--attempts", "<ATTEMPTS>", "attempts", 1, 4)
				if err != nil {
					if !deferClapValueError(args, &i, &deferred, err) {
						return err
					}
				} else {
					cloud.Attempts = attempts
				}
			case arg == "--branch":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				cloud.Branch = value
				i = next
			case strings.HasPrefix(arg, "--branch="):
				cloud.Branch = strings.TrimPrefix(arg, "--branch=")
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return clapUnexpectedLongFlagError("cloud exec", arg, args[:i])
			default:
				cloud.Query = strings.Join(args[i:], " ")
				i = len(args)
			}
		}
		if deferred != nil {
			return deferred
		}
		if cloud.Environment == "" {
			return missingRequiredArgumentsError("cloud exec", "--env <ENV_ID>")
		}
	case "status":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
				if err != nil {
					return err
				}
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, value)
				i = next
				continue
			}
			switch {
			case strings.HasPrefix(arg, "-"):
				return clapUnexpectedLongFlagError("cloud status", arg, args[:i])
			default:
				if cloud.TaskID != "" {
					return errors.New("cloud status accepts exactly one TASK_ID")
				}
				cloud.TaskID = arg
			}
		}
		if cloud.TaskID == "" {
			return missingRequiredArgumentsError("cloud status", "<TASK_ID>")
		}
	case "list":
		var deferred error
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--env":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				cloud.Environment = value
				i = next
			case strings.HasPrefix(arg, "--env="):
				cloud.Environment = strings.TrimPrefix(arg, "--env=")
			case arg == "--limit":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				limit, err := clapBoundedIntValue(value, "--limit", "<N>", "limit", 1, 20)
				if err != nil {
					if !deferClapValueError(args, &i, &deferred, err) {
						return err
					}
				} else {
					cloud.Limit = int64(limit)
				}
				i = next
			case strings.HasPrefix(arg, "--limit="):
				limit, err := clapBoundedIntValue(strings.TrimPrefix(arg, "--limit="), "--limit", "<N>", "limit", 1, 20)
				if err != nil {
					if !deferClapValueError(args, &i, &deferred, err) {
						return err
					}
				} else {
					cloud.Limit = int64(limit)
				}
			case arg == "--cursor":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				cloud.Cursor = value
				i = next
			case strings.HasPrefix(arg, "--cursor="):
				cloud.Cursor = strings.TrimPrefix(arg, "--cursor=")
			case arg == "--json":
				cloud.JSON = true
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("cloud list", arg)
			default:
				return unexpectedSubcommandArgumentError("cloud list", arg)
			}
		}
		if deferred != nil {
			return deferred
		}
	case "apply", "diff":
		var deferred error
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
				if err != nil {
					return err
				}
				cloud.ConfigOverrides = append(cloud.ConfigOverrides, value)
				i = next
				continue
			}
			switch {
			case arg == "--attempt":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				attempt, err := clapBoundedIntValue(value, "--attempt", "<N>", "attempts", 1, 4)
				if err != nil {
					if !deferClapValueError(args, &i, &deferred, err) {
						return err
					}
				} else {
					cloud.Attempt = attempt
				}
				i = next
			case strings.HasPrefix(arg, "--attempt="):
				attempt, err := clapBoundedIntValue(strings.TrimPrefix(arg, "--attempt="), "--attempt", "<N>", "attempts", 1, 4)
				if err != nil {
					if !deferClapValueError(args, &i, &deferred, err) {
						return err
					}
				} else {
					cloud.Attempt = attempt
				}
			case strings.HasPrefix(arg, "-"):
				return clapUnexpectedLongFlagError("cloud "+cloud.Action, arg, args[:i])
			default:
				if cloud.TaskID != "" {
					return fmt.Errorf("cloud %s accepts exactly one TASK_ID", cloud.Action)
				}
				cloud.TaskID = arg
			}
		}
		if deferred != nil {
			return deferred
		}
		if cloud.TaskID == "" {
			return missingRequiredArgumentsError("cloud "+cloud.Action, "<TASK_ID>")
		}
	default:
		return unrecognizedSubcommandForGroup("cloud", cloud.Action)
	}
	return nil
}

func parseResponsesAPIProxy(args []string, proxy *ResponsesAPIProxyOptions) error {
	proxy.UpstreamURL = "https://api.openai.com/v1/responses"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--port":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			port, err := parsePort(value)
			if err != nil {
				return err
			}
			proxy.Port = &port
			i = next
		case strings.HasPrefix(arg, "--port="):
			port, err := parsePort(strings.TrimPrefix(arg, "--port="))
			if err != nil {
				return err
			}
			proxy.Port = &port
		case arg == "--server-info":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			proxy.ServerInfo = value
			i = next
		case strings.HasPrefix(arg, "--server-info="):
			proxy.ServerInfo = strings.TrimPrefix(arg, "--server-info=")
		case arg == "--http-shutdown":
			proxy.HTTPShutdown = true
		case arg == "--upstream-url":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			proxy.UpstreamURL = value
			i = next
		case strings.HasPrefix(arg, "--upstream-url="):
			proxy.UpstreamURL = strings.TrimPrefix(arg, "--upstream-url=")
		case arg == "--dump-dir":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			proxy.DumpDir = value
			i = next
		case strings.HasPrefix(arg, "--dump-dir="):
			proxy.DumpDir = strings.TrimPrefix(arg, "--dump-dir=")
		default:
			if strings.HasPrefix(arg, "-") {
				return unexpectedSubcommandArgumentError("responses-api-proxy", arg)
			}
			return unexpectedSubcommandArgumentError("responses-api-proxy", arg)
		}
	}
	return nil
}

// stdioToUDSUnexpectedUsage is the usage clap prints once the flag parser was
// involved: `[OPTIONS]` stays because the command declares no options beyond
// help.
const stdioToUDSUnexpectedUsage = "Usage: codex stdio-to-uds [OPTIONS] <SOCKET_PATH>"

func parseStdioToUDS(args []string, stdio *StdioToUDSOptions) error {
	var positionals []string
	afterSeparator := false
	for _, arg := range args {
		if !afterSeparator && arg == "--" {
			afterSeparator = true
			continue
		}
		if !afterSeparator && len(arg) > 1 && strings.HasPrefix(arg, "-") {
			return unexpectedArgumentError(arg, stdioToUDSUnexpectedUsage)
		}
		positionals = append(positionals, arg)
	}
	switch {
	case len(positionals) == 0:
		// clap prints the positional-only usage when nothing was consumed.
		return &UsageError{
			Message: requiredArgumentsMessage("<SOCKET_PATH>"),
			Usage:   "Usage: codex stdio-to-uds <SOCKET_PATH>",
		}
	case len(positionals) > 1:
		return unexpectedPositionalError(positionals[1], stdioToUDSUnexpectedUsage)
	}
	stdio.SocketPath = positionals[0]
	return nil
}

var execServerRemoteTransports = []string{"noise", "direct"}

// setExecServerRemoteTransport mirrors clap's ValueEnum parsing for
// `--remote-transport`.
func setExecServerRemoteTransport(execServer *ExecServerOptions, value string) error {
	if value == "" {
		return missingValueError("--remote-transport <REMOTE_TRANSPORT>", execServerRemoteTransports)
	}
	switch value {
	case "noise", "direct":
		execServer.RemoteTransport = value
		execServer.RemoteTransportSet = true
		return nil
	}
	return invalidValueError(value, "--remote-transport <REMOTE_TRANSPORT>", execServerRemoteTransports)
}

func parseExecServer(args []string, execServer *ExecServerOptions) error {
	var deferred error
	execServer.ConcurrentRequests = 1
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			execServer.ConfigOverrides = append(execServer.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case strings.HasPrefix(arg, "--ws-"):
			// Rust #47601 shares WebsocketAuthArgs between the app-server and the
			// exec-server listeners. The `forward` subcommand does not declare
			// them, so clap reports the first one as unexpected.
			if execServer.ForwardBound {
				return clapUnexpectedLongFlagError("exec-server forward", arg, args[:i])
			}
			handled, next, err := applyWebSocketAuthFlag(args, i, arg, &execServer.WebSocketAuthOptions)
			if !handled {
				return unexpectedSubcommandArgumentError("exec-server", arg)
			}
			if err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
				continue
			}
			i = next
		case arg == "forward":
			// Rust #39249: forward mode registers an existing WebSocket
			// exec-server as a remote environment.
			if execServer.Forward != "" {
				return fmt.Errorf("exec-server forward mode is already configured")
			}
			execServer.Forward = "forward"
			execServer.ForwardBound = true
		case arg == "--strict-config":
			execServer.StrictConfig = true
		case arg == "--listen":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			execServer.Listen = value
			execServer.ListenSet = true
			i = next
		case strings.HasPrefix(arg, "--listen="):
			execServer.Listen = strings.TrimPrefix(arg, "--listen=")
			execServer.ListenSet = true
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--remote", "<URL>", args, i)
			}
			execServer.Remote = value
			execServer.RemoteSet = true
			i = next
		case strings.HasPrefix(arg, "--remote="):
			execServer.Remote = strings.TrimPrefix(arg, "--remote=")
			execServer.RemoteSet = true
		case arg == "--connect":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--connect", "<URL>", args, i)
			}
			execServer.Forward = value
			execServer.ConnectSet = true
			i = next
		case strings.HasPrefix(arg, "--connect="):
			execServer.Forward = strings.TrimPrefix(arg, "--connect=")
			execServer.ConnectSet = true
		case arg == "--environment-id":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			execServer.EnvironmentID = value
			execServer.EnvironmentIDSet = true
			i = next
		case strings.HasPrefix(arg, "--environment-id="):
			execServer.EnvironmentID = strings.TrimPrefix(arg, "--environment-id=")
			execServer.EnvironmentIDSet = true
		case arg == "--name":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			execServer.Name = value
			i = next
		case strings.HasPrefix(arg, "--name="):
			execServer.Name = strings.TrimPrefix(arg, "--name=")
		case arg == "--use-agent-identity-auth":
			execServer.UseAgentIdentityAuth = true
		case arg == "--exit-on-stdin-close":
			execServer.ExitOnStdinClose = true
		case arg == "--executor-credential-stdin":
			// Rust #54434: clap `ArgAction::SetTrue`, so the flag takes no value.
			execServer.ExecutorCredentialStdin = true
		case arg == "--proxy-private-ips-via-upstream":
			// Rust #48568: clap `ArgAction::SetTrue`, so the flag takes no value.
			execServer.ProxyPrivateIPsViaUpstream = true
		case arg == "--linux-sandbox-pid-namespace":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := validateLinuxSandboxPIDNamespace(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				execServer.LinuxSandboxPIDNamespace = value
			}
			i = next
		case strings.HasPrefix(arg, "--linux-sandbox-pid-namespace="):
			value := strings.TrimPrefix(arg, "--linux-sandbox-pid-namespace=")
			if err := validateLinuxSandboxPIDNamespace(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				execServer.LinuxSandboxPIDNamespace = value
			}
		case arg == "--concurrent-requests":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if parsed, err := parseConcurrentRequestsValue(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				execServer.ConcurrentRequests = parsed
			}
			i = next
		case strings.HasPrefix(arg, "--concurrent-requests="):
			value := strings.TrimPrefix(arg, "--concurrent-requests=")
			if parsed, err := parseConcurrentRequestsValue(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				execServer.ConcurrentRequests = parsed
			}
		case arg == "--remote-transport":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := setExecServerRemoteTransport(execServer, value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			}
			i = next
		case strings.HasPrefix(arg, "--remote-transport="):
			if err := setExecServerRemoteTransport(execServer, strings.TrimPrefix(arg, "--remote-transport=")); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			}
		case arg == "--aws-sigv4":
			execServer.AWSSigV4 = true
		case arg == "--aws-profile":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--aws-profile", "<PROFILE>", args, i)
			}
			execServer.AWSProfile = value
			execServer.AWSFlagSet = true
			i = next
		case strings.HasPrefix(arg, "--aws-profile="):
			execServer.AWSProfile = strings.TrimPrefix(arg, "--aws-profile=")
			execServer.AWSFlagSet = true
		case arg == "--aws-region":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--aws-region", "<REGION>", args, i)
			}
			execServer.AWSRegion = value
			execServer.AWSFlagSet = true
			i = next
		case strings.HasPrefix(arg, "--aws-region="):
			execServer.AWSRegion = strings.TrimPrefix(arg, "--aws-region=")
			execServer.AWSFlagSet = true
		case arg == "--aws-service":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return pendingValueRequiredPlaceholderError("--aws-service", "<SERVICE>", args, i)
			}
			execServer.AWSService = value
			execServer.AWSServiceSet = true
			execServer.AWSFlagSet = true
			i = next
		case strings.HasPrefix(arg, "--aws-service="):
			execServer.AWSService = strings.TrimPrefix(arg, "--aws-service=")
			execServer.AWSServiceSet = true
			execServer.AWSFlagSet = true
		default:
			if strings.HasPrefix(arg, "-") {
				// Once `forward` is bound clap reports against that subcommand,
				// whose page lists the three required forwarding options.
				command := "exec-server"
				if execServer.Forward != "" {
					command += " forward"
				}
				return clapUnexpectedLongFlagError(command, arg, args[:i])
			}
			return unexpectedSubcommandArgumentError("exec-server", arg)
		}
	}
	if deferred != nil {
		return deferred
	}
	if !execServer.ExitOnStdinClose {
		if value, ok := os.LookupEnv(execServerExitOnStdinCloseEnv); ok {
			enabled, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return fmt.Errorf("invalid %s value %q: %w", execServerExitOnStdinCloseEnv, value, err)
			}
			execServer.ExitOnStdinClose = enabled
		}
	}
	if !execServer.ProxyPrivateIPsViaUpstream {
		if value, ok := os.LookupEnv(execServerProxyPrivateIPsViaUpstreamEnv); ok {
			enabled, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return fmt.Errorf("invalid %s value %q: %w", execServerProxyPrivateIPsViaUpstreamEnv, value, err)
			}
			execServer.ProxyPrivateIPsViaUpstream = enabled
		}
	}
	forward := strings.TrimSpace(execServer.Forward)
	if forward != "" && forward != "forward" && execServer.Remote != "" {
		return errors.New("exec-server forward cannot be combined with --remote")
	}
	if !execServer.ForwardBound && execServer.AWSSigV4 && (!execServer.RemoteSet || !execServer.EnvironmentIDSet) {
		// clap renders the SigV4 requirement group with --environment-id first.
		var missing []string
		if !execServer.EnvironmentIDSet {
			missing = append(missing, "--environment-id <ID>")
		}
		if !execServer.RemoteSet {
			missing = append(missing, "--remote <URL>")
		}
		return &UsageError{
			Message:    requiredArgumentsMessage(missing...),
			Usage:      "Usage: codex exec-server --environment-id <ID> --remote <URL> --aws-sigv4",
			SmartUsage: true,
		}
	}
	if !execServer.ForwardBound && execServer.AWSFlagSet {
		// Any --aws-profile/--aws-region/--aws-service pulls in --aws-sigv4,
		// which in turn requires the remote registration options.
		var missing []string
		if !execServer.RemoteSet {
			missing = append(missing, "--remote <URL>")
		}
		if !execServer.EnvironmentIDSet {
			missing = append(missing, "--environment-id <ID>")
		}
		if !execServer.AWSSigV4 {
			missing = append(missing, "--aws-sigv4")
		}
		if len(missing) > 0 {
			return &UsageError{
				Message:    requiredArgumentsMessage(missing...),
				Usage:      "Usage: codex exec-server --remote <URL> --environment-id <ID> --aws-sigv4",
				SmartUsage: true,
			}
		}
	}
	if execServer.Remote != "" && execServer.EnvironmentID == "" {
		return errors.New("--environment-id is required when --remote is set")
	}
	if execServer.ForwardBound {
		var missing []string
		if !execServer.RemoteSet {
			missing = append(missing, "--remote <URL>")
		}
		if !execServer.EnvironmentIDSet {
			missing = append(missing, "--environment-id <ID>")
		}
		if !execServer.ConnectSet {
			missing = append(missing, "--connect <URL>")
		}
		awsRequired := execServer.AWSFlagSet
		if awsRequired {
			missing = append(missing, "--aws-sigv4")
		}
		if len(missing) > 0 {
			usage := "Usage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>"
			if awsRequired {
				usage += " --aws-sigv4"
			}
			return &UsageError{Message: requiredArgumentsMessage(missing...), Usage: usage, SmartUsage: true}
		}
	}
	if forward != "" && forward != "forward" && !strings.HasPrefix(forward, "ws://") && !strings.HasPrefix(forward, "wss://") {
		return fmt.Errorf("exec-server forward requires --connect ws://HOST:PORT, got %q", forward)
	}
	if forward != "" && forward != "forward" && execServer.ListenSet {
		return errors.New("--listen conflicts with exec-server forward")
	}
	if execServer.Remote != "" && execServer.ListenSet {
		return errors.New("--listen conflicts with --remote")
	}
	if execServer.UseAgentIdentityAuth && execServer.Remote == "" && forward == "" {
		return errors.New("--use-agent-identity-auth requires --remote")
	}
	if execServer.ExitOnStdinClose && execServer.Remote == "" && forward == "" {
		return errors.New("--exit-on-stdin-close requires --remote")
	}
	// Rust #54434: `requires = "exec_server_remote"` plus
	// `conflicts_with_all = ["use_agent_identity_auth", "aws_sigv4"]`. The Go port
	// has no --aws-sigv4 flag, so only the Agent Identity conflict applies.
	if execServer.ExecutorCredentialStdin && execServer.Remote == "" && forward == "" {
		return errors.New("--executor-credential-stdin requires --remote")
	}
	if execServer.ExecutorCredentialStdin && execServer.UseAgentIdentityAuth {
		return errors.New("--executor-credential-stdin conflicts with --use-agent-identity-auth")
	}
	// Rust #42781: `--remote-transport` and the `--aws-*` family require the
	// remote registration arguments, and the direct transport requires SigV4.
	transport := strings.ToLower(strings.TrimSpace(execServer.RemoteTransport))
	switch transport {
	case "":
		transport = "noise"
	case "noise", "direct":
	default:
		return fmt.Errorf("invalid --remote-transport value %q; expected noise or direct", execServer.RemoteTransport)
	}
	execServer.RemoteTransport = transport
	if strings.TrimSpace(execServer.AWSService) == "" {
		execServer.AWSService = "execute-api"
	}
	if execServer.RemoteTransportSet && execServer.Remote == "" {
		return errors.New("--remote-transport requires --remote")
	}
	if execServer.AWSSigV4 && execServer.Remote == "" && forward == "" {
		return errors.New("--aws-sigv4 requires --remote")
	}
	if execServer.AWSProfile != "" && !execServer.AWSSigV4 {
		return errors.New("--aws-profile requires --aws-sigv4")
	}
	if execServer.AWSRegion != "" && !execServer.AWSSigV4 {
		return errors.New("--aws-region requires --aws-sigv4")
	}
	if execServer.AWSServiceSet && !execServer.AWSSigV4 {
		return errors.New("--aws-service requires --aws-sigv4")
	}
	if execServer.AWSSigV4 && transport != "direct" {
		return errors.New("--aws-sigv4 requires --remote-transport direct")
	}
	if transport == "direct" && !execServer.AWSSigV4 {
		return errors.New("--remote-transport direct requires --aws-sigv4")
	}
	if execServer.AWSSigV4 && execServer.UseAgentIdentityAuth {
		return errors.New("--aws-sigv4 conflicts with --use-agent-identity-auth")
	}
	if execServer.AWSSigV4 && execServer.ExecutorCredentialStdin {
		return errors.New("--aws-sigv4 conflicts with --executor-credential-stdin")
	}
	if transport == "direct" && forward == "forward" {
		return errors.New("direct exec-server transport does not support forwarding")
	}
	return execServer.ValidateWebsocketAuthFlags()
}

// ValidateWebsocketAuthFlags validates the shared `--ws-*` family for the
// exec-server listener. Rust #47601 rejects listener authentication with stdio,
// --remote, or forward, because those transports never authenticate upgrades.
func (o *ExecServerOptions) ValidateWebsocketAuthFlags() error {
	if o == nil {
		return nil
	}
	if err := o.WebSocketAuthOptions.Validate(); err != nil {
		return err
	}
	if !o.WebSocketAuthOptions.Configured() {
		return nil
	}
	if o.Remote != "" || (o.Forward != "" && o.Forward != "forward") {
		return errors.New("WebSocket listener auth cannot be used with --remote or forward")
	}
	listen := strings.TrimSpace(o.Listen)
	if !o.ListenSet || listen == "" || listen == "stdio" {
		return errors.New("WebSocket listener auth requires a WebSocket --listen transport")
	}
	return nil
}

// Configured reports whether any `--ws-*` flag was supplied.
func (o *WebSocketAuthOptions) Configured() bool {
	if o == nil {
		return false
	}
	return strings.TrimSpace(o.WSAuth) != "" || o.WSAuthSet || o.WSTokenFileSet ||
		o.WSTokenSHA256Set || o.WSSharedSecretFileSet || o.WSIssuerSet ||
		o.WSAudienceSet || o.WSMaxClockSkewSeconds != nil
}

func parseAppServer(args []string, appServer *AppServerOptions) error {
	listenSet := false
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			appServer.ConfigOverrides = append(appServer.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case arg == "--strict-config":
			appServer.StrictConfig = true
		case arg == "--listen":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := validateAppServerListenArg(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.Listen = value
				listenSet = true
			}
			i = next
		case strings.HasPrefix(arg, "--listen="):
			value := strings.TrimPrefix(arg, "--listen=")
			if err := validateAppServerListenArg(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.Listen = value
				listenSet = true
			}
		case arg == "--stdio":
			appServer.Stdio = true
		case arg == "--remote-control":
			appServer.RemoteControl = true
		case arg == "--analytics-default-enabled":
			appServer.AnalyticsDefaultEnabled = true
		case arg == "--print-startup-config":
			appServer.PrintStartupConfig = true
		case arg == "--startup-sign-out":
			appServer.StartupSignOut = true
		case arg == "--startup-client-info":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := validateStartupClientInfo(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.StartupClientInfo = json.RawMessage(value)
			}
			i = next
		case strings.HasPrefix(arg, "--startup-client-info="):
			value := strings.TrimPrefix(arg, "--startup-client-info=")
			if err := validateStartupClientInfo(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.StartupClientInfo = json.RawMessage(value)
			}
		case arg == "--code-mode-host":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := validateAppServerCodeModeHostURL(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.CodeModeHostURL = value
			}
			i = next
		case strings.HasPrefix(arg, "--code-mode-host="):
			value := strings.TrimPrefix(arg, "--code-mode-host=")
			if err := validateAppServerCodeModeHostURL(value); err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.CodeModeHostURL = value
			}
		case arg == "--cloud-skills-snapshot":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.CloudSkillsSnapshot = value
			i = next
		case strings.HasPrefix(arg, "--cloud-skills-snapshot="):
			value := strings.TrimPrefix(arg, "--cloud-skills-snapshot=")
			if value == "" {
				return valueRequiredError("--cloud-skills-snapshot")
			}
			appServer.CloudSkillsSnapshot = value
		case strings.HasPrefix(arg, "--ws-"):
			handled, next, err := applyWebSocketAuthFlag(args, i, arg, &appServer.WebSocketAuthOptions)
			if !handled {
				return unexpectedSubcommandArgumentError("app-server", arg)
			}
			if err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
				continue
			}
			i = next
		case strings.HasPrefix(arg, "--ws-max-clock-skew-seconds="):
			value := strings.TrimPrefix(arg, "--ws-max-clock-skew-seconds=")
			parsed, err := parseUint64Flag("--ws-max-clock-skew-seconds", value)
			if err != nil {
				if !deferClapValueError(args, &i, &deferred, err) {
					return err
				}
			} else {
				appServer.WSMaxClockSkewSeconds = &parsed
			}
		case strings.HasPrefix(arg, "-"):
			return unexpectedSubcommandArgumentError("app-server", arg)
		default:
			appServer.Subcommand = append(appServer.Subcommand, args[i:]...)
			var err error
			switch appServer.Subcommand[0] {
			case "daemon":
				err = parseAppServerDaemon(args[i+1:], &appServer.Daemon)
			case "proxy":
				err = parseAppServerProxy(args[i+1:], &appServer.Proxy)
			case "generate-ts", "generate-json-schema", "generate-internal-json-schema":
				err = parseAppServerGenerate(appServer.Subcommand[0], args[i+1:], &appServer.Generate)
			default:
				return unrecognizedSubcommandForGroup("app-server", appServer.Subcommand[0])
			}
			if err != nil {
				return err
			}
			i = len(args)
		}
	}
	if deferred != nil {
		return deferred
	}
	if appServer.Stdio && listenSet {
		return errors.New("--stdio conflicts with --listen")
	}
	if appServer.StartupSignOut && !appServer.PrintStartupConfig {
		return errors.New("--startup-sign-out requires --print-startup-config")
	}
	if appServer.PrintStartupConfig && (appServer.Stdio || listenSet || appServer.RemoteControl || len(appServer.Subcommand) > 0) {
		return errors.New("--print-startup-config conflicts with server startup options")
	}
	if listenSet {
		if err := validateAppServerListen(appServer.Listen); err != nil {
			return err
		}
	}
	if err := appServer.ValidateWebsocketAuthFlags(); err != nil {
		return err
	}
	return nil
}

func validateAppServerCodeModeHostURL(value string) error {
	// clap prints every code-mode host rejection as a raw value-parser error,
	// without the surrounding "invalid value" wrapper or a usage block.
	raw := func(message string) error { return &UsageError{Message: message, Raw: true} }
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return raw("invalid code-mode host URL: " + err.Error())
	}
	if parsed.Scheme == "" && parsed.Host == "" {
		// Rust's url crate rejects a relative URL before the gRPC endpoint
		// checks run, and clap prints that value-parser error verbatim.
		return raw("invalid code-mode host URL: relative URL without a base")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return raw("code-mode host URL must use http:// or https:// with a host")
	}
	if parsed.Fragment != "" {
		return raw("code-mode host URL must not contain a fragment")
	}
	if parsed.User != nil {
		return raw("gRPC code-mode host URL must not contain credentials")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" {
		return raw("gRPC code-mode host URL must not contain a path or query")
	}
	return nil
}

// validateAppServerListenArg renders clap's value-parser diagnostic for
// `--listen`, which rejects the empty inline value the plain parser accepts.
func validateAppServerListenArg(value string) error {
	if value == "" {
		return &UsageError{Message: "invalid value '' for '--listen <URL>': unsupported --listen URL ``; expected `stdio://`, `unix://`, `unix://PATH`, `ws://IP:PORT`, or `off`"}
	}
	if err := validateAppServerListen(value); err != nil {
		return &UsageError{Message: "invalid value '" + value + "' for '--listen <URL>': " + err.Error()}
	}
	return nil
}

func validateAppServerListen(listen string) error {
	listen = strings.TrimSpace(listen)
	switch {
	case listen == "", listen == "stdio://", listen == "off":
		return nil
	case strings.HasPrefix(listen, "unix://"):
		path := strings.TrimPrefix(listen, "unix://")
		if path != "" {
			if _, err := filepath.Abs(path); err != nil {
				return fmt.Errorf("invalid unix socket --listen URL `%s`; failed to resolve socket path: %s", listen, err)
			}
		}
		return nil
	case strings.HasPrefix(listen, "ws://"):
		socketAddr := strings.TrimPrefix(listen, "ws://")
		host, port, err := net.SplitHostPort(socketAddr)
		if err != nil || net.ParseIP(host) == nil {
			return fmt.Errorf("invalid websocket --listen URL `%s`; expected `ws://IP:PORT`", listen)
		}
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 0 || parsedPort > 65535 {
			return fmt.Errorf("invalid websocket --listen URL `%s`; expected `ws://IP:PORT`", listen)
		}
		return nil
	default:
		return fmt.Errorf("unsupported --listen URL `%s`; expected `stdio://`, `unix://`, `unix://PATH`, `ws://IP:PORT`, or `off`", listen)
	}
}

func (o *AppServerOptions) ValidateWebsocketAuthFlags() error {
	if o == nil {
		return nil
	}
	return o.WebSocketAuthOptions.Validate()
}

// Validate mirrors Rust's WebsocketAuthArgs::try_into_settings: the shared
// `--ws-*` family must describe exactly one supported mode.
func (o *WebSocketAuthOptions) Validate() error {
	if o == nil {
		return nil
	}
	mode := strings.TrimSpace(o.WSAuth)
	usesCapability := o.WSTokenFileSet || o.WSTokenSHA256Set
	usesSigned := o.WSSharedSecretFileSet || o.WSIssuerSet || o.WSAudienceSet || o.WSMaxClockSkewSeconds != nil
	switch mode {
	case "":
		if o.WSAuthSet {
			return fmt.Errorf("unknown --ws-auth mode %q", mode)
		}
		if usesCapability || usesSigned {
			return errors.New("websocket auth flags require `--ws-auth capability-token` or `--ws-auth signed-bearer-token`")
		}
	case "capability-token":
		if usesSigned {
			return errors.New("`--ws-shared-secret-file`, `--ws-issuer`, `--ws-audience`, and `--ws-max-clock-skew-seconds` require `--ws-auth signed-bearer-token`")
		}
		if strings.TrimSpace(o.WSTokenFile) != "" && strings.TrimSpace(o.WSTokenSHA256) != "" {
			return errors.New("`--ws-token-file` and `--ws-token-sha256` are mutually exclusive")
		}
		if !usesCapability {
			return errors.New("`--ws-token-file` or `--ws-token-sha256` is required when `--ws-auth capability-token` is set")
		}
		if o.WSTokenFileSet {
			if err := validateWebsocketAuthPath("--ws-token-file", o.WSTokenFile); err != nil {
				return err
			}
		}
		if o.WSTokenSHA256Set {
			trimmed, err := validateWebsocketAuthSHA256("--ws-token-sha256", o.WSTokenSHA256)
			if err != nil {
				return err
			}
			o.WSTokenSHA256 = trimmed
		}
	case "signed-bearer-token":
		if usesCapability {
			return errors.New("`--ws-token-file` and `--ws-token-sha256` require `--ws-auth capability-token`, not `signed-bearer-token`")
		}
		if !o.WSSharedSecretFileSet {
			return errors.New("`--ws-shared-secret-file` is required when `--ws-auth signed-bearer-token` is set")
		}
		if err := validateWebsocketAuthPath("--ws-shared-secret-file", o.WSSharedSecretFile); err != nil {
			return err
		}
		o.WSIssuer = strings.TrimSpace(o.WSIssuer)
		o.WSAudience = strings.TrimSpace(o.WSAudience)
	default:
		return fmt.Errorf("unknown --ws-auth mode %q", mode)
	}
	return nil
}

func validateWebsocketAuthPath(flagName string, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", flagName)
	}
	return nil
}

func validateWebsocketAuthSHA256(flagName string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 64 {
		return "", fmt.Errorf("%s must be a 64-character hex SHA-256 digest", flagName)
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("%s must be a 64-character hex SHA-256 digest", flagName)
	}
	return trimmed, nil
}

func parseAppServerDaemon(args []string, daemon *AppServerDaemonOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpAppServerDaemon)
	}
	daemon.Action = args[0]
	for _, arg := range args[1:] {
		if arg == "-h" || arg == "--help" {
			daemon.Help = true
			return nil
		}
	}
	switch daemon.Action {
	case "start", "restart", "enable-remote-control", "disable-remote-control", "stop", "version":
		// clap attaches the global --config/--enable/--disable to every leaf,
		// so only an unknown flag or a stray positional reaches this branch.
		command := "app-server daemon " + daemon.Action
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
				if err != nil {
					return err
				}
				daemon.ConfigOverrides = append(daemon.ConfigOverrides, value)
				i = next
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return clapUnexpectedLongFlagError(command, arg, args[1:i])
			}
			return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts[command].usage)
		}
	case "pid-update-loop":
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--restore-release":
				if i+1 >= len(args) {
					return &UsageError{Message: "a value is required for '--restore-release <RESTORE_RELEASE>' but none was supplied"}
				}
				i++
				daemon.RestoreRelease = strings.TrimSpace(args[i])
			case "--check-package-ownership":
				daemon.CheckPackageOwnership = true
			default:
				return unexpectedSubcommandArgumentError("app-server daemon pid-update-loop", args[i])
			}
		}
	case "bootstrap":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
				if err != nil {
					return err
				}
				daemon.ConfigOverrides = append(daemon.ConfigOverrides, value)
				i = next
				continue
			}
			switch arg {
			case "--remote-control":
				daemon.RemoteControl = true
			default:
				if strings.HasPrefix(arg, "-") {
					return clapUnexpectedLongFlagError("app-server daemon bootstrap", arg, args[1:i])
				}
				return unexpectedSubcommandArgumentError("app-server daemon bootstrap", arg)
			}
		}
	case "update":
		command := "app-server daemon update"
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
				if err != nil {
					return err
				}
				daemon.ConfigOverrides = append(daemon.ConfigOverrides, value)
				i = next
				continue
			}
			switch arg {
			case "--from-cli":
				daemon.FromCLI = true
			case "-y", "--yes":
				daemon.Yes = true
			default:
				if strings.HasPrefix(arg, "-") {
					return clapUnexpectedLongFlagError(command, arg, args[1:i])
				}
				return unexpectedSubcommandArgumentError(command, arg)
			}
		}
		if daemon.Yes && !daemon.FromCLI {
			// clap models this as `requires = "from_cli"`, so it reports the
			// missing flag and echoes the accepted options in argv order after
			// the requirement.
			usage := "Usage: codex app-server daemon update --from-cli"
			if used := clapUsageOptionTokens(command, args[1:], ""); len(used) > 0 {
				usage += " " + strings.Join(used, " ")
			}
			return &UsageError{
				Message: requiredArgumentsMessage("--from-cli"),
				Usage:   usage,
			}
		}
	default:
		return unrecognizedSubcommandForGroup("app-server daemon", daemon.Action)
	}
	return nil
}

func parseQueue(args []string, queue *QueueOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Rust #57273: queue rejects inherited options that cannot affect a
		// queued message. clap validates required arguments first, so those
		// rejections live in validateQueueSharedOptions after the loop.
		if arg == "--" {
			// Queue declares no positional arguments: clap ends option parsing
			// here and reports the first token behind it.
			if i+1 < len(args) {
				return unexpectedSubcommandArgumentError("queue", args[i+1])
			}
			break
		}
		if queueUndeclaredOption(arg) {
			return queueUnexpectedArgumentError(queueUndeclaredSpelling(arg))
		}
		if handled, err := parseSharedOptionVariadicImages(args, &i, &queue.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		if handled, err := parseTUIOnlyOption(args, &i, &queue.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		switch {
		case arg == "--no-daemon":
			// Rust's queue command CLI is TuiCli-based, so the flag parses and
			// is then rejected unless an explicit --remote is supplied (#46088).
			queue.Shared.NoDaemon = true
		case arg == "--resume":
			// Rust #57232: resume an unloaded thread after queueing.
			queue.Resume = true
		case arg == "--thread":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.Thread = value
			queue.ThreadSet = true
			i = next
		case strings.HasPrefix(arg, "--thread="):
			queue.Thread = strings.TrimPrefix(arg, "--thread=")
			queue.ThreadSet = true
		case arg == "--message":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.Message = value
			i = next
		case strings.HasPrefix(arg, "--message="):
			value := strings.TrimPrefix(arg, "--message=")
			if value == "" {
				return valueRequiredError("--message")
			}
			queue.Message = value
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			queue.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--remote-auth-token-env":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.RemoteAuthEnv = value
			i = next
		case strings.HasPrefix(arg, "--remote-auth-token-env="):
			queue.RemoteAuthEnv = strings.TrimPrefix(arg, "--remote-auth-token-env=")
		case arg == "--strict-config":
			queue.StrictConfig = true
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.ConfigOverrides = append(queue.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			queue.ConfigOverrides = append(queue.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && arg != "-C":
			queue.ConfigOverrides = append(queue.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return unexpectedSubcommandArgumentError("queue", arg)
		default:
			// Queue declares no positional arguments, so clap reports a bare word
			// as an unexpected argument.
			return unexpectedSubcommandArgumentError("queue", arg)
		}
	}
	var missing []string
	if !queue.ThreadSet {
		missing = append(missing, "--thread <THREAD>")
	}
	if strings.TrimSpace(queue.Message) == "" {
		missing = append(missing, "--message <TEXT>")
	}
	if len(missing) > 0 {
		return missingRequiredArgumentsError("queue", missing...)
	}
	if err := validateQueueSharedOptions(queue.Shared, strings.TrimSpace(queue.Remote) != ""); err != nil {
		return err
	}
	return nil
}

// queueUndeclaredSpelling is clap's rendering of an undeclared queue option:
// an attached short-flag value is reported as the short flag alone.
func queueUndeclaredSpelling(arg string) string {
	if strings.HasPrefix(arg, "-a") && !strings.HasPrefix(arg, "--") && arg != "-a" {
		return "-a"
	}
	return arg
}

// queueSimilarArgumentTips mirrors clap's "a similar argument exists" tip for
// the undeclared options whose name is close enough to a declared one.
var queueSimilarArgumentTips = map[string]string{
	"--no-worktree":            "--worktree",
	"--model-reasoning-effort": "--model",
}

// queueUnexpectedArgumentError renders clap's unexpected-argument diagnostic for
// a token QueueCommand never declares.
func queueUnexpectedArgumentError(arg string) error {
	similar, ok := queueSimilarArgumentTips[arg]
	if !ok {
		return unexpectedSubcommandArgumentError("queue", arg)
	}
	return &UsageError{
		Message:    "unexpected argument '" + arg + "' found\n\n  tip: a similar argument exists: '" + similar + "'",
		Usage:      subcommandUsageTexts["queue"].usage,
		SmartUsage: true,
	}
}

// queueUndeclaredOption reports options the shared session parsers accept but
// Rust's QueueCommand never declares: clap rejects them as unexpected arguments
// before any validation runs.
func queueUndeclaredOption(arg string) bool {
	switch arg {
	case "-a", "--ask-for-approval", "--search", "--no-alt-screen", "--no-daemon",
		"--no-worktree", "--model-reasoning-effort", "--reasoning-effort":
		return true
	}
	if strings.HasPrefix(arg, "--ask-for-approval=") || strings.HasPrefix(arg, "--model-reasoning-effort=") || strings.HasPrefix(arg, "--reasoning-effort=") {
		return true
	}
	return strings.HasPrefix(arg, "-a") && !strings.HasPrefix(arg, "--")
}

// validateQueueSharedOptions mirrors Rust queue_cmd.rs restricted_options
// (#57273): queue accepts --remote, --remote-auth-token-env, --config,
// --enable/--disable, --profile, --strict-config, --worktree, --no-daemon and
// a local --cd, but rejects other inherited session options.
func validateQueueSharedOptions(shared SharedOptions, remoteSpecified bool) error {
	if remoteSpecified && shared.CWD != "" {
		return queueUnsupportedError("`codex queue` does not support `--cd` with `--remote`")
	}
	if shared.Model != "" {
		return queueUnsupportedError("`codex queue` does not support `--model`; queued messages use the thread's model")
	}
	if len(shared.Images) > 0 {
		return queueUnsupportedError("`codex queue` does not support image attachments")
	}
	for _, unsupported := range []struct {
		name string
		used bool
	}{
		{"--model-reasoning-effort", shared.ModelReasoningEffort != ""},
		{"--sandbox", shared.Sandbox != ""},
		{"--ask-for-approval", shared.ApprovalPolicy != ""},
		{"--search", shared.Search},
		{"--no-alt-screen", shared.NoAltScreen},
		{"--oss", shared.OSS},
		{"--local-provider", shared.OSSProvider != ""},
		{"--add-dir", len(shared.AddDirs) > 0},
		{"--dangerously-bypass-approvals-and-sandbox", shared.DangerouslyBypassApprovalsAndSandbox},
		{"--dangerously-bypass-hook-trust", shared.DangerouslyBypassHookTrust},
	} {
		if unsupported.used {
			return queueUnsupportedError(fmt.Sprintf("`codex queue` does not support `%s`", unsupported.name))
		}
	}
	return nil
}

// queueUnsupportedError mirrors queue_cmd.rs restricted_options: Rust reports
// these through clap, so they render with the `error:` prefix and exit status 2
// but carry no usage block.
func queueUnsupportedError(message string) error {
	return &UsageError{Message: message, Raw: true}
}

func parseAgents(args []string, agents *AgentsOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Agents-specific flags that must preserve raw values or are exclusive
		// to the dashboard invocation (Rust #39870 keeps these on the raw
		// value path rather than the shared session-config parser).
		switch {
		case arg == "-C" || arg == "--cd":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			agents.Cwd = value
			i = next
			continue
		case strings.HasPrefix(arg, "--cd="):
			value := strings.TrimPrefix(arg, "--cd=")
			if value == "" {
				return valueRequiredError("--cd")
			}
			agents.Cwd = value
			continue
		case arg == "--no-alt-screen":
			agents.NoAltScreen = true
			continue
		case arg == "--no-daemon":
			agents.NoDaemon = true
			continue
		}
		if matched, err := parseSharedOption(args, &i, &agents.Shared); err != nil {
			return err
		} else if matched {
			// Rust #39870: codex agents may accept model / approval / sandbox /
			// configuration session settings, but never initial images, local
			// providers, or additional directories.
			if len(agents.Shared.Images) > 0 {
				return fmt.Errorf("`codex agents` does not accept initial images")
			}
			if agents.Shared.OSS || strings.TrimSpace(agents.Shared.OSSProvider) != "" {
				return fmt.Errorf("`codex agents` does not accept a local provider")
			}
			if len(agents.Shared.AddDirs) > 0 {
				return fmt.Errorf("`codex agents` does not accept additional directories")
			}
			continue
		}
		if matched, err := parseTUIOnlyOption(args, &i, &agents.Shared); err != nil {
			return err
		} else if matched {
			continue
		}
		switch {
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			agents.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			agents.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--remote-auth-token-env":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			agents.RemoteAuthEnv = value
			i = next
		case strings.HasPrefix(arg, "--remote-auth-token-env="):
			agents.RemoteAuthEnv = strings.TrimPrefix(arg, "--remote-auth-token-env=")
		// Rust's AgentsCommand does not flatten --strict-config, so clap rejects
		// it after the subcommand; the root spelling stays valid (Agents is on
		// Rust's strict-config allow-list).
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			agents.ConfigOverrides = append(agents.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			agents.ConfigOverrides = append(agents.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && arg != "-C":
			agents.ConfigOverrides = append(agents.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		default:
			if strings.HasPrefix(arg, "-") {
				return unexpectedSubcommandArgumentError("agents", arg)
			}
			return unexpectedSubcommandArgumentError("agents", arg)
		}
	}
	if agents.Shared.NoAltScreen {
		agents.NoAltScreen = true
	}
	if agents.Shared.NoDaemon {
		agents.NoDaemon = true
	}
	return nil
}

func parseAppServerProxy(args []string, proxy *AppServerProxyOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			proxy.ConfigOverrides = append(proxy.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case arg == "--sock":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			proxy.SocketPath = value
			i = next
		case strings.HasPrefix(arg, "--sock="):
			proxy.SocketPath = strings.TrimPrefix(arg, "--sock=")
		default:
			if strings.HasPrefix(arg, "-") {
				return unexpectedSubcommandArgumentError("app-server proxy", arg)
			}
			return unexpectedSubcommandArgumentError("app-server proxy", arg)
		}
	}
	return nil
}

func parseAppServerGenerate(action string, args []string, generate *AppServerGenerateOptions) error {
	generate.Action = action
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			generate.ConfigOverrides = append(generate.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case arg == "--out" || arg == "-o":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			generate.OutDir = value
			i = next
		case strings.HasPrefix(arg, "--out="):
			value := strings.TrimPrefix(arg, "--out=")
			if value == "" {
				return missingValueNamedError("--out", "<DIR>", nil)
			}
			generate.OutDir = value
		case strings.HasPrefix(arg, "-o") && arg != "-o":
			value := strings.TrimPrefix(strings.TrimPrefix(arg, "-o"), "=")
			if value == "" {
				return missingValueNamedError("--out", "<DIR>", nil)
			}
			generate.OutDir = value
		case action == "generate-ts" && (arg == "--prettier" || arg == "-p"):
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			generate.Prettier = value
			i = next
		case action == "generate-ts" && strings.HasPrefix(arg, "--prettier="):
			value := strings.TrimPrefix(arg, "--prettier=")
			if value == "" {
				return missingValueNamedError("--prettier", "<PRETTIER_BIN>", nil)
			}
			generate.Prettier = value
		case action == "generate-ts" && strings.HasPrefix(arg, "-p") && arg != "-p":
			value := strings.TrimPrefix(strings.TrimPrefix(arg, "-p"), "=")
			if value == "" {
				return missingValueNamedError("--prettier", "<PRETTIER_BIN>", nil)
			}
			generate.Prettier = value
		case action != "generate-internal-json-schema" && arg == "--experimental":
			generate.Experimental = true
		default:
			if strings.HasPrefix(arg, "-") {
				return clapUnexpectedLongFlagError("app-server "+action, arg, args[:i])
			}
			return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts["app-server "+action].usage)
		}
	}
	if generate.OutDir == "" {
		return missingRequiredArgumentsError("app-server "+action, "--out <DIR>")
	}
	return nil
}

func parseApp(args []string, app *AppOptions) error {
	app.Path = "."
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			app.ConfigOverrides = append(app.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case arg == "--download-url":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			app.DownloadURL = value
			i = next
		case strings.HasPrefix(arg, "--download-url="):
			app.DownloadURL = strings.TrimPrefix(arg, "--download-url=")
		case strings.HasPrefix(arg, "-"):
			return clapUnexpectedLongFlagError("app", arg, args[:i])
		default:
			if app.Path != "." {
				return errors.New("app accepts at most one PATH")
			}
			app.Path = arg
		}
	}
	return nil
}

func parseUpdate(args []string, update *UpdateOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			update.ConfigOverrides = append(update.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			update.ConfigOverrides = append(update.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			update.ConfigOverrides = append(update.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		default:
			return unexpectedSubcommandArgumentError("update", arg)
		}
	}
	return nil
}

func parseCompletion(args []string, completion *CompletionOptions) error {
	// Shell defaults to bash, so track binding separately from the value.
	bound := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			completion.ConfigOverrides = append(completion.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			completion.ConfigOverrides = append(completion.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			completion.ConfigOverrides = append(completion.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-") && arg != "-":
			return unexpectedSubcommandArgumentError("completion", arg)
		default:
			if bound {
				return unexpectedPositionalError(arg, subcommandUsageTexts["completion"].usage)
			}
			completion.Shell = arg
			bound = true
		}
	}
	return nil
}

func parseDoctor(args []string, doctor *DoctorOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			doctor.JSON = true
		case arg == "--summary":
			doctor.Summary = true
		case arg == "--all":
			doctor.All = true
		case arg == "--no-color":
			doctor.NoColor = true
		case arg == "--ascii":
			doctor.ASCII = true
		case arg == "--feedback":
			doctor.Feedback = true
		case arg == "--network-only":
			doctor.NetworkOnly = true
		case arg == "--probe-filesystem-path":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			doctor.ProbeFilesystemPath = value
			i = next
		case strings.HasPrefix(arg, "--probe-filesystem-path="):
			doctor.ProbeFilesystemPath = strings.TrimPrefix(arg, "--probe-filesystem-path=")
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			doctor.ConfigOverrides = append(doctor.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			doctor.ConfigOverrides = append(doctor.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			doctor.ConfigOverrides = append(doctor.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return clapUnexpectedLongFlagError("doctor", arg, args[:i])
		default:
			return unexpectedArgumentWithoutTip(arg, doctorUsageText)
		}
	}
	return nil
}

func parseMigrateNFSDB(args []string, options *MigrateNFSDBOptions) error {
	if options.StagingDir == "" {
		options.StagingDir = os.TempDir()
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			options.ConfigOverrides = append(options.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case arg == "--staging-dir" || strings.HasPrefix(arg, "--staging-dir="):
			value, next, err := optionalFlagValue(args, i, arg, "--staging-dir", "<STAGING_DIR>")
			if err != nil {
				return err
			}
			if strings.HasPrefix(arg, "--staging-dir=") && value == "" {
				return valueRequiredError("--staging-dir")
			}
			options.StagingDir = value
			i = next
		case strings.HasPrefix(arg, "-"):
			return clapUnexpectedLongFlagError("migrate-nfs-db", arg, args[:i])
		default:
			return unexpectedSubcommandArgumentError("migrate-nfs-db", arg)
		}
	}
	return nil
}

func parseMigrateRollouts(args []string, options *MigrateRolloutsOptions) error {
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			options.ConfigOverrides = append(options.ConfigOverrides, value)
			i = next
			continue
		}
		switch {
		case arg == "--apply":
			options.Apply = true
		case arg == "--json":
			options.JSON = true
		case arg == "--verbose":
			options.Verbose = true
		case arg == "--thread" || strings.HasPrefix(arg, "--thread="):
			value, next, err := optionalFlagValue(args, i, arg, "--thread", "<THREAD_ID>")
			if err != nil {
				return err
			}
			if value == "" {
				return &UsageError{Message: "invalid value '' for '--thread <THREAD_ID>': invalid length: expected length 32 for simple format, found 0"}
			}
			options.Threads = append(options.Threads, value)
			i = next
		case arg == "--max-mib-per-second" || strings.HasPrefix(arg, "--max-mib-per-second="):
			value, next, err := optionalFlagValue(args, i, arg, "--max-mib-per-second", "<MIB>")
			if err != nil {
				return err
			}
			parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			var invalid error
			switch {
			case err != nil:
				invalid = &UsageError{Message: "invalid value '" + value + "' for '--max-mib-per-second <MIB>': " + clapIntegerParseDetail(value)}
			case parsed == 0:
				invalid = &UsageError{Message: "invalid value '" + value + "' for '--max-mib-per-second <MIB>': 0 is not in 1..18446744073709551615"}
			}
			if invalid != nil {
				if !deferClapValueError(args, &i, &deferred, invalid) {
					return invalid
				}
			} else {
				options.MaxMibPerSecond = parsed
			}
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				return clapUnexpectedLongFlagError("migrate-rollouts", arg, args[:i])
			}
			return unexpectedSubcommandArgumentError("migrate-rollouts", arg)
		}
	}
	if deferred != nil {
		return deferred
	}
	return nil
}

func optionalFlagValue(args []string, index int, arg, name, placeholder string) (string, int, error) {
	if strings.HasPrefix(arg, name+"=") {
		return strings.TrimPrefix(arg, name+"="), index, nil
	}
	// clap never binds a leading-dash token as an option value, so `--thread
	// --apply` reports the pending value (or the undeclared flag) instead of
	// swallowing the next flag as the value.
	if index+1 >= len(args) || clapLooksLikeOption(args[index+1]) {
		return "", index, pendingValueRequiredPlaceholderError(name, placeholder, args, index)
	}
	return args[index+1], index + 1, nil
}

func parseApply(args []string, apply *ApplyOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			apply.ConfigOverrides = append(apply.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			apply.ConfigOverrides = append(apply.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c"):
			apply.ConfigOverrides = append(apply.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return unexpectedSubcommandArgumentError("apply", arg)
		default:
			if i+1 < len(args) {
				return errors.New("apply accepts at most one PATCH")
			}
			apply.Patch = arg
			return nil
		}
	}
	if len(apply.ConfigOverrides) > 0 {
		return &UsageError{Message: requiredArgumentsMessage("<TASK_ID>"), Usage: "Usage: codex apply --config <key=value> <TASK_ID>"}
	}
	return missingRequiredArgumentsError("apply", "<TASK_ID>")
}

func parseRemoteControl(args []string, remote *RemoteControlOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, next, handled, err := clapConfigOverrideArg(args, i); handled {
			if err != nil {
				return err
			}
			remote.ConfigOverrides = append(remote.ConfigOverrides, value)
			i = next
			continue
		}
		switch arg {
		case "--json":
			remote.JSON = true
		case "--no-daemon":
			remote.NoDaemon = true
		case "start", "stop", "pair":
			if remote.Subcommand != "" {
				return errors.New("remote-control accepts at most one subcommand")
			}
			remote.Subcommand = arg
		default:
			if strings.HasPrefix(arg, "-") {
				command := "remote-control"
				if remote.Subcommand != "" {
					command += " " + remote.Subcommand
				}
				return clapUnexpectedLongFlagError(command, arg, args[:i])
			}
			return unrecognizedSubcommandWithSuggestion(arg, subcommandUsageTexts["remote-control"].usage, subcommandCandidates["remote-control"])
		}
	}
	return nil
}

// parseSessionSelection parses `codex resume` / `codex fork`. Both share Rust's
// ResumeCommand/ForkCommand shape: at most two positionals (SESSION_ID and
// PROMPT, the latter marked conflicts_with = "last"), a variadic `--image`, and
// clap-shaped diagnostics for unknown tokens.
func parseSessionSelection(args []string, session *SessionOptions, includeNonInteractive bool) error {
	command := "fork"
	if includeNonInteractive {
		command = "resume"
	}
	var positionals []string
	var deferred error
	bindPositional := func(value string) error {
		if len(positionals) == len(clapCommandPositionalSlots[command]) {
			return unexpectedPositionalError(value, subcommandUsageTexts[command].usage)
		}
		positionals = append(positionals, value)
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOptionVariadicImages(args, &i, &session.Shared); err != nil {
			if deferClapValueError(args, &i, &deferred, err) {
				continue
			}
			return err
		} else if handled {
			continue
		}
		if handled, err := parseTUIOnlyOption(args, &i, &session.Shared); err != nil {
			if deferClapValueError(args, &i, &deferred, err) {
				continue
			}
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			for _, value := range args[i+1:] {
				if err := bindPositional(value); err != nil {
					return err
				}
			}
			break
		}
		switch {
		case arg == "--last":
			session.Last = true
		case arg == "--all":
			session.All = true
		case arg == "--include-non-interactive":
			if !includeNonInteractive {
				return errors.New("--include-non-interactive is only supported for resume")
			}
			session.IncludeNonInteractive = true
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			session.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			session.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--remote-auth-token-env":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			session.RemoteAuthEnv = value
			i = next
		case strings.HasPrefix(arg, "--remote-auth-token-env="):
			session.RemoteAuthEnv = strings.TrimPrefix(arg, "--remote-auth-token-env=")
		case arg == "--strict-config":
			session.StrictConfig = true
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			session.ConfigOverrides = append(session.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			session.ConfigOverrides = append(session.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && arg != "-C":
			session.ConfigOverrides = append(session.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return sessionUnexpectedArgumentError(command, args[:i], arg)
		default:
			if err := bindPositional(arg); err != nil {
				return err
			}
		}
	}
	if deferred != nil {
		return deferred
	}
	if session.Last && len(positionals) > 1 {
		return sessionLastPromptConflictError(command)
	}
	switch len(positionals) {
	case 0:
	case 1:
		// Rust keeps `--last PROMPT` valid: clap binds the single positional to
		// SESSION_ID, and the resume path reinterprets it as the prompt.
		if session.Last {
			session.Prompt = positionals[0]
		} else {
			session.Target = positionals[0]
		}
	default:
		session.Target = positionals[0]
		session.Prompt = positionals[1]
	}
	return nil
}

func parseSessionMutation(args []string, session *SessionOptions, command string, allowForce bool) error {
	var deferred error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOptionVariadicImages(args, &i, &session.Shared); err != nil {
			if deferClapValueError(args, &i, &deferred, err) {
				continue
			}
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			for _, value := range args[i+1:] {
				if session.Target != "" {
					return unexpectedPositionalError(value, subcommandUsageTexts[command].usage)
				}
				session.Target = value
			}
			break
		}
		switch {
		case arg == "--force":
			if !allowForce {
				// Rust declares --force only on `codex delete`, so clap reports an
				// unknown flag for archive/unarchive.
				return sessionUnexpectedArgumentError(command, args[:i], arg)
			}
			session.Force = true
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			session.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			session.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--remote-auth-token-env":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			session.RemoteAuthEnv = value
			i = next
		case strings.HasPrefix(arg, "--remote-auth-token-env="):
			session.RemoteAuthEnv = strings.TrimPrefix(arg, "--remote-auth-token-env=")
		case arg == "--strict-config":
			session.StrictConfig = true
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			session.ConfigOverrides = append(session.ConfigOverrides, value)
			i = next
		case strings.HasPrefix(arg, "--config="):
			session.ConfigOverrides = append(session.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && arg != "-C":
			session.ConfigOverrides = append(session.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
		case strings.HasPrefix(arg, "-"):
			return sessionUnexpectedArgumentError(command, args[:i], arg)
		default:
			if session.Target != "" {
				return unexpectedPositionalError(arg, subcommandUsageTexts[command].usage)
			}
			session.Target = arg
		}
	}
	if deferred != nil {
		return deferred
	}
	if session.Target == "" {
		return missingRequiredArgumentsError(command, "<SESSION>")
	}
	if command == string(CommandDelete) && session.Force {
		if !isUUIDLike(session.Target) {
			return errors.New("--force requires a session UUID; names must be confirmed interactively")
		}
	}
	return nil
}

func isUUIDLike(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func parseDebug(args []string, debug *DebugOptions) error {
	if len(args) == 0 {
		return MissingSubcommandError(GroupHelpDebug)
	}
	debug.Subcommand = args[0]
	switch debug.Subcommand {
	case "models":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--bundled":
				debug.BundledModels = true
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.ConfigOverrides = append(debug.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			default:
				return unexpectedSubcommandArgumentError("debug models", arg)
			}
		}
	case "app-server":
		if len(args) < 2 {
			return MissingSubcommandError(GroupHelpDebugAppServer)
		}
		debug.AppServerAction = args[1]
		switch debug.AppServerAction {
		case "send-message-v2":
			command := "debug app-server send-message-v2"
			bound := false
			for i := 2; i < len(args); i++ {
				arg := args[i]
				if arg == "-c" || arg == "--config" {
					value, next, err := requireValue(args, i, arg)
					if err != nil {
						return err
					}
					debug.ConfigOverrides = append(debug.ConfigOverrides, value)
					i = next
					continue
				}
				if strings.HasPrefix(arg, "--config=") {
					debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
					continue
				}
				if strings.HasPrefix(arg, "-") {
					return clapUnexpectedLongFlagError(command, arg, args[2:i])
				}
				if bound {
					return unexpectedArgumentWithoutTip(arg, subcommandUsageTexts[command].usage)
				}
				debug.AppServerMessage = arg
				bound = true
			}
			if !bound {
				return missingRequiredArgumentsError(command, "<USER_MESSAGE>")
			}
		default:
			return unrecognizedSubcommandForGroup("debug app-server", debug.AppServerAction)
		}
	case "prompt-input":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--":
				// clap stops option parsing here, so the rest is the prompt.
				if i+1 < len(args) {
					debug.Prompt = strings.Join(args[i+1:], " ")
				}
				return nil
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.ConfigOverrides = append(debug.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case arg == "--image" || arg == "-i":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.Images = append(debug.Images, splitComma(value)...)
				i = next
			case strings.HasPrefix(arg, "--image="):
				value := strings.TrimPrefix(arg, "--image=")
				if value == "" {
					return missingImageValueError()
				}
				debug.Images = append(debug.Images, splitComma(value)...)
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("debug prompt-input", arg)
			default:
				debug.Prompt = strings.Join(args[i:], " ")
				return nil
			}
		}
	case "trace-reduce":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--output" || arg == "-o":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.TraceOutput = value
				i = next
			case strings.HasPrefix(arg, "--output="):
				debug.TraceOutput = strings.TrimPrefix(arg, "--output=")
			case strings.HasPrefix(arg, "-o") && arg != "-o":
				debug.TraceOutput = strings.TrimPrefix(arg, "-o")
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.ConfigOverrides = append(debug.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			case strings.HasPrefix(arg, "-"):
				return unexpectedSubcommandArgumentError("debug trace-reduce", arg)
			default:
				if debug.TraceBundle != "" {
					return errors.New("debug trace-reduce accepts exactly one TRACE_BUNDLE")
				}
				debug.TraceBundle = arg
			}
		}
		if debug.TraceBundle == "" {
			return missingRequiredArgumentsError("debug trace-reduce", "<TRACE_BUNDLE>")
		}
	case "clear-memories":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "-c" || arg == "--config":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.ConfigOverrides = append(debug.ConfigOverrides, value)
				i = next
			case strings.HasPrefix(arg, "--config="):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "--config="))
			case strings.HasPrefix(arg, "-c"):
				debug.ConfigOverrides = append(debug.ConfigOverrides, strings.TrimPrefix(arg, "-c"))
			default:
				return unexpectedSubcommandArgumentError("debug clear-memories", arg)
			}
		}
	case "config":
		if len(args) != 1 {
			return errors.New("debug config does not accept arguments")
		}
	default:
		return unrecognizedSubcommandForGroup("debug", debug.Subcommand)
	}
	return nil
}

// splitGroupConfigOverrides mirrors clap's flattened `-c`/`--config` on a group
// command: options written in front of the subcommand name belong to the same
// override list as the ones the subcommand parser collects, so they land in
// RootOptions and mergeRootConfigOverrides prepends them. Leaf-level `-c` stays
// with the subcommand parser, which keeps its existing precedence.
func (p *Parsed) splitGroupConfigOverrides(args []string) ([]string, error) {
	_, consumed, ok := GroupHelpKindForPath(p.Command, args)
	if !ok {
		return args, nil
	}
	remaining, err := consumeConfigOverrides(args[consumed:], &p.Root.ConfigOverrides)
	if err != nil || len(remaining) == len(args)-consumed {
		return args, err
	}
	merged := make([]string, 0, consumed+len(remaining))
	merged = append(merged, args[:consumed]...)
	merged = append(merged, remaining...)
	return merged, nil
}

// consumeConfigOverrides collects a leading run of clap's `-c`/`--config`
// values and returns the remaining arguments.
func consumeConfigOverrides(args []string, overrides *[]string) ([]string, error) {
	for len(args) > 0 {
		switch {
		case args[0] == "-c" || args[0] == "--config":
			if len(args) < 2 || clapLooksLikeOption(args[1]) {
				return nil, pendingValueRequiredError("--config", args, 0)
			}
			*overrides = append(*overrides, args[1])
			args = args[2:]
		case strings.HasPrefix(args[0], "--config="):
			*overrides = append(*overrides, strings.TrimPrefix(args[0], "--config="))
			args = args[1:]
		case strings.HasPrefix(args[0], "-c"):
			*overrides = append(*overrides, strings.TrimPrefix(args[0], "-c"))
			args = args[1:]
		default:
			return args, nil
		}
	}
	return args, nil
}

// groupHelpRequestFor resolves clap's help action for a group command. The
// dispatch happens before the group's own parser because each of those parsers
// rejects `-h`/`--help` as an unknown argument.
func groupHelpRequestFor(command Command, args []string) *GroupHelpRequest {
	kind, consumed, ok := GroupHelpKindForPath(command, args)
	if !ok {
		return nil
	}
	rest := args[consumed:]
	switch {
	case len(rest) == 0:
		return &GroupHelpRequest{Kind: kind, Short: true, Required: true}
	case rest[0] == "-h":
		// clap's help action fires as soon as the flag is parsed, so any
		// trailing tokens are ignored.
		return &GroupHelpRequest{Kind: kind, Short: true}
	case rest[0] == "--help":
		return &GroupHelpRequest{Kind: kind}
	case rest[0] == "help" && len(rest) == 1:
		return &GroupHelpRequest{Kind: kind}
	default:
		return nil
	}
}

// splitSubcommandFeatureFlags mirrors clap's flattened `--enable`/`--disable`
// arguments: Rust declares them on every subcommand, so `codex login --enable
// foo` parses exactly like `codex --enable foo login`. The values land in
// RootOptions, which is where the shared startup path validates and applies
// them, so both spellings behave identically. `sandbox setup` is the one
// subcommand Rust parses with its own clap definition and therefore rejects the
// shared flags.
func (p *Parsed) splitSubcommandFeatureFlags(args []string) ([]string, error) {
	if p.Command == CommandSandbox && len(args) > 0 && args[0] == "setup" {
		return args, nil
	}
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			remaining = append(remaining, args[i:]...)
			break
		}
		name, value, hasInlineValue := strings.Cut(arg, "=")
		if name != "--enable" && name != "--disable" {
			remaining = append(remaining, arg)
			continue
		}
		if hasInlineValue {
			p.appendFeatureFlag(name, value)
			continue
		}
		// clap refuses to take a leading-dash token as an option value and
		// reports the pending value instead.
		if i+1 >= len(args) || clapLooksLikeOption(args[i+1]) {
			return nil, pendingValueRequiredError(name, args, i)
		}
		p.appendFeatureFlag(name, args[i+1])
		i++
	}
	return remaining, nil
}

func (p *Parsed) appendFeatureFlag(name, value string) {
	if name == "--enable" {
		p.Root.EnableFeatures = append(p.Root.EnableFeatures, value)
		return
	}
	p.Root.DisableFeatures = append(p.Root.DisableFeatures, value)
}

// clapLooksLikeOption reports whether clap would treat the token as an option
// instead of an option value. A single "-" is a value (stdin).
func clapLooksLikeOption(token string) bool {
	return len(token) > 1 && strings.HasPrefix(token, "-")
}

func parseSharedOption(args []string, i *int, shared *SharedOptions) (bool, error) {
	return parseSharedOptionWithImages(args, i, shared, false)
}

// parseSharedOptionVariadicImages mirrors Rust's root/exec `--image`, which
// declares num_args = 1.. and therefore consumes every following non-flag
// argument; `exec resume`/`exec fork` declare a single value and keep using
// parseSharedOption.
func parseSharedOptionVariadicImages(args []string, i *int, shared *SharedOptions) (bool, error) {
	return parseSharedOptionWithImages(args, i, shared, true)
}

func parseSharedOptionWithImages(args []string, i *int, shared *SharedOptions, variadicImages bool) (bool, error) {
	arg := args[*i]
	switch {
	case arg == "--worktree":
		shared.Worktree = true
		return true, nil
	case arg == "--no-worktree":
		shared.Worktree = false
		return true, nil
	case arg == "--image" || arg == "-i":
		if variadicImages {
			values, next, err := requireVariadicValues(args, *i, arg)
			if err != nil {
				return false, err
			}
			for _, value := range values {
				shared.Images = append(shared.Images, splitComma(value)...)
			}
			// The loop advances once more after a handled option, so point at the
			// last consumed value rather than at the next flag.
			*i = next - 1
			return true, nil
		}
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.Images = append(shared.Images, splitComma(value)...)
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--image="):
		// clap stops a num_args = 1.. option at an inline value, so
		// `--image=a.png b.png` leaves b.png to the positional slots.
		value := strings.TrimPrefix(arg, "--image=")
		images := splitComma(value)
		if len(images) == 0 {
			return false, missingImageValueError()
		}
		shared.Images = append(shared.Images, images...)
		return true, nil
	case strings.HasPrefix(arg, "-i="):
		images := splitComma(strings.TrimPrefix(arg, "-i="))
		if len(images) == 0 {
			return false, missingImageValueError()
		}
		shared.Images = append(shared.Images, images...)
		return true, nil
	case arg == "--model" || arg == "-m":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.Model = value
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--model="):
		shared.Model = strings.TrimPrefix(arg, "--model=")
		return true, nil
	case strings.HasPrefix(arg, "-m") && arg != "-m":
		shared.Model = strings.TrimPrefix(arg, "-m")
		return true, nil
	case arg == "--model-reasoning-effort" || arg == "--reasoning-effort":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.ModelReasoningEffort = value
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--model-reasoning-effort="):
		shared.ModelReasoningEffort = strings.TrimPrefix(arg, "--model-reasoning-effort=")
		return true, nil
	case strings.HasPrefix(arg, "--reasoning-effort="):
		shared.ModelReasoningEffort = strings.TrimPrefix(arg, "--reasoning-effort=")
		return true, nil
	case arg == "--oss":
		shared.OSS = true
		return true, nil
	case arg == "--local-provider":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.OSSProvider = value
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--local-provider="):
		shared.OSSProvider = strings.TrimPrefix(arg, "--local-provider=")
		return true, nil
	case arg == "--profile" || arg == "-p":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.Profile = value
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--profile="):
		value := strings.TrimPrefix(arg, "--profile=")
		if value == "" {
			return false, clapProfileValueError("--profile", "<CONFIG_PROFILE_V2>", value)
		}
		shared.Profile = value
		return true, nil
	case strings.HasPrefix(arg, "-p") && arg != "-p":
		shared.Profile = strings.TrimPrefix(arg, "-p")
		return true, nil
	case arg == "--sandbox" || arg == "-s":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, missingValueError("--sandbox <SANDBOX_MODE>", sandboxModeValues)
		}
		if err := validateSandboxMode(value); err != nil {
			return false, err
		}
		shared.Sandbox = value
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--sandbox="):
		value := strings.TrimPrefix(arg, "--sandbox=")
		if err := validateSandboxMode(value); err != nil {
			return false, err
		}
		shared.Sandbox = value
		return true, nil
	case strings.HasPrefix(arg, "-s") && arg != "-s":
		value := strings.TrimPrefix(arg, "-s")
		if err := validateSandboxMode(value); err != nil {
			return false, err
		}
		shared.Sandbox = value
		return true, nil
	case arg == "--dangerously-bypass-approvals-and-sandbox" || arg == "--yolo":
		shared.DangerouslyBypassApprovalsAndSandbox = true
		return true, nil
	case arg == "--dangerously-bypass-hook-trust":
		shared.DangerouslyBypassHookTrust = true
		return true, nil
	case arg == "--approve-for-me" || arg == "--not-so-yolo":
		shared.ApproveForMe = true
		return true, nil
	case arg == "--cd" || arg == "-C":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.CWD = filepath.Clean(value)
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--cd="):
		value := strings.TrimPrefix(arg, "--cd=")
		if value == "" {
			return false, valueRequiredError("--cd")
		}
		shared.CWD = filepath.Clean(value)
		return true, nil
	case arg == "--add-dir":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.AddDirs = append(shared.AddDirs, filepath.Clean(value))
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--add-dir="):
		value := strings.TrimPrefix(arg, "--add-dir=")
		if value == "" {
			return false, valueRequiredError("--add-dir")
		}
		shared.AddDirs = append(shared.AddDirs, filepath.Clean(value))
		return true, nil
	default:
		return false, nil
	}
}

func parseTUIOnlyOption(args []string, i *int, shared *SharedOptions) (bool, error) {
	arg := args[*i]
	switch {
	case arg == "--ask-for-approval" || arg == "-a":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		if err := setApprovalPolicy(shared, value); err != nil {
			return false, err
		}
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--ask-for-approval="):
		if err := setApprovalPolicy(shared, strings.TrimPrefix(arg, "--ask-for-approval=")); err != nil {
			return false, err
		}
		return true, nil
	case strings.HasPrefix(arg, "-a") && arg != "-a":
		if err := setApprovalPolicy(shared, strings.TrimPrefix(arg, "-a")); err != nil {
			return false, err
		}
		return true, nil
	case arg == "--search":
		shared.Search = true
		return true, nil
	case arg == "--no-alt-screen":
		shared.NoAltScreen = true
		return true, nil
	case arg == "--no-daemon":
		shared.NoDaemon = true
		return true, nil
	default:
		return false, nil
	}
}

var webSocketAuthModes = []string{"capability-token", "signed-bearer-token"}

// setWebSocketAuthMode mirrors clap's ValueEnum parsing for `--ws-auth`: the
// value is validated while argv is matched, so an invalid or empty inline value
// reports before any later unknown-argument diagnostic.
func setWebSocketAuthMode(options *WebSocketAuthOptions, value string) error {
	if value == "" {
		return missingValueError("--ws-auth <MODE>", webSocketAuthModes)
	}
	switch value {
	case "capability-token", "signed-bearer-token":
		options.WSAuth = value
		options.WSAuthSet = true
		return nil
	}
	return invalidValueError(value, "--ws-auth <MODE>", webSocketAuthModes)
}

func setApprovalPolicy(shared *SharedOptions, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return missingValueError("--ask-for-approval <APPROVAL_POLICY>", approvalPolicyValues)
	}
	switch value {
	case "on-request", "never":
		shared.ApprovalPolicy = value
		return nil
	default:
		return invalidValueError(value, "--ask-for-approval <APPROVAL_POLICY>", approvalPolicyValues)
	}
}

func setExecColor(exec *ExecOptions, value string) error {
	if value == "" {
		return missingValueError("--color <COLOR>", colorValues)
	}
	for _, allowed := range colorValues {
		if value == allowed {
			exec.Color = value
			return nil
		}
	}
	return invalidValueError(value, "--color <COLOR>", colorValues)
}

func setExecCyberAccessProgram(exec *ExecOptions, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return missingValueError("--cyber-access-program <PROGRAM>", cyberAccessProgramValues)
	}
	switch value {
	case "standard", "daybreak_blue", "daybreak_red":
		exec.CyberAccessProgram = value
		return nil
	default:
		return invalidValueError(value, "--cyber-access-program <PROGRAM>", cyberAccessProgramValues)
	}
}

func requireValue(args []string, i int, flag string) (string, int, error) {
	// clap never accepts a leading-dash token as an option value unless the
	// argument declares allow_hyphen_values, so `-c --json` reports the pending
	// value instead of eating the next flag.
	if i+1 >= len(args) || clapLooksLikeOption(args[i+1]) {
		return "", i, pendingValueRequiredError(flag, args, i)
	}
	return args[i+1], i + 1, nil
}

// missingImageValueError mirrors clap's diagnostic for `--image`, whose
// num_args = 1.. renders the value name with a trailing ellipsis.
func missingImageValueError() error {
	return &UsageError{Message: "a value is required for '--image <FILE>...' but none was supplied"}
}

// requireVariadicValues consumes clap's num_args = 1.. values: every argument
// after the flag that does not start a new option (and until `--`).
func requireVariadicValues(args []string, i int, flag string) ([]string, int, error) {
	values, next, err := requireVariadicValuesFrom(args, i+1, flag)
	if err != nil || len(values) == 0 {
		return nil, i, missingImageValueError()
	}
	return values, next, nil
}

func requireVariadicValuesFrom(args []string, start int, flag string) ([]string, int, error) {
	var values []string
	next := start
	for next < len(args) {
		candidate := args[next]
		if candidate == "--" || (len(candidate) > 1 && strings.HasPrefix(candidate, "-")) {
			break
		}
		values = append(values, candidate)
		next++
	}
	return values, next, nil
}

func splitComma(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseKeyValueFlag(value string, flag string) (string, string, error) {
	key, parsed, ok := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", fmt.Errorf("%s requires KEY=VALUE", flag)
	}
	return key, parsed, nil
}

// validateStartupClientInfo mirrors the serde deserializer clap installs for
// `--startup-client-info`: the value must decode into the ClientInfo struct, so
// scalars, arrays and syntax errors are rejected before the server starts.
// serde's wording is reproduced for the shapes encoding/json can classify.
func validateStartupClientInfo(value string) error {
	invalid := func(detail string) error {
		return &UsageError{Message: "invalid value '" + value + "' for '--startup-client-info <STARTUP_CLIENT_INFO>': " + detail}
	}
	if value == "" {
		return invalid("EOF while parsing a value at line 1 column 0")
	}
	var raw any
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		if strings.Contains(err.Error(), "unexpected end of JSON input") {
			return invalid("EOF while parsing an object at line 1 column 1")
		}
		return invalid("expected value at line 1 column 1")
	}
	column := strconv.Itoa(len(strings.TrimSpace(value)))
	switch typed := raw.(type) {
	case map[string]any:
		return nil
	case []any:
		return invalid("invalid length " + strconv.Itoa(len(typed)) + ", expected struct ClientInfo with 3 elements at line 1 column " + column)
	case string:
		return invalid("invalid type: string " + strconv.Quote(typed) + ", expected struct ClientInfo at line 1 column " + column)
	case bool:
		return invalid("invalid type: boolean `" + strconv.FormatBool(typed) + "`, expected struct ClientInfo at line 1 column " + column)
	case float64:
		if typed == float64(int64(typed)) {
			return invalid("invalid type: integer `" + strconv.FormatInt(int64(typed), 10) + "`, expected struct ClientInfo at line 1 column " + column)
		}
		return invalid("invalid type: floating point `" + strconv.FormatFloat(typed, 'g', -1, 64) + "`, expected struct ClientInfo at line 1 column " + column)
	case nil:
		return invalid("invalid type: null, expected struct ClientInfo at line 1 column " + column)
	}
	return invalid("expected struct ClientInfo at line 1 column " + column)
}

// validateLinuxSandboxPIDNamespace applies clap's value parser for the global
// `--linux-sandbox-pid-namespace <MODE>` flag.
func validateLinuxSandboxPIDNamespace(value string) error {
	switch strings.TrimSpace(value) {
	case "isolate", "inherit":
		return nil
	}
	return &UsageError{Message: "invalid value '" + value + "' for '--linux-sandbox-pid-namespace <MODE>': expected 'isolate' or 'inherit'"}
}

// parseConcurrentRequestsValue applies clap's NonZeroUsize parser for
// `--concurrent-requests <COUNT>`.
func parseConcurrentRequestsValue(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, &UsageError{Message: "invalid value '" + value + "' for '--concurrent-requests <COUNT>': " + clapIntegerParseDetail(value)}
	}
	if parsed <= 0 {
		return 0, &UsageError{Message: "invalid value '" + value + "' for '--concurrent-requests <COUNT>': number would be zero for non-zero type"}
	}
	return parsed, nil
}

func parseBoundedInt(value string, name string, min int, max int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return parsed, nil
}

func parseBoundedInt64(value string, name string, min int64, max int64) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return parsed, nil
}

// clapIntegerParseDetail is the Rust integer parser's message for a value that
// failed to parse; an empty string names EOF instead of a bad digit.
func clapIntegerParseDetail(value string) string {
	if strings.TrimSpace(value) == "" {
		return "cannot parse integer from empty string"
	}
	return "invalid digit found in string"
}

func parseUint64Flag(flag string, value string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, &UsageError{Message: "invalid value '" + value + "' for '" + flag + " <SECONDS>': " + clapIntegerParseDetail(value)}
	}
	return parsed, nil
}

// clapBoundedIntValue applies clap's value parser for a bounded integer option:
// the diagnostic repeats the range wording of the Rust parser and names the
// option together with its placeholder.
func clapBoundedIntValue(value, flag, placeholder, label string, min, max int) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, &UsageError{Message: "invalid value '" + value + "' for '" + flag + " " + placeholder + "': " +
			label + " must be an integer between " + strconv.Itoa(min) + " and " + strconv.Itoa(max)}
	}
	if parsed < min || parsed > max {
		return 0, &UsageError{Message: "invalid value '" + value + "' for '" + flag + " " + placeholder + "': " +
			label + " must be between " + strconv.Itoa(min) + " and " + strconv.Itoa(max)}
	}
	return parsed, nil
}

func parsePort(value string) (uint16, error) {
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("port must be between 0 and 65535")
	}
	return uint16(parsed), nil
}
