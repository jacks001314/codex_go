package tui

type SlashCommandFrame struct {
	Name        string
	Command     Command
	Aliases     []string
	Description string
}

func SlashCommandNames() []string {
	frames := SlashCommandFrames()
	out := make([]string, 0, len(frames))
	for _, frame := range frames {
		out = append(out, frame.Name)
	}
	return out
}

func SlashCommandFrames() []SlashCommandFrame {
	return []SlashCommandFrame{
		{Name: "help", Command: CommandHelp, Aliases: []string{"?"}, Description: "show this command list"},
		{Name: "keymap", Command: CommandKeymap, Aliases: []string{"keys"}, Description: "show or edit key bindings"},
		{Name: "status", Command: CommandStatus, Description: "show current session status"},
		{Name: "usage", Command: CommandUsage, Description: "show account usage or token activity"},
		{Name: "goal", Command: CommandGoal, Description: "show or manage the current thread goal"},
		{Name: "statusline", Command: CommandStatusline, Description: "configure status line items"},
		{Name: "title", Command: CommandTitle, Description: "configure terminal title items"},
		{Name: "debug-config", Command: CommandDebugConfig, Description: "show config layers and requirements"},
		{Name: "new", Command: CommandNew, Description: "start a fresh local thread"},
		{Name: "init", Command: CommandInit, Description: "create AGENTS.md guidance"},
		{Name: "compact", Command: CommandCompact, Description: "compact conversation context"},
		{Name: "clear", Command: CommandClear, Description: "clear the visible transcript"},
		{Name: "copy", Command: CommandCopy, Description: "copy the last agent response"},
		{Name: "raw", Command: CommandRaw, Description: "toggle raw scrollback mode"},
		{Name: "diff", Command: CommandDiff, Description: "show git diff"},
		{Name: "ps", Command: CommandPs, Description: "list background terminals"},
		{Name: "stop", Command: CommandStop, Aliases: []string{"clean"}, Description: "stop background terminals"},
		{Name: "model", Command: CommandModel, Description: "show or set model"},
		{Name: "personality", Command: CommandPersonality, Description: "choose communication style"},
		{Name: "plan", Command: CommandPlan, Description: "switch to Plan mode"},
		{Name: "agent", Command: CommandAgent, Aliases: []string{"agents", "subagents"}, Description: "switch active agent thread"},
		{Name: "side", Command: CommandSide, Aliases: []string{"btw"}, Description: "start a side conversation"},
		{Name: "permissions", Command: CommandPermissions, Description: "open permissions menu"},
		{Name: "approval", Command: CommandApproval, Description: "show or set approval policy"},
		{Name: "sandbox", Command: CommandSandbox, Description: "show or set sandbox profile"},
		{Name: "experimental", Command: CommandExperimental, Description: "toggle experimental features"},
		{Name: "review", Command: CommandReview, Description: "review current changes"},
		{Name: "rename", Command: CommandRename, Description: "rename current thread"},
		{Name: "mention", Command: CommandMention, Description: "mention files or context"},
		{Name: "skills", Command: CommandSkills, Description: "list or manage skills"},
		{Name: "hooks", Command: CommandHooks, Description: "view and manage lifecycle hooks"},
		{Name: "mcp", Command: CommandMcp, Description: "list configured MCP tools"},
		{Name: "apps", Command: CommandApps, Description: "manage apps"},
		{Name: "plugins", Command: CommandPlugins, Description: "browse plugins"},
		{Name: "theme", Command: CommandTheme, Description: "choose syntax theme"},
		{Name: "pets", Command: CommandPets, Aliases: []string{"pet"}, Description: "choose or hide the terminal pet"},
		{Name: "ide", Command: CommandIde, Description: "inspect IDE context"},
		{Name: "vim", Command: CommandVim, Description: "toggle Vim mode"},
		{Name: "approve", Command: CommandAutoReview, Description: "approve automatically when available"},
		{Name: "memories", Command: CommandMemories, Description: "configure memory use"},
		{Name: "feedback", Command: CommandFeedback, Description: "send logs to maintainers"},
		{Name: "app", Command: CommandApp, Description: "open app integration"},
		{Name: "import", Command: CommandImport, Description: "import setup and recent chats"},
		{Name: "setup-default-sandbox", Command: CommandElevateSandbox, Description: "set up elevated agent sandbox"},
		{Name: "sandbox-add-read-dir", Command: CommandSandboxReadRoot, Description: "let sandbox read a directory"},
		{Name: "rollout", Command: CommandRollout, Description: "inspect rollout state"},
		{Name: "test-approval", Command: CommandTestApproval, Description: "debug approval flow"},
		{Name: "debug-m-drop", Command: CommandMemoryDrop, Description: "debug memory drop"},
		{Name: "debug-m-update", Command: CommandMemoryUpdate, Description: "debug memory update"},
		{Name: "resume", Command: CommandResume, Description: "choose a previous session to resume"},
		{Name: "fork", Command: CommandFork, Description: "choose a previous session to fork"},
		{Name: "archive", Command: CommandArchive, Description: "archive a previous session"},
		{Name: "unarchive", Command: CommandUnarchive, Description: "unarchive a previous session"},
		{Name: "delete", Command: CommandDelete, Description: "delete a previous session"},
		{Name: "attach", Command: CommandAttach, Description: "attach a file path"},
		{Name: "image", Command: CommandImage, Description: "attach a local image"},
		{Name: "url-image", Command: CommandURLImage, Description: "attach a remote image URL"},
		{Name: "clear-attachments", Command: CommandClearAttachments, Description: "remove prompt attachments"},
		{Name: "editor", Command: CommandEditor, Description: "edit current draft in external editor"},
		{Name: "logout", Command: CommandLogout, Description: "log out of Codex"},
		{Name: "exit", Command: CommandExit, Aliases: []string{"quit"}, Description: "quit"},
	}
}
