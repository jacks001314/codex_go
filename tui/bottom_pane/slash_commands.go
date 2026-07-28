package bottompane

import (
	"runtime"
	"strings"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/slash_commands.rs.

type SlashCommandPopupItem struct {
	Command string
	Args    string
}

type ServiceTierCommand struct {
	ID          string
	Name        string
	Description string
}

type SlashCommandItemKind string

const (
	SlashCommandItemBuiltin     SlashCommandItemKind = "builtin"
	SlashCommandItemServiceTier SlashCommandItemKind = "service_tier"
)

type SlashCommandItem struct {
	Kind        SlashCommandItemKind
	Name        string
	Command     codextui.Command
	Aliases     []string
	Description string
	IsAlias     bool
	ServiceTier *ServiceTierCommand
}

func (i SlashCommandItem) CommandText() string {
	return strings.TrimSpace(i.Name)
}

func (i SlashCommandItem) SupportsInlineArgs() bool {
	if i.Kind == SlashCommandItemServiceTier {
		return false
	}
	switch i.Command {
	case codextui.CommandReview,
		codextui.CommandRename,
		codextui.CommandNew,
		codextui.CommandClear,
		codextui.CommandPlan,
		codextui.CommandGoal,
		codextui.CommandIde,
		codextui.CommandKeymap,
		codextui.CommandMcp,
		codextui.CommandRaw,
		codextui.CommandUsage,
		codextui.CommandPets,
		codextui.CommandSide,
		codextui.CommandResume,
		codextui.CommandSandboxReadRoot:
		return true
	default:
		return false
	}
}

func (i SlashCommandItem) AvailableInSideConversation() bool {
	if i.Kind == SlashCommandItemServiceTier {
		return false
	}
	return sideConversationCommandAllowed(i.Command)
}

func (i SlashCommandItem) AvailableDuringTask() bool {
	if i.Kind == SlashCommandItemServiceTier {
		return true
	}
	switch i.Command {
	case codextui.CommandNew,
		codextui.CommandArchive,
		codextui.CommandDelete,
		codextui.CommandFork,
		codextui.CommandInit,
		codextui.CommandCompact,
		codextui.CommandKeymap,
		codextui.CommandVim,
		codextui.CommandElevateSandbox,
		codextui.CommandSandboxReadRoot,
		codextui.CommandExperimental,
		codextui.CommandMemories,
		codextui.CommandImport,
		codextui.CommandReview,
		codextui.CommandPlan,
		codextui.CommandClear,
		codextui.CommandLogout,
		codextui.CommandMemoryDrop,
		codextui.CommandMemoryUpdate,
		codextui.CommandTheme,
		codextui.CommandPets:
		return false
	default:
		return true
	}
}

type BuiltinCommandFlags struct {
	CollaborationModesEnabled   bool
	ConnectorsEnabled           bool
	PluginsCommandEnabled       bool
	TokenActivityCommandEnabled bool
	ServiceTierCommandsEnabled  bool
	GoalCommandEnabled          bool
	PersonalityCommandEnabled   bool
	AllowElevateSandbox         bool
	SideConversationActive      bool
}

func BuiltinsForInput(flags BuiltinCommandFlags) []SlashCommandItem {
	frames := slashCommandFramesByName()
	out := []SlashCommandItem{}
	for _, name := range rustSlashCommandOrder {
		frame, ok := frames[name]
		if !ok {
			continue
		}
		out = appendBuiltinFrameIfAvailable(out, frame, false, flags)
	}
	return out
}

func CommandsForInput(flags BuiltinCommandFlags, serviceTierCommands []ServiceTierCommand) []SlashCommandItem {
	commands := []SlashCommandItem{}
	tiersEnabled := flags.ServiceTierCommandsEnabled
	for _, item := range BuiltinsForInput(flags) {
		commands = append(commands, item)
		if item.Command == codextui.CommandModel && !item.IsAlias && tiersEnabled {
			for _, tier := range serviceTierCommands {
				tierCopy := tier
				commands = append(commands, SlashCommandItem{
					Kind:        SlashCommandItemServiceTier,
					Name:        tier.Name,
					Description: tier.Description,
					ServiceTier: &tierCopy,
				})
			}
		}
	}
	if !flags.SideConversationActive {
		return commands
	}
	filtered := commands[:0]
	for _, item := range commands {
		if item.AvailableInSideConversation() {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func FindBuiltinCommand(name string, flags BuiltinCommandFlags) (SlashCommandItem, bool) {
	name = normalizeSlashCommandName(name)
	if repeatedGoalAlias(name) {
		name = "goal"
	}
	lookupFlags := flags
	lookupFlags.TokenActivityCommandEnabled = true
	lookupFlags.SideConversationActive = false
	for _, frame := range codextui.SlashCommandFrames() {
		isAlias := false
		matches := frame.Name == name
		if !matches {
			for _, alias := range frame.Aliases {
				if alias == name {
					matches = true
					isAlias = true
					break
				}
			}
		}
		if !matches || !builtinFrameAvailable(frame, lookupFlags) {
			continue
		}
		item := SlashCommandItem{
			Kind:        SlashCommandItemBuiltin,
			Name:        frame.Name,
			Command:     frame.Command,
			Aliases:     append([]string(nil), frame.Aliases...),
			Description: slashCommandDescription(frame),
			IsAlias:     isAlias,
		}
		if isAlias {
			item.Name = name
		}
		return item, true
	}
	return SlashCommandItem{}, false
}

func FindSlashCommand(name string, flags BuiltinCommandFlags, serviceTierCommands []ServiceTierCommand) (SlashCommandItem, bool) {
	if command, ok := FindBuiltinCommand(name, flags); ok {
		return command, true
	}
	if !flags.ServiceTierCommandsEnabled {
		return SlashCommandItem{}, false
	}
	name = normalizeSlashCommandName(name)
	for _, command := range serviceTierCommands {
		if command.Name == name {
			commandCopy := command
			return SlashCommandItem{
				Kind:        SlashCommandItemServiceTier,
				Name:        command.Name,
				Description: command.Description,
				ServiceTier: &commandCopy,
			}, true
		}
	}
	return SlashCommandItem{}, false
}

func HasSlashCommandPrefix(name string, flags BuiltinCommandFlags, serviceTierCommands []ServiceTierCommand) bool {
	name = normalizeSlashCommandName(name)
	for _, command := range CommandsForInput(flags, serviceTierCommands) {
		commandText := command.CommandText()
		if command.IsAlias && commandText != "quit" && commandText != "btw" {
			continue
		}
		if fuzzyCommandMatch(commandText, name) {
			return true
		}
	}
	return false
}

func appendBuiltinFrameIfAvailable(out []SlashCommandItem, frame codextui.SlashCommandFrame, isAlias bool, flags BuiltinCommandFlags) []SlashCommandItem {
	if !builtinFrameAvailable(frame, flags) {
		return out
	}
	return append(out, SlashCommandItem{
		Kind:        SlashCommandItemBuiltin,
		Name:        frame.Name,
		Command:     frame.Command,
		Aliases:     append([]string(nil), frame.Aliases...),
		Description: slashCommandDescription(frame),
		IsAlias:     isAlias,
	})
}

func builtinFrameAvailable(frame codextui.SlashCommandFrame, flags BuiltinCommandFlags) bool {
	switch frame.Command {
	case codextui.CommandSandboxReadRoot:
		if runtime.GOOS != "windows" {
			return false
		}
	case codextui.CommandApp:
		if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
			return false
		}
	case codextui.CommandElevateSandbox:
		if !flags.AllowElevateSandbox {
			return false
		}
	case codextui.CommandPlan:
		if !flags.CollaborationModesEnabled {
			return false
		}
	case codextui.CommandApps:
		if !flags.ConnectorsEnabled {
			return false
		}
	case codextui.CommandPlugins:
		if !flags.PluginsCommandEnabled {
			return false
		}
	case codextui.CommandUsage:
		if !flags.TokenActivityCommandEnabled {
			return false
		}
	case codextui.CommandGoal:
		if !flags.GoalCommandEnabled {
			return false
		}
	case codextui.CommandPersonality:
		if !flags.PersonalityCommandEnabled {
			return false
		}
	}
	if flags.SideConversationActive && !sideConversationCommandAllowed(frame.Command) {
		return false
	}
	return true
}

func slashCommandFramesByName() map[string]codextui.SlashCommandFrame {
	frames := map[string]codextui.SlashCommandFrame{}
	for _, frame := range codextui.SlashCommandFrames() {
		frames[frame.Name] = frame
	}
	return frames
}

func slashCommandDescription(frame codextui.SlashCommandFrame) string {
	if override := rustSlashCommandDescriptions[frame.Name]; override != "" {
		return override
	}
	return frame.Description
}

func normalizeSlashCommandName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.TrimPrefix(name, "/")
	return name
}

func repeatedGoalAlias(name string) bool {
	rest, ok := strings.CutPrefix(name, "g")
	if !ok {
		return false
	}
	middle, ok := strings.CutSuffix(rest, "al")
	if !ok || middle == "" {
		return false
	}
	for _, r := range middle {
		if r != 'o' {
			return false
		}
	}
	return true
}

func fuzzyCommandMatch(command string, query string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	cursor := 0
	for _, r := range command {
		if cursor < len(query) && byte(r) == query[cursor] {
			cursor++
		}
	}
	return cursor == len(query)
}

func sideConversationCommandAllowed(command codextui.Command) bool {
	switch command {
	case codextui.CommandIde,
		codextui.CommandCopy,
		codextui.CommandRaw,
		codextui.CommandDiff,
		codextui.CommandMention,
		codextui.CommandStatus,
		codextui.CommandUsage:
		return true
	default:
		return false
	}
}

var rustSlashCommandOrder = []string{
	"model",
	"ide",
	"permissions",
	"keymap",
	"vim",
	"setup-default-sandbox",
	"sandbox-add-read-dir",
	"experimental",
	"approve",
	"memories",
	"skills",
	"import",
	"hooks",
	"review",
	"rename",
	"new",
	"archive",
	"delete",
	"resume",
	"fork",
	"app",
	"init",
	"compact",
	"plan",
	"goal",
	"agent",
	"side",
	"btw",
	"copy",
	"raw",
	"diff",
	"mention",
	"status",
	"usage",
	"debug-config",
	"title",
	"statusline",
	"theme",
	"pets",
	"mcp",
	"apps",
	"plugins",
	"logout",
	"quit",
	"exit",
	"feedback",
	"rollout",
	"ps",
	"stop",
	"clear",
	"personality",
	"test-approval",
	"subagents",
	"debug-m-drop",
	"debug-m-update",
}

var rustSlashCommandDescriptions = map[string]string{
	"model":                 "choose what model and reasoning effort to use",
	"ide":                   "include current selection, open files, and other context from your IDE",
	"permissions":           "choose what Codex is allowed to do",
	"keymap":                "remap TUI shortcuts",
	"vim":                   "toggle Vim mode for the composer",
	"setup-default-sandbox": "set up elevated agent sandbox",
	"sandbox-add-read-dir":  "let sandbox read a directory: /sandbox-add-read-dir <absolute_path>",
	"experimental":          "toggle experimental features",
	"approve":               "approve one retry of a recent auto-review denial",
	"memories":              "configure memory use and generation",
	"skills":                "use skills to improve how Codex performs specific tasks",
	"import":                "import setup, this project, and recent chats from Claude Code",
	"hooks":                 "view and manage lifecycle hooks",
	"review":                "review my current changes and find issues",
	"rename":                "rename the current thread",
	"new":                   "start a new chat during a conversation",
	"archive":               "archive this session and exit",
	"delete":                "permanently delete this session and exit",
	"resume":                "resume a saved chat",
	"fork":                  "fork the current chat",
	"app":                   "continue this session in Codex Desktop",
	"init":                  "create an AGENTS.md file with instructions for Codex",
	"compact":               "summarize conversation to prevent hitting the context limit",
	"plan":                  "switch to Plan mode",
	"goal":                  "set or view the goal for a long-running task",
	"agent":                 "switch the active agent thread",
	"side":                  "start a side conversation in an ephemeral fork",
	"btw":                   "start a side conversation in an ephemeral fork",
	"copy":                  "copy last response as markdown",
	"raw":                   "toggle raw scrollback mode for copy-friendly terminal selection",
	"diff":                  "show git diff (including untracked files)",
	"mention":               "mention a file",
	"status":                "show current session configuration and token usage",
	"usage":                 "view account usage or use a usage limit reset",
	"debug-config":          "show config layers and requirement sources for debugging",
	"title":                 "configure which items appear in the terminal title",
	"statusline":            "configure which items appear in the status line",
	"theme":                 "choose a syntax highlighting theme",
	"pets":                  "choose or hide the terminal pet",
	"mcp":                   "list configured MCP tools; use /mcp verbose for details",
	"apps":                  "manage apps",
	"plugins":               "browse plugins",
	"logout":                "log out of Codex",
	"quit":                  "exit Codex",
	"exit":                  "exit Codex",
	"feedback":              "send logs to maintainers",
	"rollout":               "print the rollout file path",
	"ps":                    "list background terminals",
	"stop":                  "stop all background terminals",
	"clear":                 "clear the terminal and start a new chat",
	"personality":           "choose a communication style for Codex",
	"test-approval":         "test approval request",
	"subagents":             "switch the active agent thread",
	"debug-m-drop":          "DO NOT USE",
	"debug-m-update":        "DO NOT USE",
}
