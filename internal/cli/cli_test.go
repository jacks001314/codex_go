package cli

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseExecWithRootOptions(t *testing.T) {
	parsed, err := Parse([]string{
		"-c", `model="gpt-5.2"`,
		"--enable", "unified_exec",
		"-m", "gpt-5.5",
		"exec",
		"--json",
		"--skip-git-repo-check",
		"-C", ".",
		"hello",
	})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandExec {
		t.Fatalf("Command = %q, want %q", parsed.Command, CommandExec)
	}
	if got := parsed.Root.ConfigOverrides; len(got) != 1 || got[0] != `model="gpt-5.2"` {
		t.Fatalf("Root.ConfigOverrides = %#v", got)
	}
	if got := parsed.Root.EnableFeatures; len(got) != 1 || got[0] != "unified_exec" {
		t.Fatalf("Root.EnableFeatures = %#v", got)
	}
	if parsed.Root.Shared.Model != "gpt-5.5" {
		t.Fatalf("Root.Shared.Model = %q", parsed.Root.Shared.Model)
	}
	if !parsed.Exec.JSON {
		t.Fatal("Exec.JSON = false, want true")
	}
	if !parsed.Exec.SkipGitRepoCheck {
		t.Fatal("Exec.SkipGitRepoCheck = false, want true")
	}
	if parsed.Exec.Shared.CWD != "." {
		t.Fatalf("Exec.Shared.CWD = %q", parsed.Exec.Shared.CWD)
	}
	if parsed.Exec.Prompt != "hello" {
		t.Fatalf("Exec.Prompt = %q", parsed.Exec.Prompt)
	}
}

func TestParseTUIOnlyFlagsForRootResumeAndFork(t *testing.T) {
	root, err := Parse([]string{"--search", "--ask-for-approval", "on-request", "--no-alt-screen"})
	if err != nil {
		t.Fatalf("Parse root returned error: %v", err)
	}
	if !root.Root.Shared.Search || root.Root.Shared.ApprovalPolicy != "on-request" || !root.Root.Shared.NoAltScreen {
		t.Fatalf("root shared = %#v", root.Root.Shared)
	}

	resume, err := Parse([]string{"resume", "sid", "--search", "--ask-for-approval=never", "--no-alt-screen"})
	if err != nil {
		t.Fatalf("Parse resume returned error: %v", err)
	}
	if resume.Session.Target != "sid" || !resume.Session.Shared.Search || resume.Session.Shared.ApprovalPolicy != "never" || !resume.Session.Shared.NoAltScreen {
		t.Fatalf("resume session = %#v", resume.Session)
	}

	fork, err := Parse([]string{"fork", "--last", "-auntrusted", "continue"})
	if err != nil {
		t.Fatalf("Parse fork returned error: %v", err)
	}
	if !fork.Session.Last || fork.Session.Prompt != "continue" || fork.Session.Shared.ApprovalPolicy != "untrusted" {
		t.Fatalf("fork session = %#v", fork.Session)
	}
}

func TestParseTUIOnlyApprovalConflictsWithDangerousBypass(t *testing.T) {
	for _, args := range [][]string{
		{"--yolo", "--ask-for-approval", "never"},
		{"resume", "--dangerously-bypass-approvals-and-sandbox", "--ask-for-approval", "on-request"},
		{"fork", "--last", "-anever", "--yolo"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := Parse(args)
			if err == nil || !strings.Contains(err.Error(), "conflicts with `--ask-for-approval`") {
				t.Fatalf("Parse(%v) error = %v", args, err)
			}
		})
	}
}

func TestParseExecOutputFlags(t *testing.T) {
	parsed, err := Parse([]string{
		"exec",
		"--output-schema", "schema.json",
		"--output-last-message", "last.txt",
		"--experimental-json",
		"-c", "model=gpt-5.5",
		"hello",
	})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.OutputSchema != "schema.json" {
		t.Fatalf("OutputSchema = %q", parsed.Exec.OutputSchema)
	}
	if parsed.Exec.LastMessageFile != "last.txt" {
		t.Fatalf("LastMessageFile = %q", parsed.Exec.LastMessageFile)
	}
	if !parsed.Exec.JSON {
		t.Fatal("JSON = false")
	}
	if got := parsed.Exec.ConfigOverrides; len(got) != 1 || got[0] != "model=gpt-5.5" {
		t.Fatalf("ConfigOverrides = %#v", got)
	}
}

func TestParseExecRejectsFullAutoWithDangerousBypass(t *testing.T) {
	for _, args := range [][]string{
		{"exec", "--full-auto", "--dangerously-bypass-approvals-and-sandbox", "hello"},
		{"--yolo", "exec", "--full-auto", "hello"},
		{"exec", "resume", "--last", "--full-auto", "--dangerously-bypass-approvals-and-sandbox", "hello"},
	} {
		_, err := Parse(args)
		if err == nil {
			t.Fatalf("Parse(%v) returned nil error, want conflict", args)
		}
		if !strings.Contains(err.Error(), "--full-auto") || !strings.Contains(err.Error(), "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatalf("Parse(%v) error = %q", args, err.Error())
		}
	}
}

func TestParseExecDashPrompt(t *testing.T) {
	parsed, err := Parse([]string{"exec", "-"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Prompt != "-" {
		t.Fatalf("Prompt = %q", parsed.Exec.Prompt)
	}
}

func TestParseLoginStatus(t *testing.T) {
	parsed, err := Parse([]string{"login", "status"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandLogin {
		t.Fatalf("Command = %q, want login", parsed.Command)
	}
	if parsed.Login.Action != "status" {
		t.Fatalf("Login.Action = %q, want status", parsed.Login.Action)
	}
}

func TestParseFeaturesEnable(t *testing.T) {
	parsed, err := Parse([]string{"features", "enable", "shell_tool"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandFeatures {
		t.Fatalf("Command = %q, want features", parsed.Command)
	}
	if parsed.Features.Action != "enable" || parsed.Features.Feature != "shell_tool" {
		t.Fatalf("Features = %#v", parsed.Features)
	}
}

func TestParseInteractivePrompt(t *testing.T) {
	parsed, err := Parse([]string{"fix", "tests"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandInteractive {
		t.Fatalf("Command = %q, want interactive", parsed.Command)
	}
	if parsed.Root.Prompt != "fix tests" {
		t.Fatalf("Prompt = %q", parsed.Root.Prompt)
	}
}

func TestRejectRootStrictConfigForUnsupportedSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "mcp before subcommand parse",
			args: []string{"--strict-config", "mcp"},
			want: "`--strict-config` is not supported for `codex mcp`",
		},
		{
			name: "cloud before subcommand parse",
			args: []string{"--strict-config", "-c", "foo=bar", "cloud", "list"},
			want: "`--strict-config` is not supported for `codex cloud`",
		},
		{
			name: "app-server tooling after subcommand parse",
			args: []string{"--strict-config", "app-server", "daemon", "bootstrap"},
			want: "`--strict-config` is not supported for `codex app-server daemon bootstrap`",
		},
		{
			name: "app desktop after cfg gate",
			args: []string{"--strict-config", "app"},
			want: "`--strict-config` is not supported for `codex app`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args)
			if err == nil {
				t.Fatal("Parse returned nil error, want failure")
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAllowRootStrictConfigForSupportedSubcommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "exec", args: []string{"--strict-config", "exec", "hello"}},
		{name: "review", args: []string{"--strict-config", "review", "hello"}},
		{name: "mcp-server", args: []string{"--strict-config", "mcp-server"}},
		{name: "exec-server", args: []string{"--strict-config", "exec-server", "--listen", "stdio"}},
		{name: "app-server runtime", args: []string{"--strict-config", "app-server", "--listen", "off"}},
		{name: "resume", args: []string{"--strict-config", "resume", "--last"}},
		{name: "fork", args: []string{"--strict-config", "fork", "--last"}},
		{name: "doctor", args: []string{"--strict-config", "doctor", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := Parse(tt.args)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if !parsed.Root.StrictConfig {
				t.Fatal("Root.StrictConfig = false")
			}
		})
	}
}

func TestRejectRemoteForExec(t *testing.T) {
	_, err := Parse([]string{"--remote", "ws://127.0.0.1:4500", "exec", "hello"})
	if err == nil {
		t.Fatal("Parse returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "only supported for interactive TUI commands") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestParseRemoteAuthEnvForInteractiveRoot(t *testing.T) {
	parsed, err := Parse([]string{"--remote-auth-token-env", "TOKEN"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandInteractive || parsed.Root.RemoteAuthEnv != "TOKEN" {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseDebugPromptInput(t *testing.T) {
	parsed, err := Parse([]string{"debug", "prompt-input", "--image", "a.png,b.png", "hello"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandDebug {
		t.Fatalf("Command = %q", parsed.Command)
	}
	if parsed.Debug.Subcommand != "prompt-input" {
		t.Fatalf("Subcommand = %q", parsed.Debug.Subcommand)
	}
	if parsed.Debug.Prompt != "hello" {
		t.Fatalf("Prompt = %q", parsed.Debug.Prompt)
	}
	if got := parsed.Debug.Images; len(got) != 2 || got[0] != "a.png" || got[1] != "b.png" {
		t.Fatalf("Images = %#v", got)
	}
}

func TestParseDebugTooling(t *testing.T) {
	models, err := Parse([]string{"debug", "models", "--bundled"})
	if err != nil {
		t.Fatalf("Parse debug models returned error: %v", err)
	}
	if models.Debug.Subcommand != "models" || !models.Debug.BundledModels {
		t.Fatalf("models = %#v", models.Debug)
	}

	appServer, err := Parse([]string{"debug", "app-server", "send-message-v2", "hello"})
	if err != nil {
		t.Fatalf("Parse debug app-server returned error: %v", err)
	}
	if appServer.Debug.AppServerAction != "send-message-v2" || appServer.Debug.AppServerMessage != "hello" {
		t.Fatalf("app-server = %#v", appServer.Debug)
	}

	trace, err := Parse([]string{"debug", "trace-reduce", "--output", "state.json", "trace-bundle"})
	if err != nil {
		t.Fatalf("Parse trace-reduce returned error: %v", err)
	}
	if trace.Debug.TraceBundle != "trace-bundle" || trace.Debug.TraceOutput != "state.json" {
		t.Fatalf("trace-reduce = %#v", trace.Debug)
	}

	clear, err := Parse([]string{"debug", "clear-memories"})
	if err != nil {
		t.Fatalf("Parse clear-memories returned error: %v", err)
	}
	if clear.Debug.Subcommand != "clear-memories" {
		t.Fatalf("clear = %#v", clear.Debug)
	}
}

func TestParseRootReviewCommit(t *testing.T) {
	parsed, err := Parse([]string{"review", "--commit", "abc123", "--title", "Fix bug"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandReview {
		t.Fatalf("Command = %q", parsed.Command)
	}
	if parsed.Exec.Subcommand != "review" {
		t.Fatalf("Exec.Subcommand = %q", parsed.Exec.Subcommand)
	}
	if parsed.Exec.Review.Commit != "abc123" || parsed.Exec.Review.CommitTitle != "Fix bug" {
		t.Fatalf("Review = %#v", parsed.Exec.Review)
	}
}

func TestParseExecReviewBase(t *testing.T) {
	parsed, err := Parse([]string{"exec", "review", "--base", "main"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandExec || parsed.Exec.Subcommand != "review" {
		t.Fatalf("parsed = %#v", parsed)
	}
	if parsed.Exec.Review.Base != "main" {
		t.Fatalf("Base = %q", parsed.Exec.Review.Base)
	}
}

func TestParseExecResume(t *testing.T) {
	parsed, err := Parse([]string{"exec", "--json", "resume", "--last", "2+2"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandExec || parsed.Exec.Subcommand != "resume" {
		t.Fatalf("parsed = %#v", parsed)
	}
	if !parsed.Exec.JSON || !parsed.Exec.Resume.Last || parsed.Exec.Resume.Prompt != "2+2" {
		t.Fatalf("exec resume = %#v", parsed.Exec)
	}

	parsed, err = Parse([]string{"exec", "resume", "session-123", "-o", "last.txt", "--output-schema", "schema.json", "continue here"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Exec.Resume.SessionID != "session-123" || parsed.Exec.Resume.Prompt != "continue here" {
		t.Fatalf("resume = %#v", parsed.Exec.Resume)
	}
	if parsed.Exec.LastMessageFile != "last.txt" || parsed.Exec.OutputSchema != "schema.json" {
		t.Fatalf("exec output flags = %#v", parsed.Exec)
	}
}

func TestParseReviewRejectsConflicts(t *testing.T) {
	_, err := Parse([]string{"review", "--uncommitted", "custom"})
	if err == nil {
		t.Fatal("Parse returned nil error, want failure")
	}
}

func TestParseResumeLastAndPrompt(t *testing.T) {
	parsed, err := Parse([]string{"resume", "--last", "--include-non-interactive", "continue here"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandResume {
		t.Fatalf("Command = %q, want resume", parsed.Command)
	}
	if !parsed.Session.Last || !parsed.Session.IncludeNonInteractive {
		t.Fatalf("Session flags = %#v", parsed.Session)
	}
	if parsed.Session.Prompt != "continue here" {
		t.Fatalf("Prompt = %q", parsed.Session.Prompt)
	}

	parsed, err = Parse([]string{"resume", "continue here", "--last"})
	if err != nil {
		t.Fatalf("Parse trailing --last returned error: %v", err)
	}
	if !parsed.Session.Last || parsed.Session.Target != "" || parsed.Session.Prompt != "continue here" {
		t.Fatalf("trailing --last session = %#v", parsed.Session)
	}
}

func TestParseSessionLastRejectsExplicitSessionAndPrompt(t *testing.T) {
	for _, args := range [][]string{
		{"resume", "--last", "session-1", "continue here"},
		{"resume", "session-1", "--last", "continue here"},
		{"fork", "--last", "session-1", "continue here"},
		{"fork", "session-1", "--last", "continue here"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := Parse(args)
			if err == nil || !strings.Contains(err.Error(), "--last conflicts with SESSION_ID and prompt") {
				t.Fatalf("Parse(%v) error = %v", args, err)
			}
		})
	}
}

func TestParseArchiveAndDelete(t *testing.T) {
	deleteID := "123e4567-e89b-12d3-a456-426614174000"
	archive, err := Parse([]string{"archive", "thread-1"})
	if err != nil {
		t.Fatalf("Parse archive returned error: %v", err)
	}
	if archive.Command != CommandArchive || archive.Session.Target != "thread-1" {
		t.Fatalf("archive = %#v", archive)
	}

	deleted, err := Parse([]string{"delete", "--force", deleteID})
	if err != nil {
		t.Fatalf("Parse delete returned error: %v", err)
	}
	if deleted.Command != CommandDelete || !deleted.Session.Force || deleted.Session.Target != deleteID {
		t.Fatalf("delete = %#v", deleted)
	}

	_, err = Parse([]string{"delete", "--force", "thread-1"})
	if err == nil || !strings.Contains(err.Error(), "--force requires a session UUID") {
		t.Fatalf("non-UUID force delete error = %v", err)
	}

	_, err = Parse([]string{"delete", "--force", "--name", "Thread One"})
	if err == nil || !strings.Contains(err.Error(), "unknown delete option --name") {
		t.Fatalf("name force delete error = %v", err)
	}
}

func TestParseSessionRejectsNameFlagLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"resume", "--name", "Design notes"},
		{"fork", "--name=Fork Me"},
		{"archive", "--name=Design notes"},
		{"unarchive", "--name", "Design notes"},
		{"delete", "--name", "Design notes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := Parse(args)
			if err == nil || !strings.Contains(err.Error(), "--name") {
				t.Fatalf("Parse(%v) error = %v", args, err)
			}
		})
	}
}

func TestParseSessionMutationRejectsTUIOnlyFlagsLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"archive", "--search", "Design"},
		{"delete", "--ask-for-approval", "never", "Design"},
		{"unarchive", "--no-alt-screen", "Design"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := Parse(args)
			if err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("Parse(%v) error = %v", args, err)
			}
		})
	}
}

func TestParseSessionRemoteOptions(t *testing.T) {
	resume, err := Parse([]string{
		"--remote", "ws://127.0.0.1:4500",
		"resume",
		"--remote=wss://example.test/session",
		"--remote-auth-token-env", "SESSION_TOKEN",
		"--last",
	})
	if err != nil {
		t.Fatalf("Parse resume remote returned error: %v", err)
	}
	if resume.Root.Remote != "ws://127.0.0.1:4500" || resume.Session.Remote != "wss://example.test/session" || resume.Session.RemoteAuthEnv != "SESSION_TOKEN" {
		t.Fatalf("resume remote = root %#v session %#v", resume.Root, resume.Session)
	}

	archive, err := Parse([]string{"archive", "--remote", "unix://", "--remote-auth-token-env=SESSION_TOKEN", "thread-1"})
	if err != nil {
		t.Fatalf("Parse archive remote returned error: %v", err)
	}
	if archive.Session.Remote != "unix://" || archive.Session.RemoteAuthEnv != "SESSION_TOKEN" || archive.Session.Target != "thread-1" {
		t.Fatalf("archive remote = %#v", archive.Session)
	}

	fork, err := Parse([]string{"fork", "--remote", "unix://", "--last"})
	if err != nil {
		t.Fatalf("Parse fork remote returned error: %v", err)
	}
	if fork.Session.Remote != "unix://" || !fork.Session.Last {
		t.Fatalf("fork remote = %#v", fork.Session)
	}
}

func TestParseForkRustSurface(t *testing.T) {
	parsed, err := Parse([]string{"fork", "--all", "thread-1", "continue here"})
	if err != nil {
		t.Fatalf("Parse fork returned error: %v", err)
	}
	if parsed.Command != CommandFork || parsed.Session.Target != "thread-1" || !parsed.Session.All || parsed.Session.Prompt != "continue here" {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseForkRejectsSnapshotOptionsLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"fork", "--history-mode", "last-n", "thread-1"},
		{"fork", "--last-n", "2", "thread-1"},
		{"fork", "--last-turn-id", "turn-2", "thread-1"},
		{"fork", "--ephemeral", "thread-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := Parse(args)
			if err == nil || !strings.Contains(err.Error(), "unknown session option") {
				t.Fatalf("Parse(%v) error = %v", args, err)
			}
		})
	}
}

func TestParseSessionMutationRequiresTarget(t *testing.T) {
	_, err := Parse([]string{"unarchive"})
	if err == nil {
		t.Fatal("Parse returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "requires SESSION") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestParseRemoteControl(t *testing.T) {
	parsed, err := Parse([]string{"remote-control", "--json", "pair"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandRemoteControl || !parsed.RemoteControl.JSON || parsed.RemoteControl.Subcommand != "pair" {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseMCPAddStdio(t *testing.T) {
	parsed, err := Parse([]string{"mcp", "add", "fs", "--env", "ROOT=.", "--", "mcp-fs", "--readonly"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandMCP || parsed.MCP.Action != "add" || parsed.MCP.Name != "fs" {
		t.Fatalf("parsed = %#v", parsed)
	}
	if parsed.MCP.Env["ROOT"] != "." {
		t.Fatalf("Env = %#v", parsed.MCP.Env)
	}
	if got := parsed.MCP.Command; len(got) != 2 || got[0] != "mcp-fs" || got[1] != "--readonly" {
		t.Fatalf("Command = %#v", got)
	}
}

func TestParseMCPAddHTTP(t *testing.T) {
	parsed, err := Parse([]string{"mcp", "add", "docs", "--url", "https://mcp.example.test", "--bearer-token-env-var", "TOKEN"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.MCP.URL != "https://mcp.example.test" || parsed.MCP.BearerTokenEnvVar != "TOKEN" {
		t.Fatalf("MCP = %#v", parsed.MCP)
	}
}

func TestParseMCPAddTransportFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "bearer token requires url",
			args: []string{"mcp", "add", "fs", "--bearer-token-env-var", "TOKEN", "--", "mcp-fs"},
			want: "mcp add --bearer-token-env-var requires --url",
		},
		{
			name: "oauth client requires url",
			args: []string{"mcp", "add", "fs", "--oauth-client-id", "client-1", "--", "mcp-fs"},
			want: "mcp add --oauth-client-id requires --url",
		},
		{
			name: "oauth resource requires url",
			args: []string{"mcp", "add", "fs", "--oauth-resource", "resource-1", "--", "mcp-fs"},
			want: "mcp add --oauth-resource requires --url",
		},
		{
			name: "env is stdio only",
			args: []string{"mcp", "add", "docs", "--url", "https://mcp.example.test", "--env", "TOKEN=secret"},
			want: "mcp add --env is only valid with stdio COMMAND",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.args)
			if err == nil {
				t.Fatal("Parse returned nil error, want validation failure")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("error = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParsePluginCommands(t *testing.T) {
	add, err := Parse([]string{"plugin", "add", "sample@debug", "--json"})
	if err != nil {
		t.Fatalf("Parse add returned error: %v", err)
	}
	if add.Command != CommandPlugin || add.Plugin.Action != "add" || add.Plugin.Plugin != "sample@debug" || !add.Plugin.JSON {
		t.Fatalf("add = %#v", add)
	}

	list, err := Parse([]string{"plugin", "list", "--marketplace", "debug", "--json", "--available"})
	if err != nil {
		t.Fatalf("Parse list returned error: %v", err)
	}
	if list.Plugin.Action != "list" || list.Plugin.MarketplaceName != "debug" || !list.Plugin.Available {
		t.Fatalf("list = %#v", list)
	}

	marketplace, err := Parse([]string{"plugin", "marketplace", "add", "--sparse", "plugins/foo", "owner/repo"})
	if err != nil {
		t.Fatalf("Parse marketplace returned error: %v", err)
	}
	if marketplace.Plugin.Marketplace.Action != "add" || marketplace.Plugin.Marketplace.Source != "owner/repo" {
		t.Fatalf("marketplace = %#v", marketplace.Plugin.Marketplace)
	}
}

func TestParseSandbox(t *testing.T) {
	parsed, err := Parse([]string{"sandbox", "-P", ":workspace", "-C", ".", "--", "echo", "hello"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandSandbox || parsed.Sandbox.PermissionProfile != ":workspace" || parsed.Sandbox.CWD != "." {
		t.Fatalf("parsed = %#v", parsed)
	}
	if got := parsed.Sandbox.Command; len(got) != 2 || got[0] != "echo" || got[1] != "hello" {
		t.Fatalf("Command = %#v", got)
	}

	alias, err := Parse([]string{"sandbox", "--permissions-profile", ":workspace", "--", "echo"})
	if err != nil {
		t.Fatalf("Parse alias returned error: %v", err)
	}
	if alias.Sandbox.PermissionProfile != ":workspace" {
		t.Fatalf("alias sandbox = %#v", alias.Sandbox)
	}

	pathsArgs := []string{"sandbox", "--sandbox-state-json", `{}`, "--sandbox-state-readable-root", "docs", "--", "echo"}
	if runtime.GOOS == "darwin" {
		pathsArgs = []string{"sandbox", "--sandbox-state-json", `{}`, "--sandbox-state-readable-root", "docs", "--allow-unix-socket=run/codex.sock", "--", "echo"}
	}
	paths, err := Parse(pathsArgs)
	if err != nil {
		t.Fatalf("Parse sandbox paths returned error: %v", err)
	}
	wantReadable, err := filepath.Abs("docs")
	if err != nil {
		t.Fatalf("Abs docs error = %v", err)
	}
	wantSocket, err := filepath.Abs(filepath.Join("run", "codex.sock"))
	if err != nil {
		t.Fatalf("Abs socket error = %v", err)
	}
	if len(paths.Sandbox.SandboxReadableRoots) != 1 || paths.Sandbox.SandboxReadableRoots[0] != filepath.Clean(wantReadable) {
		t.Fatalf("readable roots = %#v, want %q", paths.Sandbox.SandboxReadableRoots, filepath.Clean(wantReadable))
	}
	if runtime.GOOS == "darwin" {
		if len(paths.Sandbox.AllowUnixSockets) != 1 || paths.Sandbox.AllowUnixSockets[0] != filepath.Clean(wantSocket) {
			t.Fatalf("allow unix sockets = %#v, want %q", paths.Sandbox.AllowUnixSockets, filepath.Clean(wantSocket))
		}
	} else {
		_, err := Parse([]string{"sandbox", "--allow-unix-socket=run/codex.sock", "--", "echo"})
		if err == nil || !strings.Contains(err.Error(), "unknown sandbox option --allow-unix-socket") {
			t.Fatalf("allow unix socket error = %v", err)
		}
		_, err = Parse([]string{"sandbox", "--log-denials", "--", "echo"})
		if err == nil || !strings.Contains(err.Error(), "unknown sandbox option --log-denials") {
			t.Fatalf("log denials error = %v", err)
		}
	}
}

func TestParseSandboxRejectsMissingRequiredFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "cwd requires profile", args: []string{"sandbox", "-C", "."}, want: "`--cd` requires `--permission-profile`"},
		{name: "managed config requires profile", args: []string{"sandbox", "--include-managed-config", "--", "echo"}, want: "`--include-managed-config` requires `--permission-profile`"},
		{name: "readable root requires state", args: []string{"sandbox", "--sandbox-state-readable-root", "."}, want: "`--sandbox-state-readable-root` requires `--sandbox-state-json`"},
		{name: "disable network requires state", args: []string{"sandbox", "--sandbox-state-disable-network", "--", "echo"}, want: "`--sandbox-state-disable-network` requires `--sandbox-state-json`"},
		{name: "state conflicts with profile", args: []string{"sandbox", "--sandbox-state-json", `{}`, "--permission-profile", ":workspace", "--", "echo"}, want: "`--sandbox-state-json` conflicts with `--permission-profile`"},
		{name: "state conflicts with cwd", args: []string{"sandbox", "--sandbox-state-json", `{}`, "-C", ".", "--", "echo"}, want: "`--sandbox-state-json` conflicts with `--cd`"},
		{name: "state conflicts with managed config", args: []string{"sandbox", "--sandbox-state-json", `{}`, "--include-managed-config", "--", "echo"}, want: "`--sandbox-state-json` conflicts with `--include-managed-config`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.args)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Parse(%v) error = %v, want %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestParseSandboxSetup(t *testing.T) {
	parsed, err := Parse([]string{"sandbox", "setup", "--elevated", "--current-user"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !parsed.Sandbox.Setup || !parsed.Sandbox.Elevated || !parsed.Sandbox.CurrentUser {
		t.Fatalf("Sandbox = %#v", parsed.Sandbox)
	}

	managed, err := Parse([]string{"sandbox", "setup", "--elevated", "--user", `DOMAIN\alice`, "--codex-home", `C:\Users\alice\.codex`})
	if err != nil {
		t.Fatalf("Parse managed setup returned error: %v", err)
	}
	if managed.Sandbox.User != `DOMAIN\alice` || managed.Sandbox.CodexHome != `C:\Users\alice\.codex` {
		t.Fatalf("managed setup = %#v", managed.Sandbox)
	}
}

func TestParseSandboxSetupRejectsMissingRequiredFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing user", args: []string{"sandbox", "setup", "--elevated"}, want: "--user or --current-user is required"},
		{name: "missing codex home", args: []string{"sandbox", "setup", "--elevated", "--user", `DOMAIN\alice`}, want: "--codex-home is required with --user"},
		{name: "user conflict", args: []string{"sandbox", "setup", "--elevated", "--current-user", "--user", `DOMAIN\alice`, "--codex-home", `C:\Users\alice\.codex`}, want: "--user conflicts with --current-user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.args)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Parse(%v) error = %v, want %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestParseExecpolicyCheck(t *testing.T) {
	parsed, err := Parse([]string{"execpolicy", "check", "--rules", "policy.rules", "--pretty", "--resolve-host-executables", "git", "push"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandExecpolicy || parsed.Execpolicy.Action != "check" || !parsed.Execpolicy.Pretty || !parsed.Execpolicy.ResolveHostExecutables {
		t.Fatalf("parsed = %#v", parsed)
	}
	if got := parsed.Execpolicy.Rules; len(got) != 1 || got[0] != "policy.rules" {
		t.Fatalf("Rules = %#v", got)
	}
	if got := parsed.Execpolicy.Command; len(got) != 2 || got[0] != "git" || got[1] != "push" {
		t.Fatalf("Command = %#v", got)
	}

	parsed, err = Parse([]string{"execpolicy", "check", "--rules", "policy.rules", "--dry-run", "--force"})
	if err != nil {
		t.Fatalf("Parse returned error for hyphen command: %v", err)
	}
	if got := parsed.Execpolicy.Command; len(got) != 2 || got[0] != "--dry-run" || got[1] != "--force" {
		t.Fatalf("hyphen Command = %#v", got)
	}
}

func TestParseExecpolicyCheckHyphenCommandBeforeRulesConsumesTrailingArgs(t *testing.T) {
	_, err := Parse([]string{"execpolicy", "check", "--dry-run", "--rules", "policy.rules"})
	if err == nil || err.Error() != "execpolicy check requires --rules PATH" {
		t.Fatalf("Parse error = %v, want missing rules because --dry-run starts COMMAND", err)
	}
}

func TestParseApplyAcceptsSinglePatchArgument(t *testing.T) {
	patch := "*** Begin Patch\n*** End Patch"
	parsed, err := Parse([]string{"apply", patch})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandApply || parsed.Apply.Patch != patch {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseApplyRejectsExtraPatchArguments(t *testing.T) {
	_, err := Parse([]string{"apply", "patch-one", "patch-two"})
	if err == nil || err.Error() != "apply accepts at most one PATCH" {
		t.Fatalf("Parse error = %v", err)
	}
}

func TestParseMCPServer(t *testing.T) {
	parsed, err := Parse([]string{"mcp-server", "--strict-config"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandMCPServer || !parsed.MCPServer.StrictConfig {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseCloudCommands(t *testing.T) {
	execParsed, err := Parse([]string{"cloud", "exec", "--env", "env-1", "--attempts=3", "--branch", "feature/a", "fix", "tests"})
	if err != nil {
		t.Fatalf("Parse cloud exec returned error: %v", err)
	}
	if execParsed.Command != CommandCloud || execParsed.Cloud.Action != "exec" || execParsed.Cloud.Environment != "env-1" {
		t.Fatalf("exec parsed = %#v", execParsed)
	}
	if execParsed.Cloud.Attempts != 3 || execParsed.Cloud.Branch != "feature/a" || execParsed.Cloud.Query != "fix tests" {
		t.Fatalf("cloud exec = %#v", execParsed.Cloud)
	}

	listParsed, err := Parse([]string{"cloud", "list", "--env=env-1", "--limit", "12", "--cursor", "next", "--json"})
	if err != nil {
		t.Fatalf("Parse cloud list returned error: %v", err)
	}
	if listParsed.Cloud.Action != "list" || listParsed.Cloud.Limit != 12 || listParsed.Cloud.Cursor != "next" || !listParsed.Cloud.JSON {
		t.Fatalf("cloud list = %#v", listParsed.Cloud)
	}

	diffParsed, err := Parse([]string{"cloud", "diff", "--attempt=2", "task-1"})
	if err != nil {
		t.Fatalf("Parse cloud diff returned error: %v", err)
	}
	if diffParsed.Cloud.Action != "diff" || diffParsed.Cloud.TaskID != "task-1" || diffParsed.Cloud.Attempt != 2 {
		t.Fatalf("cloud diff = %#v", diffParsed.Cloud)
	}

	statusParsed, err := Parse([]string{"cloud", "status", "task-1"})
	if err != nil {
		t.Fatalf("Parse cloud status returned error: %v", err)
	}
	if statusParsed.Cloud.Action != "status" || statusParsed.Cloud.TaskID != "task-1" {
		t.Fatalf("cloud status = %#v", statusParsed.Cloud)
	}
}

func TestParseCloudStatusRejectsExtrasInOrder(t *testing.T) {
	_, err := Parse([]string{"cloud", "status", "--json", "task-1"})
	if err == nil || err.Error() != "unknown cloud status option --json" {
		t.Fatalf("cloud status option error = %v", err)
	}
	_, err = Parse([]string{"cloud", "status", "task-1", "task-2"})
	if err == nil || err.Error() != "cloud status accepts exactly one TASK_ID" {
		t.Fatalf("cloud status extra task error = %v", err)
	}
}

func TestParseResponsesAPIProxy(t *testing.T) {
	parsed, err := Parse([]string{
		"responses-api-proxy",
		"--port", "3456",
		"--server-info=server.json",
		"--http-shutdown",
		"--upstream-url", "http://127.0.0.1/responses",
		"--dump-dir", "dumps",
	})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandResponsesAPIProxy || parsed.ResponsesAPIProxy.Port == nil || *parsed.ResponsesAPIProxy.Port != 3456 {
		t.Fatalf("parsed = %#v", parsed)
	}
	if parsed.ResponsesAPIProxy.ServerInfo != "server.json" || !parsed.ResponsesAPIProxy.HTTPShutdown {
		t.Fatalf("proxy = %#v", parsed.ResponsesAPIProxy)
	}
	if parsed.ResponsesAPIProxy.UpstreamURL != "http://127.0.0.1/responses" || parsed.ResponsesAPIProxy.DumpDir != "dumps" {
		t.Fatalf("proxy = %#v", parsed.ResponsesAPIProxy)
	}
}

func TestParseExecServerAndStdioToUDS(t *testing.T) {
	defaultListen, err := Parse([]string{"exec-server"})
	if err != nil {
		t.Fatalf("Parse exec-server default returned error: %v", err)
	}
	if defaultListen.ExecServer.ListenSet || defaultListen.ExecServer.Listen != "" {
		t.Fatalf("default exec-server listen = %#v", defaultListen.ExecServer)
	}

	execServer, err := Parse([]string{
		"exec-server",
		"--remote=ws://127.0.0.1:7777",
		"--environment-id", "env-1",
		"--name", "worker-a",
		"--use-agent-identity-auth",
	})
	if err != nil {
		t.Fatalf("Parse exec-server returned error: %v", err)
	}
	if execServer.Command != CommandExecServer || execServer.ExecServer.Remote != "ws://127.0.0.1:7777" || execServer.ExecServer.EnvironmentID != "env-1" {
		t.Fatalf("exec-server = %#v", execServer)
	}
	if execServer.ExecServer.Name != "worker-a" || !execServer.ExecServer.UseAgentIdentityAuth {
		t.Fatalf("exec-server = %#v", execServer.ExecServer)
	}

	listen, err := Parse([]string{"exec-server", "--listen=stdio"})
	if err != nil {
		t.Fatalf("Parse exec-server listen returned error: %v", err)
	}
	if !listen.ExecServer.ListenSet || listen.ExecServer.Listen != "stdio" {
		t.Fatalf("exec-server listen = %#v", listen.ExecServer)
	}

	stdio, err := Parse([]string{"stdio-to-uds", `\\.\pipe\codex-test`})
	if err != nil {
		t.Fatalf("Parse stdio-to-uds returned error: %v", err)
	}
	if stdio.Command != CommandStdioToUDS || stdio.StdioToUDS.SocketPath != `\\.\pipe\codex-test` {
		t.Fatalf("stdio-to-uds = %#v", stdio)
	}

	_, err = Parse([]string{"exec-server", "--remote", "ws://127.0.0.1:7777"})
	if err == nil || err.Error() != "--environment-id is required when --remote is set" {
		t.Fatalf("exec-server missing environment error = %v", err)
	}
	_, err = Parse([]string{"exec-server", "--listen", "stdio", "--remote", "ws://127.0.0.1:7777", "--environment-id", "env-1"})
	if err == nil || err.Error() != "--listen conflicts with --remote" {
		t.Fatalf("exec-server listen conflict error = %v", err)
	}
	_, err = Parse([]string{"exec-server", "--use-agent-identity-auth"})
	if err == nil || err.Error() != "--use-agent-identity-auth requires --remote" {
		t.Fatalf("exec-server agent identity requires remote error = %v", err)
	}
}

func TestParseAppServerToolingSubcommands(t *testing.T) {
	proxy, err := Parse([]string{"app-server", "proxy", "--sock", `\\.\pipe\codex-app-server`})
	if err != nil {
		t.Fatalf("Parse proxy returned error: %v", err)
	}
	if proxy.Command != CommandAppServer || proxy.AppServer.Subcommand[0] != "proxy" || proxy.AppServer.Proxy.SocketPath != `\\.\pipe\codex-app-server` {
		t.Fatalf("proxy = %#v", proxy)
	}

	ts, err := Parse([]string{"app-server", "generate-ts", "--out", "gen", "--prettier=prettier", "--experimental"})
	if err != nil {
		t.Fatalf("Parse generate-ts returned error: %v", err)
	}
	if ts.AppServer.Generate.Action != "generate-ts" || ts.AppServer.Generate.OutDir != "gen" || ts.AppServer.Generate.Prettier != "prettier" || !ts.AppServer.Generate.Experimental {
		t.Fatalf("generate-ts = %#v", ts.AppServer.Generate)
	}

	schema, err := Parse([]string{"app-server", "generate-json-schema", "-ogen", "--experimental"})
	if err != nil {
		t.Fatalf("Parse generate-json-schema returned error: %v", err)
	}
	if schema.AppServer.Generate.Action != "generate-json-schema" || schema.AppServer.Generate.OutDir != "gen" || !schema.AppServer.Generate.Experimental {
		t.Fatalf("generate-json-schema = %#v", schema.AppServer.Generate)
	}

	internal, err := Parse([]string{"app-server", "generate-internal-json-schema", "--out=internal"})
	if err != nil {
		t.Fatalf("Parse generate-internal-json-schema returned error: %v", err)
	}
	if internal.AppServer.Generate.Action != "generate-internal-json-schema" || internal.AppServer.Generate.OutDir != "internal" {
		t.Fatalf("generate-internal-json-schema = %#v", internal.AppServer.Generate)
	}
}

func TestParseAppServerListenValidation(t *testing.T) {
	off, err := Parse([]string{"app-server", "--listen", "off"})
	if err != nil {
		t.Fatalf("Parse listen off returned error: %v", err)
	}
	if off.AppServer.Listen != "off" {
		t.Fatalf("listen = %q", off.AppServer.Listen)
	}

	_, err = Parse([]string{"app-server", "--stdio", "--listen", "stdio://"})
	if err == nil || !strings.Contains(err.Error(), "--stdio conflicts with --listen") {
		t.Fatalf("Parse stdio/listen conflict error = %v", err)
	}

	relativeUnix, err := Parse([]string{"app-server", "--listen", "unix://codex.sock"})
	if err != nil {
		t.Fatalf("Parse relative unix listen returned error: %v", err)
	}
	if relativeUnix.AppServer.Listen != "unix://codex.sock" {
		t.Fatalf("relative unix listen = %q", relativeUnix.AppServer.Listen)
	}

	_, err = Parse([]string{"app-server", "--listen", "http://foo"})
	wantUnsupported := "unsupported --listen URL `http://foo`; expected `stdio://`, `unix://`, `unix://PATH`, `ws://IP:PORT`, or `off`"
	if err == nil || err.Error() != wantUnsupported {
		t.Fatalf("Parse unsupported listen error = %v, want %q", err, wantUnsupported)
	}

	_, err = Parse([]string{"app-server", "--listen", "ws://localhost:8765"})
	wantInvalidWS := "invalid websocket --listen URL `ws://localhost:8765`; expected `ws://IP:PORT`"
	if err == nil || err.Error() != wantInvalidWS {
		t.Fatalf("Parse invalid websocket listen error = %v, want %q", err, wantInvalidWS)
	}
}

func TestParseAppServerWebSocketAuthFlags(t *testing.T) {
	parsed, err := Parse([]string{
		"app-server",
		"--listen", "ws://127.0.0.1:8765",
		"--ws-auth", "capability-token",
		"--ws-token-sha256", strings.Repeat("ab", 32),
	})
	if err != nil {
		t.Fatalf("Parse app-server ws auth returned error: %v", err)
	}
	if parsed.AppServer.WSAuth != "capability-token" || parsed.AppServer.WSTokenSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("app-server ws auth = %#v", parsed.AppServer)
	}

	_, err = Parse([]string{"app-server", "--ws-token-sha256", strings.Repeat("ab", 32)})
	if err == nil || !strings.Contains(err.Error(), "websocket auth flags require") {
		t.Fatalf("Parse missing ws-auth error = %v", err)
	}

	_, err = Parse([]string{"app-server", "--ws-auth", "signed-bearer-token"})
	if err == nil || !strings.Contains(err.Error(), "--ws-shared-secret-file") {
		t.Fatalf("Parse signed bearer missing secret error = %v", err)
	}

	_, err = Parse([]string{"app-server", "--ws-auth", "capability-token", "--ws-token-sha256", "not-a-sha256"})
	if err == nil || err.Error() != "--ws-token-sha256 must be a 64-character hex SHA-256 digest" {
		t.Fatalf("Parse malformed ws hash error = %v", err)
	}

	_, err = Parse([]string{"app-server", "--ws-auth", "capability-token", "--ws-token-file", "token.txt"})
	if err == nil || err.Error() != "--ws-token-file must be an absolute path" {
		t.Fatalf("Parse relative ws token file error = %v", err)
	}

	_, err = Parse([]string{"app-server", "--ws-auth", "signed-bearer-token", "--ws-shared-secret-file", "secret.txt"})
	if err == nil || err.Error() != "--ws-shared-secret-file must be an absolute path" {
		t.Fatalf("Parse relative ws shared secret file error = %v", err)
	}

	absSecret := filepath.Join(t.TempDir(), "secret.txt")
	signed, err := Parse([]string{
		"app-server",
		"--ws-auth", "signed-bearer-token",
		"--ws-shared-secret-file", absSecret,
		"--ws-issuer", " issuer ",
		"--ws-audience", "   ",
	})
	if err != nil {
		t.Fatalf("Parse signed bearer auth returned error: %v", err)
	}
	if signed.AppServer.WSSharedSecretFile != absSecret || signed.AppServer.WSIssuer != "issuer" || signed.AppServer.WSAudience != "" {
		t.Fatalf("signed bearer options = %#v", signed.AppServer)
	}

	_, err = Parse([]string{"app-server", "--ws-issuer="})
	if err == nil || !strings.Contains(err.Error(), "websocket auth flags require") {
		t.Fatalf("Parse empty ws issuer without auth error = %v", err)
	}
}

func TestParseDesktopApp(t *testing.T) {
	parsed, err := Parse([]string{"app", "--download-url", "https://example.test/codex", "."})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandApp || parsed.App.DownloadURL != "https://example.test/codex" || parsed.App.Path != "." {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseUpdate(t *testing.T) {
	parsed, err := Parse([]string{"update"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Command != CommandUpdate {
		t.Fatalf("parsed = %#v", parsed)
	}

	_, err = Parse([]string{"update", "--json"})
	if err == nil || !strings.Contains(err.Error(), "unknown update option --json") {
		t.Fatalf("Parse update --json error = %v", err)
	}
}
