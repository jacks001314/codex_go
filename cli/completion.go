package cli

import (
	"fmt"
	"runtime"
	"strings"
)

type completionNode struct {
	Name        string
	Aliases     []string
	Options     []string
	Subcommands []*completionNode
}

type completionRow struct {
	Path  string
	Words []string
}

type completionTransition struct {
	From string
	Word string
	To   string
}

var configCompletionOptions = []string{"-c", "--config"}

var sharedCompletionOptions = []string{
	"-i", "--image",
	"-m", "--model",
	"--oss",
	"--local-provider",
	"-p", "--profile",
	"-s", "--sandbox",
	"--dangerously-bypass-approvals-and-sandbox",
	"--yolo",
	"--dangerously-bypass-hook-trust",
	"-C", "--cd",
	"--add-dir",
}

var tuiOnlyCompletionOptions = []string{
	"-a", "--ask-for-approval",
	"--search",
	"--no-alt-screen",
}

func GenerateCompletion(shell string, commandName string) (string, error) {
	if strings.TrimSpace(commandName) == "" {
		commandName = "codex"
	}
	spec := codexCompletionSpec()
	switch shell {
	case "bash":
		return bashCompletion(commandName, spec), nil
	case "zsh":
		return zshCompletion(commandName, spec), nil
	case "fish":
		return fishCompletion(commandName, spec), nil
	case "powershell":
		return powershellCompletion(commandName, spec), nil
	case "elvish":
		return elvishCompletion(commandName, spec), nil
	default:
		return "", fmt.Errorf("unsupported completion shell %s", shell)
	}
}

func codexCompletionSpec() *completionNode {
	return node("", rootCompletionOptions(),
		nodeWithAliases("exec", []string{"e"}, execCompletionOptions(),
			node("resume", execResumeCompletionOptions()),
			node("review", reviewCompletionOptions()),
		),
		node("review", reviewCompletionOptions()),
		node("login", combineCompletionOptions(configCompletionOptions,
			[]string{"--with-api-key", "--with-access-token", "--api-key", "--device-auth"}),
			node("status", configCompletionOptions),
		),
		node("logout", configCompletionOptions),
		node("mcp", configCompletionOptions,
			node("list", combineCompletionOptions(configCompletionOptions, []string{"--json"})),
			node("get", combineCompletionOptions(configCompletionOptions, []string{"--json"})),
			node("add", combineCompletionOptions(configCompletionOptions,
				[]string{"--url", "--env", "--bearer-token-env-var", "--oauth-client-id", "--oauth-resource"})),
			node("remove", configCompletionOptions),
			node("login", combineCompletionOptions(configCompletionOptions, []string{"--scopes"})),
			node("logout", configCompletionOptions),
		),
		node("plugin", configCompletionOptions,
			node("add", pluginSelectorCompletionOptions()),
			node("list", combineCompletionOptions(configCompletionOptions, []string{"-m", "--marketplace", "--json", "--available"})),
			node("marketplace", configCompletionOptions,
				node("add", combineCompletionOptions(configCompletionOptions, []string{"--ref", "--sparse", "--json"})),
				node("list", combineCompletionOptions(configCompletionOptions, []string{"--json"})),
				node("upgrade", combineCompletionOptions(configCompletionOptions, []string{"--json"})),
				node("remove", combineCompletionOptions(configCompletionOptions, []string{"--json"})),
			),
			node("remove", pluginSelectorCompletionOptions()),
		),
		node("mcp-server", []string{"--strict-config"}),
		node("app-server", appServerCompletionOptions(),
			node("daemon", nil,
				node("bootstrap", []string{"--remote-control"}),
				node("start", nil),
				node("restart", nil),
				node("enable-remote-control", nil),
				node("disable-remote-control", nil),
				node("stop", nil),
				node("version", nil),
			),
			node("proxy", []string{"--sock"}),
			node("generate-ts", []string{"-o", "--out", "-p", "--prettier", "--experimental"}),
			node("generate-json-schema", []string{"-o", "--out", "--experimental"}),
		),
		node("remote-control", []string{"--json"},
			node("start", []string{"--json"}),
			node("stop", []string{"--json"}),
			node("pair", []string{"--json"}),
		),
		node("app", []string{"--download-url"}),
		node("completion", nil,
			node("bash", nil),
			node("elvish", nil),
			node("fish", nil),
			node("powershell", nil),
			node("zsh", nil),
		),
		node("update", nil),
		node("doctor", []string{"--json", "--summary", "--all", "--no-color", "--ascii", "--feedback"}),
		node("sandbox", sandboxCompletionOptions(),
			node("setup", []string{"--elevated", "--user", "--current-user", "--codex-home"}),
		),
		node("debug", nil,
			node("models", []string{"--bundled"}),
			node("app-server", nil,
				node("send-message-v2", nil),
			),
			node("prompt-input", []string{"-i", "--image"}),
			node("clear-memories", nil),
			node("config", nil),
		),
		nodeWithAliases("apply", []string{"a"}, configCompletionOptions),
		node("resume", sessionSelectionCompletionOptions(true)),
		node("archive", sessionMutationCompletionOptions(false)),
		node("delete", sessionMutationCompletionOptions(true)),
		node("unarchive", sessionMutationCompletionOptions(false)),
		node("fork", sessionSelectionCompletionOptions(false)),
		node("cloud", configCompletionOptions,
			node("exec", combineCompletionOptions(configCompletionOptions, []string{"--env", "--attempts", "--branch"})),
			node("status", nil),
			node("list", combineCompletionOptions(configCompletionOptions, []string{"--env", "--limit", "--cursor", "--json"})),
			node("apply", []string{"--attempt"}),
			node("diff", []string{"--attempt"}),
		),
		node("exec-server", []string{"--strict-config", "--listen", "--remote", "--environment-id", "--name", "--use-agent-identity-auth", "--exit-on-stdin-close"}),
		node("features", nil,
			node("list", nil),
			node("enable", nil),
			node("disable", nil),
		),
	)
}

func rootCompletionOptions() []string {
	return combineCompletionOptions(configCompletionOptions, sharedCompletionOptions,
		[]string{
			"--enable",
			"--disable",
			"--remote",
			"--remote-auth-token-env",
			"--strict-config",
			"-a",
			"--ask-for-approval",
			"--search",
			"--no-alt-screen",
		})
}

func execCompletionOptions() []string {
	return combineCompletionOptions(configCompletionOptions, sharedCompletionOptions,
		[]string{
			"--strict-config",
			"--skip-git-repo-check",
			"--ephemeral",
			"--ignore-user-config",
			"--ignore-rules",
			"--output-schema",
			"--color",
			"--json",
			"--experimental-json",
			"-o",
			"--output-last-message",
		})
}

func execResumeCompletionOptions() []string {
	return combineCompletionOptions(execCompletionOptions(), []string{"--last", "--all"})
}

func reviewCompletionOptions() []string {
	return combineCompletionOptions(configCompletionOptions, sharedCompletionOptions,
		[]string{"--strict-config", "--uncommitted", "--base", "--commit", "--title"})
}

func pluginSelectorCompletionOptions() []string {
	return combineCompletionOptions(configCompletionOptions, []string{"-m", "--marketplace", "--json"})
}

func appServerCompletionOptions() []string {
	return []string{
		"--strict-config",
		"--listen",
		"--stdio",
		"--analytics-default-enabled",
		"--code-mode-host",
		"--ws-auth",
		"--ws-token-file",
		"--ws-token-sha256",
		"--ws-shared-secret-file",
		"--ws-issuer",
		"--ws-audience",
		"--ws-max-clock-skew-seconds",
	}
}

func sandboxCompletionOptions() []string {
	options := []string{
		"-P",
		"--permission-profile",
		"--permissions-profile",
		"-p",
		"--profile",
		"-C",
		"--cd",
		"--include-managed-config",
		"--sandbox-state-json",
		"--sandbox-state-readable-root",
		"--sandbox-state-disable-network",
	}
	if runtime.GOOS == "darwin" {
		options = append(options, "--allow-unix-socket", "--log-denials")
	}
	return combineCompletionOptions(configCompletionOptions, options)
}

func sessionSelectionCompletionOptions(includeNonInteractive bool) []string {
	options := combineCompletionOptions(configCompletionOptions, sharedCompletionOptions, tuiOnlyCompletionOptions,
		[]string{"--last", "--all", "--remote", "--remote-auth-token-env", "--strict-config"})
	if includeNonInteractive {
		options = combineCompletionOptions(options, []string{"--include-non-interactive"})
	}
	return options
}

func sessionMutationCompletionOptions(allowForce bool) []string {
	options := combineCompletionOptions(configCompletionOptions, sharedCompletionOptions,
		[]string{"--remote", "--remote-auth-token-env", "--strict-config"})
	if allowForce {
		options = combineCompletionOptions(options, []string{"--force"})
	}
	return options
}

func node(name string, options []string, subcommands ...*completionNode) *completionNode {
	return nodeWithAliases(name, nil, options, subcommands...)
}

func nodeWithAliases(name string, aliases []string, options []string, subcommands ...*completionNode) *completionNode {
	return &completionNode{
		Name:        name,
		Aliases:     append([]string(nil), aliases...),
		Options:     uniqueCompletionWords(options),
		Subcommands: subcommands,
	}
}

func combineCompletionOptions(groups ...[]string) []string {
	var out []string
	for _, group := range groups {
		out = append(out, group...)
	}
	return uniqueCompletionWords(out)
}

func uniqueCompletionWords(words []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(words))
	for _, word := range words {
		if word == "" {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	return out
}

func (n *completionNode) names() []string {
	if n == nil || n.Name == "" {
		return nil
	}
	return append([]string{n.Name}, n.Aliases...)
}

func completionRows(root *completionNode) ([]completionRow, []completionTransition) {
	var rows []completionRow
	var transitions []completionTransition
	collectCompletionRows(root, nil, &rows, &transitions)
	return rows, transitions
}

func collectCompletionRows(node *completionNode, path []string, rows *[]completionRow, transitions *[]completionTransition) {
	if node == nil {
		return
	}
	pathKey := strings.Join(path, " ")
	words := append([]string(nil), node.Options...)
	for _, child := range node.Subcommands {
		words = append(words, child.names()...)
		childPath := append(append([]string(nil), path...), child.Name)
		childKey := strings.Join(childPath, " ")
		for _, name := range child.names() {
			*transitions = append(*transitions, completionTransition{
				From: pathKey,
				Word: name,
				To:   childKey,
			})
		}
	}
	*rows = append(*rows, completionRow{Path: pathKey, Words: uniqueCompletionWords(words)})
	for _, child := range node.Subcommands {
		collectCompletionRows(child, append(append([]string(nil), path...), child.Name), rows, transitions)
	}
}

func flattenedCompletionWords(root *completionNode) []string {
	rows, _ := completionRows(root)
	var words []string
	for _, row := range rows {
		words = append(words, row.Words...)
	}
	return uniqueCompletionWords(words)
}

func bashCompletion(commandName string, root *completionNode) string {
	rows, transitions := completionRows(root)
	var builder strings.Builder
	fmt.Fprintf(&builder, "# bash completion for %s\n", commandName)
	fmt.Fprintf(&builder, "_%s_complete() {\n", commandName)
	builder.WriteString("  local cur path word words\n")
	builder.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	builder.WriteString("  path=\"\"\n")
	builder.WriteString("  for ((i=1; i<COMP_CWORD; i++)); do\n")
	builder.WriteString("    word=\"${COMP_WORDS[i]}\"\n")
	builder.WriteString("    [[ \"$word\" == -* ]] && continue\n")
	builder.WriteString("    case \"${path}|${word}\" in\n")
	for _, transition := range transitions {
		fmt.Fprintf(&builder, "      %s) path=%s ;;\n", bashCasePattern(transition.From, transition.Word), shellSingleQuote(transition.To))
	}
	builder.WriteString("    esac\n")
	builder.WriteString("  done\n")
	builder.WriteString("  case \"$path\" in\n")
	for _, row := range rows {
		fmt.Fprintf(&builder, "    %s) words=%s ;;\n", shellSingleQuote(row.Path), shellSingleQuote(strings.Join(row.Words, " ")))
	}
	builder.WriteString("    *) words=\"\" ;;\n")
	builder.WriteString("  esac\n")
	builder.WriteString("  COMPREPLY=( $(compgen -W \"$words\" -- \"$cur\") )\n")
	builder.WriteString("}\n")
	fmt.Fprintf(&builder, "complete -F _%s_complete %s\n", commandName, commandName)
	return builder.String()
}

func zshCompletion(commandName string, root *completionNode) string {
	return fmt.Sprintf(`#compdef %[1]s
_%[1]s() {
  local -a words
  words=(%[2]s)
  compadd -- $words
}
_%[1]s "$@"
`, commandName, zshArrayWords(flattenedCompletionWords(root)))
}

func fishCompletion(commandName string, root *completionNode) string {
	var builder strings.Builder
	words := flattenedCompletionWords(root)
	for _, word := range words {
		fmt.Fprintf(&builder, "complete -c %s -f -a %s\n", commandName, shellSingleQuote(word))
	}
	return builder.String()
}

func powershellCompletion(commandName string, root *completionNode) string {
	rows, transitions := completionRows(root)
	var builder strings.Builder
	fmt.Fprintf(&builder, "Register-ArgumentCompleter -Native -CommandName %s -ScriptBlock {\n", psSingleQuote(commandName))
	builder.WriteString("  param($wordToComplete, $commandAst, $cursorPosition)\n")
	builder.WriteString("  $wordsByPath = @{\n")
	for _, row := range rows {
		fmt.Fprintf(&builder, "    %s = @(%s)\n", psSingleQuote(row.Path), psArrayWords(row.Words))
	}
	builder.WriteString("  }\n")
	builder.WriteString("  $transitions = @{\n")
	for _, transition := range transitions {
		key := transition.From + "|" + transition.Word
		fmt.Fprintf(&builder, "    %s = %s\n", psSingleQuote(key), psSingleQuote(transition.To))
	}
	builder.WriteString("  }\n")
	builder.WriteString("  $path = ''\n")
	builder.WriteString("  $tokens = @($commandAst.CommandElements | Select-Object -Skip 1)\n")
	builder.WriteString("  foreach ($token in $tokens) {\n")
	builder.WriteString("    $text = $token.ToString()\n")
	builder.WriteString("    if ($text.StartsWith('-')) { continue }\n")
	builder.WriteString("    $key = \"$path|$text\"\n")
	builder.WriteString("    if ($transitions.ContainsKey($key)) { $path = $transitions[$key] }\n")
	builder.WriteString("  }\n")
	builder.WriteString("  if (-not $wordsByPath.ContainsKey($path)) { return }\n")
	builder.WriteString("  $wordsByPath[$path] | Where-Object { $_ -like \"$wordToComplete*\" } | ForEach-Object {\n")
	builder.WriteString("    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)\n")
	builder.WriteString("  }\n")
	builder.WriteString("}\n")
	return builder.String()
}

func elvishCompletion(commandName string, root *completionNode) string {
	return fmt.Sprintf(`set edit:completion:arg-completer[%s] = {|@words|
  put %s
}
`, commandName, strings.Join(elvishWords(flattenedCompletionWords(root)), " "))
}

func bashCasePattern(path string, word string) string {
	return shellSingleQuote(path + "|" + word)
}

func zshArrayWords(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, shellSingleQuote(word))
	}
	return strings.Join(quoted, " ")
}

func psArrayWords(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, psSingleQuote(word))
	}
	return strings.Join(quoted, ", ")
}

func elvishWords(words []string) []string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, shellSingleQuote(word))
	}
	return quoted
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func psSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
