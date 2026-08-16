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
		{Name: "keymap", Command: CommandKeymap, Aliases: []string{"keys"}, Description: "remap TUI shortcuts"},
		{Name: "status", Command: CommandStatus, Description: "show current session configuration and token usage"},
		{Name: "usage", Command: CommandUsage, Description: "view account usage or use a usage limit reset"},
		{Name: "goal", Command: CommandGoal, Description: "set or view the goal for a long-running task"},
		{Name: "statusline", Command: CommandStatusline, Description: "configure status line items"},
		{Name: "title", Command: CommandTitle, Description: "configure terminal title items"},
		{Name: "cd", Command: CommandCd, Description: "change the current working directory"},
		{Name: "pwd", Command: CommandPwd, Aliases: []string{"cwd"}, Description: "show the current working directory"},
		{Name: "debug-config", Command: CommandDebugConfig, Description: "show config layers and requirements"},
		{Name: "new", Command: CommandNew, Description: "start a new chat during a conversation"},
		{Name: "init", Command: CommandInit, Description: "create an AGENTS.md file with instructions for Codex"},
		{Name: "compact", Command: CommandCompact, Description: "summarize conversation to prevent hitting the context limit"},
		{Name: "clear", Command: CommandClear, Description: "clear the visible transcript"},
		{Name: "copy", Command: CommandCopy, Description: "copy last response as markdown"},
		{Name: "raw", Command: CommandRaw, Description: "toggle raw scrollback mode for copy-friendly terminal selection"},
		{Name: "diff", Command: CommandDiff, Description: "show git diff (including untracked files)"},
		{Name: "ps", Command: CommandPs, Description: "list background terminals"},
		{Name: "stop", Command: CommandStop, Aliases: []string{"clean"}, Description: "stop all background terminals"},
		{Name: "model", Command: CommandModel, Description: "choose what model and reasoning effort to use"},
		{Name: "personality", Command: CommandPersonality, Description: "choose communication style"},
		{Name: "plan", Command: CommandPlan, Description: "switch to Plan mode"},
		{Name: "agent", Command: CommandAgent, Description: "switch active agent thread"},
		{Name: "subagents", Command: CommandAgent, Description: "switch active agent thread"},
		{Name: "side", Command: CommandSide, Description: "start a side conversation in an ephemeral fork"},
		{Name: "btw", Command: CommandSide, Description: "start a side conversation in an ephemeral fork"},
		{Name: "permissions", Command: CommandPermissions, Description: "choose what Codex is allowed to do"},
		{Name: "approval", Command: CommandApproval, Description: "show or set approval policy"},
		{Name: "sandbox", Command: CommandSandbox, Description: "show or set sandbox profile"},
		{Name: "experimental", Command: CommandExperimental, Description: "toggle experimental features"},
		{Name: "review", Command: CommandReview, Description: "review my current changes and find issues"},
		{Name: "rename", Command: CommandRename, Description: "rename current thread"},
		{Name: "mention", Command: CommandMention, Description: "mention a file"},
		{Name: "skills", Command: CommandSkills, Description: "use skills to improve how Codex performs specific tasks"},
		{Name: "hooks", Command: CommandHooks, Description: "view and manage lifecycle hooks"},
		{Name: "mcp", Command: CommandMcp, Description: "list configured MCP tools; use /mcp verbose for details"},
		{Name: "apps", Command: CommandApps, Description: "manage apps"},
		{Name: "plugins", Command: CommandPlugins, Description: "browse plugins"},
		{Name: "theme", Command: CommandTheme, Description: "choose syntax theme"},
		{Name: "pets", Command: CommandPets, Aliases: []string{"pet"}, Description: "choose or hide the terminal pet"},
		{Name: "ide", Command: CommandIde, Description: "include current selection, open files, and other context from your IDE"},
		{Name: "vim", Command: CommandVim, Description: "toggle Vim mode"},
		{Name: "approve", Command: CommandAutoReview, Description: "approve one retry of a recent auto-review denial"},
		{Name: "memories", Command: CommandMemories, Description: "configure memory use and generation"},
		{Name: "feedback", Command: CommandFeedback, Description: "send logs to maintainers"},
		{Name: "app", Command: CommandApp, Description: "continue this session in Codex Desktop"},
		{Name: "import", Command: CommandImport, Description: "import setup, this project, and recent chats from Claude Code"},
		{Name: "setup-default-sandbox", Command: CommandElevateSandbox, Description: "set up elevated agent sandbox"},
		{Name: "sandbox-add-read-dir", Command: CommandSandboxReadRoot, Description: "let sandbox read a directory: /sandbox-add-read-dir <absolute_path>"},
		{Name: "rollout", Command: CommandRollout, Description: "print the rollout file path"},
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
		{Name: "quit", Command: CommandExit, Description: "exit Codex"},
		{Name: "exit", Command: CommandExit, Description: "exit Codex"},
	}
}
