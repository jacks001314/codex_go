package cli

import (
	"encoding/hex"
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
	CommandMCPServer         Command = "mcp-server"
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
	CommandAgents            Command = "agents"
	CommandQueue             Command = "queue"
)

var knownCommands = map[string]Command{
	"exec":                CommandExec,
	"e":                   CommandExec,
	"review":              CommandReview,
	"login":               CommandLogin,
	"logout":              CommandLogout,
	"mcp":                 CommandMCP,
	"plugin":              CommandPlugin,
	"mcp-server":          CommandMCPServer,
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
	"agents":              CommandAgents,
	"queue":               CommandQueue,
}

type SharedOptions struct {
	Images                               []string
	Model                                string
	ModelReasoningEffort                 string
	OSS                                  bool
	OSSProvider                          string
	Profile                              string
	Sandbox                              string
	ApprovalPolicy                       string
	Search                               bool
	NoAltScreen                          bool
	DangerouslyBypassApprovalsAndSandbox bool
	DangerouslyBypassHookTrust           bool
	CWD                                  string
	AddDirs                              []string
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
	Subcommand            string
	SubArgs               []string
	Review                ReviewOptions
	Resume                ExecResumeOptions
	Fork                  ExecForkOptions
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
}

type MCPOptions struct {
	Action            string
	Name              string
	JSON              bool
	URL               string
	BearerTokenEnvVar string
	OAuthClientID     string
	OAuthResource     string
	Env               map[string]string
	Command           []string
	Scopes            []string
	ConfigOverrides   []string
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
}

type ExecpolicyOptions struct {
	Action                 string
	Rules                  []string
	Pretty                 bool
	ResolveHostExecutables bool
	Command                []string
}

type MCPServerOptions struct {
	StrictConfig bool
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
	StrictConfig         bool
	Listen               string
	ListenSet            bool
	Remote               string
	EnvironmentID        string
	Name                 string
	UseAgentIdentityAuth bool
	ExitOnStdinClose     bool
}

const execServerExitOnStdinCloseEnv = "CODEX_EXEC_SERVER_EXIT_ON_STDIN_CLOSE"

type AppServerOptions struct {
	Subcommand              []string
	Daemon                  AppServerDaemonOptions
	Proxy                   AppServerProxyOptions
	Generate                AppServerGenerateOptions
	StrictConfig            bool
	Listen                  string
	Stdio                   bool
	RemoteControl           bool
	AnalyticsDefaultEnabled bool
	CodeModeHostURL         string
	WSAuth                  string
	WSAuthSet               bool
	WSTokenFile             string
	WSTokenFileSet          bool
	WSTokenSHA256           string
	WSTokenSHA256Set        bool
	WSSharedSecretFile      string
	WSSharedSecretFileSet   bool
	WSIssuer                string
	WSIssuerSet             bool
	WSAudience              string
	WSAudienceSet           bool
	WSMaxClockSkewSeconds   *uint64
}

type AppServerDaemonOptions struct {
	Action        string
	RemoteControl bool
}

type AppServerProxyOptions struct {
	SocketPath string
}

type AppServerGenerateOptions struct {
	Action       string
	OutDir       string
	Prettier     string
	Experimental bool
}

type AppOptions struct {
	Path        string
	DownloadURL string
}

type UpdateOptions struct {
	JSON bool
}

type CompletionOptions struct {
	Shell string
}

type DoctorOptions struct {
	JSON    bool
	Summary bool
	All     bool
	NoColor bool
	ASCII   bool
}

type MigrateRolloutsOptions struct {
	Apply           bool
	Threads         []string
	MaxMibPerSecond uint64
	JSON            bool
	Verbose         bool
}

type ApplyOptions struct {
	Patch           string
	ConfigOverrides []string
}

type RemoteControlOptions struct {
	JSON       bool
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
	Thread          string
	Message         string
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
}

type ReviewOptions struct {
	Uncommitted bool
	Base        string
	Commit      string
	CommitTitle string
	Prompt      string
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
	MCPServer         MCPServerOptions
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
	Apply             ApplyOptions
	RemoteControl     RemoteControlOptions
	Session           SessionOptions
	Queue             QueueOptions
	Agents            AgentsOptions
	Debug             DebugOptions
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

	rest, err := parseRoot(args, &p.Root)
	if err != nil {
		return nil, err
	}
	if len(rest) == 0 {
		return p, p.Validate()
	}
	if cmd, ok := knownCommands[rest[0]]; ok {
		p.Command = cmd
		p.RawSubcommand = append([]string(nil), rest...)
		if p.Root.StrictConfig && strictConfigUnsupportedBeforeSubcommandParse(cmd) {
			return nil, fmt.Errorf("`--strict-config` is not supported for `codex %s`", cmd)
		}
		if _, err := parseSubcommand(p, rest[1:]); err != nil {
			return nil, err
		}
		return p, p.Validate()
	}
	p.Root.Prompt = strings.Join(rest, " ")
	return p, p.Validate()
}

func strictConfigUnsupportedBeforeSubcommandParse(command Command) bool {
	switch command {
	case CommandInteractive, CommandExec, CommandReview, CommandMCPServer, CommandExecServer,
		CommandResume, CommandArchive, CommandDelete, CommandUnarchive, CommandFork, CommandDoctor,
		CommandAppServer:
		return false
	default:
		return command != ""
	}
}

func parseSubcommand(p *Parsed, args []string) (*Parsed, error) {
	switch p.Command {
	case CommandExec:
		return p, parseExec(args, &p.Exec)
	case CommandReview:
		p.Command = CommandReview
		p.Exec.Subcommand = "review"
		return p, parseReview(args, &p.Exec.Review)
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
	case CommandMCPServer:
		return p, parseMCPServer(args, &p.MCPServer)
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
	case CommandDebug:
		return p, parseDebug(args, &p.Debug)
	default:
		p.RawSubcommand = append(p.RawSubcommand[:1:1], args...)
		return p, nil
	}
}

func (p *Parsed) Validate() error {
	if err := validateTUISharedOptions(p.Root.Shared); err != nil {
		return err
	}
	switch p.Command {
	case CommandResume, CommandFork:
		if err := validateTUISharedOptions(p.Session.Shared); err != nil {
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

func validateTUISharedOptions(shared SharedOptions) error {
	if shared.DangerouslyBypassApprovalsAndSandbox && strings.TrimSpace(shared.ApprovalPolicy) != "" {
		return errors.New("`--dangerously-bypass-approvals-and-sandbox` conflicts with `--ask-for-approval`")
	}
	return nil
}

func (p *Parsed) unsupportedStrictConfigSubcommandName() string {
	switch p.Command {
	case CommandInteractive, CommandExec, CommandReview, CommandMCPServer, CommandExecServer,
		CommandResume, CommandArchive, CommandDelete, CommandUnarchive, CommandFork, CommandDoctor, CommandQueue:
		return ""
	case CommandAppServer:
		if len(p.AppServer.Subcommand) == 0 {
			return ""
		}
		return "app-server " + strings.Join(p.AppServer.Subcommand, " ")
	case "app":
		return "app"
	default:
		if p.Command == "" {
			return ""
		}
		return string(p.Command)
	}
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
	default:
		if p.Command == "" {
			return ""
		}
		return string(p.Command)
	}
}

func parseRoot(args []string, root *RootOptions) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOption(args, &i, &root.Shared); err != nil {
			return nil, err
		} else if handled {
			continue
		}
		if handled, err := parseTUIOnlyOption(args, &i, &root.Shared); err != nil {
			return nil, err
		} else if handled {
			continue
		}
		if arg == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if cmd, ok := knownCommands[arg]; ok {
			_ = cmd
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case arg == "-c" || arg == "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, err
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
				return nil, err
			}
			root.EnableFeatures = append(root.EnableFeatures, value)
			i = next
		case strings.HasPrefix(arg, "--enable="):
			root.EnableFeatures = append(root.EnableFeatures, strings.TrimPrefix(arg, "--enable="))
		case arg == "--disable":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			root.DisableFeatures = append(root.DisableFeatures, value)
			i = next
		case strings.HasPrefix(arg, "--disable="):
			root.DisableFeatures = append(root.DisableFeatures, strings.TrimPrefix(arg, "--disable="))
		case arg == "--remote":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			root.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			root.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--remote-auth-token-env":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			root.RemoteAuthEnv = value
			i = next
		case strings.HasPrefix(arg, "--remote-auth-token-env="):
			root.RemoteAuthEnv = strings.TrimPrefix(arg, "--remote-auth-token-env=")
		case arg == "--strict-config":
			root.StrictConfig = true
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown root option %s", arg)
		default:
			rest = append(rest, args[i:]...)
			i = len(args)
		}
	}
	return rest, nil
}

func parseExec(args []string, exec *ExecOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOption(args, &i, &exec.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			exec.SubArgs = append(exec.SubArgs, args[i+1:]...)
			return nil
		}
		switch {
		case arg == "-":
			exec.Prompt = "-"
			return nil
		case arg == "resume":
			exec.Subcommand = arg
			return parseExecResume(args[i+1:], exec)
		case arg == "fork":
			exec.Subcommand = arg
			return parseExecFork(args[i+1:], exec)
		case arg == "review":
			exec.Subcommand = arg
			return parseReview(args[i+1:], &exec.Review)
		case arg == "--strict-config":
			exec.StrictConfig = true
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
			exec.OutputSchema = strings.TrimPrefix(arg, "--output-schema=")
		case arg == "--color":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := setExecColor(exec, value); err != nil {
				return err
			}
			i = next
		case strings.HasPrefix(arg, "--color="):
			if err := setExecColor(exec, strings.TrimPrefix(arg, "--color=")); err != nil {
				return err
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
			exec.LastMessageFile = strings.TrimPrefix(arg, "--output-last-message=")
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
			return fmt.Errorf("unknown exec option %s", arg)
		default:
			exec.Prompt = strings.Join(args[i:], " ")
			return nil
		}
	}
	return nil
}

func parseExecResume(args []string, exec *ExecOptions) error {
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOption(args, &i, &exec.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		switch {
		case arg == "--last":
			exec.Resume.Last = true
		case arg == "--all":
			exec.Resume.All = true
		case arg == "--json" || arg == "--experimental-json":
			exec.JSON = true
		case arg == "--strict-config":
			exec.StrictConfig = true
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
			exec.OutputSchema = strings.TrimPrefix(arg, "--output-schema=")
		case arg == "--output-last-message" || arg == "-o":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			exec.LastMessageFile = value
			i = next
		case strings.HasPrefix(arg, "--output-last-message="):
			exec.LastMessageFile = strings.TrimPrefix(arg, "--output-last-message=")
		case arg == "--color":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := setExecColor(exec, value); err != nil {
				return err
			}
			i = next
		case strings.HasPrefix(arg, "--color="):
			if err := setExecColor(exec, strings.TrimPrefix(arg, "--color=")); err != nil {
				return err
			}
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
			return fmt.Errorf("unknown exec resume option %s", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if exec.Resume.Last {
		if len(positionals) > 1 {
			return errors.New("exec resume --last accepts at most one prompt")
		}
		if len(positionals) == 1 {
			exec.Resume.Prompt = positionals[0]
		}
		return nil
	}
	switch len(positionals) {
	case 0:
		return nil
	case 1:
		exec.Resume.SessionID = positionals[0]
	default:
		exec.Resume.SessionID = positionals[0]
		exec.Resume.Prompt = strings.Join(positionals[1:], " ")
	}
	return nil
}

func parseExecFork(args []string, exec *ExecOptions) error {
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--image" || arg == "-i" {
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			exec.Fork.Images = append(exec.Fork.Images, splitComma(value)...)
			i = next
			continue
		}
		if strings.HasPrefix(arg, "--image=") {
			exec.Fork.Images = append(exec.Fork.Images, splitComma(strings.TrimPrefix(arg, "--image="))...)
			continue
		}
		if handled, err := parseSharedOption(args, &i, &exec.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		switch {
		case arg == "-":
			positionals = append(positionals, arg)
		case arg == "--strict-config":
			exec.StrictConfig = true
		case arg == "--skip-git-repo-check":
			exec.SkipGitRepoCheck = true
		case arg == "--ephemeral":
			exec.Ephemeral = true
		case arg == "--ignore-user-config":
			exec.IgnoreUserConfig = true
		case arg == "--ignore-rules":
			exec.IgnoreRules = true
		case arg == "--json" || arg == "--experimental-json":
			exec.JSON = true
		case arg == "--output-schema" || arg == "--output-last-message" || arg == "-o":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if arg == "--output-schema" {
				exec.OutputSchema = value
			} else {
				exec.LastMessageFile = value
			}
			i = next
		case strings.HasPrefix(arg, "--output-schema="):
			exec.OutputSchema = strings.TrimPrefix(arg, "--output-schema=")
		case strings.HasPrefix(arg, "--output-last-message="):
			exec.LastMessageFile = strings.TrimPrefix(arg, "--output-last-message=")
		case arg == "--color" || strings.HasPrefix(arg, "--color="):
			value := strings.TrimPrefix(arg, "--color=")
			if arg == "--color" {
				var next int
				var err error
				value, next, err = requireValue(args, i, arg)
				if err != nil {
					return err
				}
				i = next
			}
			if err := setExecColor(exec, value); err != nil {
				return err
			}
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
			return fmt.Errorf("unknown exec fork option %s", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return errors.New("exec fork requires SESSION_ID")
	}
	exec.Fork.SessionID = positionals[0]
	if len(positionals) > 1 {
		exec.Fork.Prompt = strings.Join(positionals[1:], " ")
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
			return fmt.Errorf("unknown login option %s", arg)
		default:
			return fmt.Errorf("unknown login action %s", arg)
		}
	}
	return nil
}

func parseReview(args []string, review *ReviewOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
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
			return fmt.Errorf("unknown review option %s", arg)
		default:
			if review.Uncommitted || review.Base != "" || review.Commit != "" {
				return errors.New("PROMPT conflicts with --uncommitted, --base, and --commit")
			}
			review.Prompt = strings.Join(args[i:], " ")
			return nil
		}
	}
	if review.CommitTitle != "" && review.Commit == "" {
		return errors.New("--title requires --commit")
	}
	return nil
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
			return fmt.Errorf("unknown logout option %s", arg)
		}
	}
	return nil
}

func parseFeatures(args []string, features *FeatureOptions) error {
	if len(args) == 0 {
		return errors.New("features requires a subcommand")
	}
	features.Action = args[0]
	switch features.Action {
	case "list":
		if len(args) != 1 {
			return errors.New("features list does not accept arguments")
		}
	case "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("features %s requires FEATURE", features.Action)
		}
		features.Feature = args[1]
	default:
		return fmt.Errorf("unknown features subcommand %s", features.Action)
	}
	return nil
}

func parseMCP(args []string, mcp *MCPOptions) error {
	if len(args) == 0 {
		return errors.New("mcp requires a subcommand")
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
				return fmt.Errorf("unknown mcp list option %s", arg)
			default:
				return fmt.Errorf("mcp list does not accept argument %s", arg)
			}
		}
	case "get":
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
				return fmt.Errorf("unknown mcp get option %s", arg)
			default:
				if mcp.Name != "" {
					return errors.New("mcp get accepts exactly one NAME")
				}
				mcp.Name = arg
			}
		}
		if mcp.Name == "" {
			return errors.New("mcp get requires NAME")
		}
	case "add":
		if len(args) < 2 {
			return errors.New("mcp add requires NAME")
		}
		mcp.Name = args[1]
		for i := 2; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--":
				mcp.Command = append(mcp.Command, args[i+1:]...)
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
			case arg == "--oauth-resource":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				mcp.OAuthResource = value
				i = next
			case strings.HasPrefix(arg, "--oauth-resource="):
				mcp.OAuthResource = strings.TrimPrefix(arg, "--oauth-resource=")
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
				return fmt.Errorf("unknown mcp add option %s", arg)
			default:
				mcp.Command = append(mcp.Command, args[i:]...)
				i = len(args)
			}
		}
		if mcp.URL != "" && len(mcp.Command) > 0 {
			return errors.New("mcp add accepts either --url or COMMAND, not both")
		}
		if mcp.URL != "" && len(mcp.Env) > 0 {
			return errors.New("mcp add --env is only valid with stdio COMMAND")
		}
		if mcp.URL == "" && mcp.BearerTokenEnvVar != "" {
			return errors.New("mcp add --bearer-token-env-var requires --url")
		}
		if mcp.URL == "" && mcp.OAuthClientID != "" {
			return errors.New("mcp add --oauth-client-id requires --url")
		}
		if mcp.URL == "" && mcp.OAuthResource != "" {
			return errors.New("mcp add --oauth-resource requires --url")
		}
		if mcp.URL == "" && len(mcp.Command) == 0 {
			return errors.New("mcp add requires --url URL or COMMAND")
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
				return fmt.Errorf("unknown mcp %s option %s", mcp.Action, arg)
			default:
				if mcp.Name != "" {
					return fmt.Errorf("mcp %s accepts exactly one NAME", mcp.Action)
				}
				mcp.Name = arg
			}
		}
		if mcp.Name == "" {
			return fmt.Errorf("mcp %s requires NAME", mcp.Action)
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
				return fmt.Errorf("unknown mcp login option %s", arg)
			default:
				if mcp.Name != "" {
					return errors.New("mcp login accepts exactly one NAME")
				}
				mcp.Name = arg
			}
		}
		if mcp.Name == "" {
			return errors.New("mcp login requires NAME")
		}
	default:
		return fmt.Errorf("unknown mcp subcommand %s", mcp.Action)
	}
	return nil
}

func parsePlugin(args []string, plugin *PluginOptions) error {
	if len(args) == 0 {
		return errors.New("plugin requires a subcommand")
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
				return fmt.Errorf("unknown plugin %s option %s", plugin.Action, arg)
			default:
				if plugin.Plugin != "" {
					return fmt.Errorf("plugin %s accepts exactly one PLUGIN", plugin.Action)
				}
				plugin.Plugin = arg
			}
		}
		if plugin.Plugin == "" {
			return fmt.Errorf("plugin %s requires PLUGIN", plugin.Action)
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
				return fmt.Errorf("unknown plugin list option %s", arg)
			default:
				return fmt.Errorf("plugin list does not accept argument %s", arg)
			}
		}
		if plugin.Available && !plugin.JSON {
			return errors.New("plugin list --available requires --json")
		}
	case "marketplace":
		return parsePluginMarketplace(args[1:], &plugin.Marketplace)
	default:
		return fmt.Errorf("unknown plugin subcommand %s", plugin.Action)
	}
	return nil
}

func parsePluginMarketplace(args []string, marketplace *PluginMarketplaceOptions) error {
	if len(args) == 0 {
		return errors.New("plugin marketplace requires a subcommand")
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
				return fmt.Errorf("unknown plugin marketplace add option %s", arg)
			default:
				if marketplace.Source != "" {
					return errors.New("plugin marketplace add accepts exactly one SOURCE")
				}
				marketplace.Source = arg
			}
		}
		if marketplace.Source == "" {
			return errors.New("plugin marketplace add requires SOURCE")
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
				return fmt.Errorf("unknown plugin marketplace list option %s", arg)
			default:
				return fmt.Errorf("plugin marketplace list does not accept argument %s", arg)
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
				return fmt.Errorf("unknown plugin marketplace upgrade option %s", arg)
			default:
				if marketplace.Name != "" {
					return errors.New("plugin marketplace upgrade accepts at most one MARKETPLACE_NAME")
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
				return fmt.Errorf("unknown plugin marketplace remove option %s", arg)
			default:
				if marketplace.Name != "" {
					return errors.New("plugin marketplace remove accepts exactly one MARKETPLACE_NAME")
				}
				marketplace.Name = arg
			}
		}
		if marketplace.Name == "" {
			return errors.New("plugin marketplace remove requires MARKETPLACE_NAME")
		}
	default:
		return fmt.Errorf("unknown plugin marketplace subcommand %s", marketplace.Action)
	}
	return nil
}

func parseSandbox(args []string, sandbox *SandboxOptions) error {
	if len(args) > 0 && args[0] == "setup" {
		sandbox.Setup = true
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--elevated":
				sandbox.Elevated = true
			case arg == "--current-user":
				sandbox.CurrentUser = true
			case arg == "--user":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				sandbox.User = value
				i = next
			case strings.HasPrefix(arg, "--user="):
				sandbox.User = strings.TrimPrefix(arg, "--user=")
			case arg == "--codex-home":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				sandbox.CodexHome = value
				i = next
			case strings.HasPrefix(arg, "--codex-home="):
				sandbox.CodexHome = strings.TrimPrefix(arg, "--codex-home=")
			default:
				if strings.HasPrefix(arg, "-") {
					return fmt.Errorf("unknown sandbox setup option %s", arg)
				}
				return fmt.Errorf("sandbox setup does not accept argument %s", arg)
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
				return err
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
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			sandbox.ConfigProfile = value
			i = next
		case strings.HasPrefix(arg, "--profile="):
			sandbox.ConfigProfile = strings.TrimPrefix(arg, "--profile=")
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
			sandbox.CWD = filepath.Clean(strings.TrimPrefix(arg, "--cd="))
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
				return err
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
				return fmt.Errorf("unknown sandbox option %s", arg)
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
				return fmt.Errorf("unknown sandbox option %s", arg)
			}
			path, err := absoluteCLIPath(strings.TrimPrefix(arg, "--allow-unix-socket="))
			if err != nil {
				return err
			}
			sandbox.AllowUnixSockets = append(sandbox.AllowUnixSockets, path)
		case arg == "--log-denials":
			if runtime.GOOS != "darwin" {
				return fmt.Errorf("unknown sandbox option %s", arg)
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
			return fmt.Errorf("unknown sandbox option %s", arg)
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
	if sandbox.CurrentUser && strings.TrimSpace(sandbox.User) != "" {
		return errors.New("--user conflicts with --current-user")
	}
	if !sandbox.CurrentUser && strings.TrimSpace(sandbox.User) == "" {
		return errors.New("--user or --current-user is required")
	}
	if strings.TrimSpace(sandbox.User) != "" && strings.TrimSpace(sandbox.CodexHome) == "" {
		return errors.New("--codex-home is required with --user")
	}
	return nil
}

func parseExecpolicy(args []string, execpolicy *ExecpolicyOptions) error {
	if len(args) == 0 {
		return errors.New("execpolicy requires a subcommand")
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
		if len(execpolicy.Rules) == 0 {
			return errors.New("execpolicy check requires --rules PATH")
		}
		if len(execpolicy.Command) == 0 {
			return errors.New("execpolicy check requires COMMAND")
		}
	default:
		return fmt.Errorf("unknown execpolicy subcommand %s", execpolicy.Action)
	}
	return nil
}

func parseMCPServer(args []string, mcpServer *MCPServerOptions) error {
	for _, arg := range args {
		switch arg {
		case "--strict-config":
			mcpServer.StrictConfig = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown mcp-server option %s", arg)
			}
			return fmt.Errorf("mcp-server does not accept argument %s", arg)
		}
	}
	return nil
}

func parseCloud(args []string, cloud *CloudOptions) error {
	cloud.Attempts = 1
	cloud.Limit = 20
	if len(args) == 0 {
		cloud.Action = "tui"
		return nil
	}
	cloud.Action = args[0]
	switch cloud.Action {
	case "exec":
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
				attempts, err := parseBoundedInt(value, "attempts", 1, 4)
				if err != nil {
					return err
				}
				cloud.Attempts = attempts
				i = next
			case strings.HasPrefix(arg, "--attempts="):
				attempts, err := parseBoundedInt(strings.TrimPrefix(arg, "--attempts="), "attempts", 1, 4)
				if err != nil {
					return err
				}
				cloud.Attempts = attempts
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
				return fmt.Errorf("unknown cloud exec option %s", arg)
			default:
				cloud.Query = strings.Join(args[i:], " ")
				i = len(args)
			}
		}
		if cloud.Environment == "" {
			return errors.New("cloud exec requires --env ENV_ID")
		}
	case "status":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case strings.HasPrefix(arg, "-"):
				return fmt.Errorf("unknown cloud status option %s", arg)
			default:
				if cloud.TaskID != "" {
					return errors.New("cloud status accepts exactly one TASK_ID")
				}
				cloud.TaskID = arg
			}
		}
		if cloud.TaskID == "" {
			return errors.New("cloud status requires TASK_ID")
		}
	case "list":
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
				limit, err := parseBoundedInt64(value, "limit", 1, 20)
				if err != nil {
					return err
				}
				cloud.Limit = limit
				i = next
			case strings.HasPrefix(arg, "--limit="):
				limit, err := parseBoundedInt64(strings.TrimPrefix(arg, "--limit="), "limit", 1, 20)
				if err != nil {
					return err
				}
				cloud.Limit = limit
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
				return fmt.Errorf("unknown cloud list option %s", arg)
			default:
				return fmt.Errorf("cloud list does not accept argument %s", arg)
			}
		}
	case "apply", "diff":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--attempt":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				attempt, err := parseBoundedInt(value, "attempt", 1, 4)
				if err != nil {
					return err
				}
				cloud.Attempt = attempt
				i = next
			case strings.HasPrefix(arg, "--attempt="):
				attempt, err := parseBoundedInt(strings.TrimPrefix(arg, "--attempt="), "attempt", 1, 4)
				if err != nil {
					return err
				}
				cloud.Attempt = attempt
			case strings.HasPrefix(arg, "-"):
				return fmt.Errorf("unknown cloud %s option %s", cloud.Action, arg)
			default:
				if cloud.TaskID != "" {
					return fmt.Errorf("cloud %s accepts exactly one TASK_ID", cloud.Action)
				}
				cloud.TaskID = arg
			}
		}
		if cloud.TaskID == "" {
			return fmt.Errorf("cloud %s requires TASK_ID", cloud.Action)
		}
	default:
		return fmt.Errorf("unknown cloud subcommand %s", cloud.Action)
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
				return fmt.Errorf("unknown responses-api-proxy option %s", arg)
			}
			return fmt.Errorf("responses-api-proxy does not accept argument %s", arg)
		}
	}
	return nil
}

func parseStdioToUDS(args []string, stdio *StdioToUDSOptions) error {
	if len(args) != 1 {
		return errors.New("stdio-to-uds requires exactly one SOCKET_PATH")
	}
	stdio.SocketPath = args[0]
	return nil
}

func parseExecServer(args []string, execServer *ExecServerOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
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
				return err
			}
			execServer.Remote = value
			i = next
		case strings.HasPrefix(arg, "--remote="):
			execServer.Remote = strings.TrimPrefix(arg, "--remote=")
		case arg == "--environment-id":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			execServer.EnvironmentID = value
			i = next
		case strings.HasPrefix(arg, "--environment-id="):
			execServer.EnvironmentID = strings.TrimPrefix(arg, "--environment-id=")
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
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown exec-server option %s", arg)
			}
			return fmt.Errorf("exec-server does not accept argument %s", arg)
		}
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
	if execServer.Remote != "" && execServer.EnvironmentID == "" {
		return errors.New("--environment-id is required when --remote is set")
	}
	if execServer.Remote != "" && execServer.ListenSet {
		return errors.New("--listen conflicts with --remote")
	}
	if execServer.UseAgentIdentityAuth && execServer.Remote == "" {
		return errors.New("--use-agent-identity-auth requires --remote")
	}
	if execServer.ExitOnStdinClose && execServer.Remote == "" {
		return errors.New("--exit-on-stdin-close requires --remote")
	}
	return nil
}

func parseAppServer(args []string, appServer *AppServerOptions) error {
	listenSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--strict-config":
			appServer.StrictConfig = true
		case arg == "--listen":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.Listen = value
			listenSet = true
			i = next
		case strings.HasPrefix(arg, "--listen="):
			appServer.Listen = strings.TrimPrefix(arg, "--listen=")
			listenSet = true
		case arg == "--stdio":
			appServer.Stdio = true
		case arg == "--remote-control":
			appServer.RemoteControl = true
		case arg == "--analytics-default-enabled":
			appServer.AnalyticsDefaultEnabled = true
		case arg == "--code-mode-host":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			if err := validateAppServerCodeModeHostURL(value); err != nil {
				return err
			}
			appServer.CodeModeHostURL = value
			i = next
		case strings.HasPrefix(arg, "--code-mode-host="):
			value := strings.TrimPrefix(arg, "--code-mode-host=")
			if err := validateAppServerCodeModeHostURL(value); err != nil {
				return err
			}
			appServer.CodeModeHostURL = value
		case arg == "--ws-auth":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.WSAuth = value
			appServer.WSAuthSet = true
			i = next
		case strings.HasPrefix(arg, "--ws-auth="):
			appServer.WSAuth = strings.TrimPrefix(arg, "--ws-auth=")
			appServer.WSAuthSet = true
		case arg == "--ws-token-file":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.WSTokenFile = value
			appServer.WSTokenFileSet = true
			i = next
		case strings.HasPrefix(arg, "--ws-token-file="):
			appServer.WSTokenFile = strings.TrimPrefix(arg, "--ws-token-file=")
			appServer.WSTokenFileSet = true
		case arg == "--ws-token-sha256":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.WSTokenSHA256 = value
			appServer.WSTokenSHA256Set = true
			i = next
		case strings.HasPrefix(arg, "--ws-token-sha256="):
			appServer.WSTokenSHA256 = strings.TrimPrefix(arg, "--ws-token-sha256=")
			appServer.WSTokenSHA256Set = true
		case arg == "--ws-shared-secret-file":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.WSSharedSecretFile = value
			appServer.WSSharedSecretFileSet = true
			i = next
		case strings.HasPrefix(arg, "--ws-shared-secret-file="):
			appServer.WSSharedSecretFile = strings.TrimPrefix(arg, "--ws-shared-secret-file=")
			appServer.WSSharedSecretFileSet = true
		case arg == "--ws-issuer":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.WSIssuer = value
			appServer.WSIssuerSet = true
			i = next
		case strings.HasPrefix(arg, "--ws-issuer="):
			appServer.WSIssuer = strings.TrimPrefix(arg, "--ws-issuer=")
			appServer.WSIssuerSet = true
		case arg == "--ws-audience":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			appServer.WSAudience = value
			appServer.WSAudienceSet = true
			i = next
		case strings.HasPrefix(arg, "--ws-audience="):
			appServer.WSAudience = strings.TrimPrefix(arg, "--ws-audience=")
			appServer.WSAudienceSet = true
		case arg == "--ws-max-clock-skew-seconds":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			parsed, err := parseUint64Flag(arg, value)
			if err != nil {
				return err
			}
			appServer.WSMaxClockSkewSeconds = &parsed
			i = next
		case strings.HasPrefix(arg, "--ws-max-clock-skew-seconds="):
			value := strings.TrimPrefix(arg, "--ws-max-clock-skew-seconds=")
			parsed, err := parseUint64Flag("--ws-max-clock-skew-seconds", value)
			if err != nil {
				return err
			}
			appServer.WSMaxClockSkewSeconds = &parsed
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown app-server option %s", arg)
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
				return fmt.Errorf("unknown app-server subcommand %s", appServer.Subcommand[0])
			}
			if err != nil {
				return err
			}
			i = len(args)
		}
	}
	if appServer.Stdio && listenSet {
		return errors.New("--stdio conflicts with --listen")
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
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid websocket URL: %w", err)
	}
	if (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Hostname() == "" {
		return errors.New("code-mode host URL must use ws:// or wss:// with a host")
	}
	if parsed.Fragment != "" {
		return errors.New("code-mode host URL must not contain a fragment")
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
			if err := validateAppServerWebsocketAuthPath("--ws-token-file", o.WSTokenFile); err != nil {
				return err
			}
		}
		if o.WSTokenSHA256Set {
			trimmed, err := validateAppServerWebsocketSHA256("--ws-token-sha256", o.WSTokenSHA256)
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
		if err := validateAppServerWebsocketAuthPath("--ws-shared-secret-file", o.WSSharedSecretFile); err != nil {
			return err
		}
		o.WSIssuer = strings.TrimSpace(o.WSIssuer)
		o.WSAudience = strings.TrimSpace(o.WSAudience)
	default:
		return fmt.Errorf("unknown --ws-auth mode %q", mode)
	}
	return nil
}

func validateAppServerWebsocketAuthPath(flagName string, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", flagName)
	}
	return nil
}

func validateAppServerWebsocketSHA256(flagName string, value string) (string, error) {
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
		return errors.New("app-server daemon requires a subcommand")
	}
	daemon.Action = args[0]
	switch daemon.Action {
	case "start", "restart", "enable-remote-control", "disable-remote-control", "stop", "version", "pid-update-loop":
		if len(args) != 1 {
			return fmt.Errorf("app-server daemon %s does not accept arguments", daemon.Action)
		}
	case "bootstrap":
		for _, arg := range args[1:] {
			switch arg {
			case "--remote-control":
				daemon.RemoteControl = true
			default:
				if strings.HasPrefix(arg, "-") {
					return fmt.Errorf("unknown app-server daemon bootstrap option %s", arg)
				}
				return fmt.Errorf("app-server daemon bootstrap does not accept argument %s", arg)
			}
		}
	default:
		return fmt.Errorf("unknown app-server daemon subcommand %s", daemon.Action)
	}
	return nil
}

func parseQueue(args []string, queue *QueueOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOption(args, &i, &queue.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		switch {
		case arg == "--thread":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.Thread = value
			i = next
		case strings.HasPrefix(arg, "--thread="):
			queue.Thread = strings.TrimPrefix(arg, "--thread=")
		case arg == "--message":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			queue.Message = value
			i = next
		case strings.HasPrefix(arg, "--message="):
			queue.Message = strings.TrimPrefix(arg, "--message=")
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
			return fmt.Errorf("unknown queue option %s", arg)
		default:
			return fmt.Errorf("`codex queue` does not accept positional arguments")
		}
	}
	if strings.TrimSpace(queue.Thread) == "" {
		return errors.New("`codex queue` requires --thread <THREAD>")
	}
	if strings.TrimSpace(queue.Message) == "" {
		return errors.New("`codex queue` requires --message <TEXT>")
	}
	return nil
}

func parseAgents(args []string, agents *AgentsOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
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
		case arg == "--strict-config":
			agents.StrictConfig = true
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
			// Rust #39114: invocation-specific session overrides cannot apply
			// to shared sessions, so positional args and other options are
			// rejected.
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown agents option %s", arg)
			}
			return fmt.Errorf("`codex agents` does not accept argument %s", arg)
		}
	}
	return nil
}

func parseAppServerProxy(args []string, proxy *AppServerProxyOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
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
				return fmt.Errorf("unknown app-server proxy option %s", arg)
			}
			return fmt.Errorf("app-server proxy does not accept argument %s", arg)
		}
	}
	return nil
}

func parseAppServerGenerate(action string, args []string, generate *AppServerGenerateOptions) error {
	generate.Action = action
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--out" || arg == "-o":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			generate.OutDir = value
			i = next
		case strings.HasPrefix(arg, "--out="):
			generate.OutDir = strings.TrimPrefix(arg, "--out=")
		case strings.HasPrefix(arg, "-o") && arg != "-o":
			generate.OutDir = strings.TrimPrefix(arg, "-o")
		case action == "generate-ts" && (arg == "--prettier" || arg == "-p"):
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return err
			}
			generate.Prettier = value
			i = next
		case action == "generate-ts" && strings.HasPrefix(arg, "--prettier="):
			generate.Prettier = strings.TrimPrefix(arg, "--prettier=")
		case action == "generate-ts" && strings.HasPrefix(arg, "-p") && arg != "-p":
			generate.Prettier = strings.TrimPrefix(arg, "-p")
		case action != "generate-internal-json-schema" && arg == "--experimental":
			generate.Experimental = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown app-server %s option %s", action, arg)
			}
			return fmt.Errorf("app-server %s does not accept argument %s", action, arg)
		}
	}
	if generate.OutDir == "" {
		return fmt.Errorf("app-server %s requires --out DIR", action)
	}
	return nil
}

func parseApp(args []string, app *AppOptions) error {
	app.Path = "."
	for i := 0; i < len(args); i++ {
		arg := args[i]
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
			return fmt.Errorf("unknown app option %s", arg)
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
	_ = update
	if len(args) == 0 {
		return nil
	}
	arg := args[0]
	if strings.HasPrefix(arg, "-") {
		return fmt.Errorf("unknown update option %s", arg)
	}
	return fmt.Errorf("update does not accept argument %s", arg)
}

func parseCompletion(args []string, completion *CompletionOptions) error {
	if len(args) > 1 {
		return errors.New("completion accepts at most one shell")
	}
	if len(args) == 1 {
		completion.Shell = args[0]
	}
	return nil
}

func parseDoctor(args []string, doctor *DoctorOptions) error {
	for _, arg := range args {
		switch arg {
		case "--json":
			doctor.JSON = true
		case "--summary":
			doctor.Summary = true
		case "--all":
			doctor.All = true
		case "--no-color":
			doctor.NoColor = true
		case "--ascii":
			doctor.ASCII = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown doctor option %s", arg)
			}
			return fmt.Errorf("doctor does not accept argument %s", arg)
		}
	}
	return nil
}

func parseMigrateRollouts(args []string, options *MigrateRolloutsOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--apply":
			options.Apply = true
		case arg == "--json":
			options.JSON = true
		case arg == "--verbose":
			options.Verbose = true
		case arg == "--thread" || strings.HasPrefix(arg, "--thread="):
			value, next, err := optionalFlagValue(args, i, arg, "--thread")
			if err != nil {
				return err
			}
			options.Threads = append(options.Threads, value)
			i = next
		case arg == "--max-mib-per-second" || strings.HasPrefix(arg, "--max-mib-per-second="):
			value, next, err := optionalFlagValue(args, i, arg, "--max-mib-per-second")
			if err != nil {
				return err
			}
			parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil || parsed == 0 {
				return fmt.Errorf("--max-mib-per-second must be a positive integer")
			}
			options.MaxMibPerSecond = parsed
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown migrate-rollouts option %s", arg)
			}
			return fmt.Errorf("migrate-rollouts does not accept argument %s", arg)
		}
	}
	return nil
}

func optionalFlagValue(args []string, index int, arg string, name string) (string, int, error) {
	if strings.HasPrefix(arg, name+"=") {
		return strings.TrimPrefix(arg, name+"="), index, nil
	}
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", name)
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
			return fmt.Errorf("unknown apply option %s", arg)
		default:
			if i+1 < len(args) {
				return errors.New("apply accepts at most one PATCH")
			}
			apply.Patch = arg
			return nil
		}
	}
	return nil
}

func parseRemoteControl(args []string, remote *RemoteControlOptions) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			remote.JSON = true
		case "start", "stop", "pair":
			if remote.Subcommand != "" {
				return errors.New("remote-control accepts at most one subcommand")
			}
			remote.Subcommand = arg
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown remote-control option %s", arg)
			}
			return fmt.Errorf("unknown remote-control subcommand %s", arg)
		}
	}
	return nil
}

func parseSessionSelection(args []string, session *SessionOptions, includeNonInteractive bool) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOption(args, &i, &session.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		if handled, err := parseTUIOnlyOption(args, &i, &session.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		if arg == "--" {
			session.Prompt = strings.Join(args[i+1:], " ")
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
			return fmt.Errorf("unknown session option %s", arg)
		default:
			if session.Last {
				if session.Target != "" || len(args[i:]) > 1 {
					return errors.New("--last conflicts with SESSION_ID and prompt")
				}
				session.Prompt = arg
				return nil
			}
			if session.Target == "" {
				session.Target = arg
				continue
			}
			session.Prompt = strings.Join(args[i:], " ")
			return nil
		}
	}
	if session.Last && session.Target != "" {
		session.Prompt = session.Target
		session.Target = ""
	}
	return nil
}

func parseSessionMutation(args []string, session *SessionOptions, command string, allowForce bool) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := parseSharedOption(args, &i, &session.Shared); err != nil {
			return err
		} else if handled {
			continue
		}
		switch {
		case arg == "--force":
			if !allowForce {
				return fmt.Errorf("--force is not supported for %s", command)
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
			return fmt.Errorf("unknown %s option %s", command, arg)
		default:
			if session.Target != "" {
				return fmt.Errorf("%s accepts exactly one SESSION", command)
			}
			session.Target = arg
		}
	}
	if session.Target == "" {
		return fmt.Errorf("%s requires SESSION", command)
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
		return errors.New("debug requires a subcommand")
	}
	debug.Subcommand = args[0]
	switch debug.Subcommand {
	case "models":
		for _, arg := range args[1:] {
			switch arg {
			case "--bundled":
				debug.BundledModels = true
			default:
				if strings.HasPrefix(arg, "-") {
					return fmt.Errorf("unknown debug models option %s", arg)
				}
				return fmt.Errorf("debug models does not accept argument %s", arg)
			}
		}
	case "app-server":
		if len(args) < 2 {
			return errors.New("debug app-server requires a subcommand")
		}
		debug.AppServerAction = args[1]
		switch debug.AppServerAction {
		case "send-message-v2":
			if len(args) != 3 {
				return errors.New("debug app-server send-message-v2 requires USER_MESSAGE")
			}
			debug.AppServerMessage = args[2]
		default:
			return fmt.Errorf("unknown debug app-server subcommand %s", debug.AppServerAction)
		}
	case "prompt-input":
		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--image" || arg == "-i":
				value, next, err := requireValue(args, i, arg)
				if err != nil {
					return err
				}
				debug.Images = append(debug.Images, splitComma(value)...)
				i = next
			case strings.HasPrefix(arg, "--image="):
				debug.Images = append(debug.Images, splitComma(strings.TrimPrefix(arg, "--image="))...)
			case strings.HasPrefix(arg, "-"):
				return fmt.Errorf("unknown debug prompt-input option %s", arg)
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
			case strings.HasPrefix(arg, "-"):
				return fmt.Errorf("unknown debug trace-reduce option %s", arg)
			default:
				if debug.TraceBundle != "" {
					return errors.New("debug trace-reduce accepts exactly one TRACE_BUNDLE")
				}
				debug.TraceBundle = arg
			}
		}
		if debug.TraceBundle == "" {
			return errors.New("debug trace-reduce requires TRACE_BUNDLE")
		}
	case "clear-memories":
		if len(args) != 1 {
			return errors.New("debug clear-memories does not accept arguments")
		}
	case "config":
		if len(args) != 1 {
			return errors.New("debug config does not accept arguments")
		}
	default:
		return fmt.Errorf("unknown debug subcommand %s", debug.Subcommand)
	}
	return nil
}

func parseSharedOption(args []string, i *int, shared *SharedOptions) (bool, error) {
	arg := args[*i]
	switch {
	case arg == "--image" || arg == "-i":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.Images = append(shared.Images, splitComma(value)...)
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--image="):
		shared.Images = append(shared.Images, splitComma(strings.TrimPrefix(arg, "--image="))...)
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
		shared.Profile = strings.TrimPrefix(arg, "--profile=")
		return true, nil
	case strings.HasPrefix(arg, "-p") && arg != "-p":
		shared.Profile = strings.TrimPrefix(arg, "-p")
		return true, nil
	case arg == "--sandbox" || arg == "-s":
		value, next, err := requireValue(args, *i, arg)
		if err != nil {
			return false, err
		}
		shared.Sandbox = value
		*i = next
		return true, nil
	case strings.HasPrefix(arg, "--sandbox="):
		shared.Sandbox = strings.TrimPrefix(arg, "--sandbox=")
		return true, nil
	case strings.HasPrefix(arg, "-s") && arg != "-s":
		shared.Sandbox = strings.TrimPrefix(arg, "-s")
		return true, nil
	case arg == "--dangerously-bypass-approvals-and-sandbox" || arg == "--yolo":
		shared.DangerouslyBypassApprovalsAndSandbox = true
		return true, nil
	case arg == "--dangerously-bypass-hook-trust":
		shared.DangerouslyBypassHookTrust = true
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
		shared.CWD = filepath.Clean(strings.TrimPrefix(arg, "--cd="))
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
		shared.AddDirs = append(shared.AddDirs, filepath.Clean(strings.TrimPrefix(arg, "--add-dir=")))
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
	default:
		return false, nil
	}
}

func setApprovalPolicy(shared *SharedOptions, value string) error {
	value = strings.TrimSpace(value)
	switch value {
	case "untrusted", "on-request", "never":
		shared.ApprovalPolicy = value
		return nil
	default:
		return errors.New("--ask-for-approval must be one of untrusted, on-request, never")
	}
}

func setExecColor(exec *ExecOptions, value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always", "never", "auto":
		exec.Color = strings.ToLower(strings.TrimSpace(value))
		return nil
	default:
		return errors.New("--color must be one of always, never, auto")
	}
}

func requireValue(args []string, i int, flag string) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("%s requires a value", flag)
	}
	return args[i+1], i + 1, nil
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

func parseUint64Flag(flag string, value string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer", flag)
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
