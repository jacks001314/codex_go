package shell

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Rust parity: codex-rs/shell-command/src/parse_command.rs. Display-only command
// parsing summarizes an arbitrary model-authored command for the transcript and
// the app-server's commandActions. The parsing is intentionally lossy; the goal
// is a human-readable guess of what a command does, never a safety proof.

type DisplayCommandKind int

const (
	DisplayCommandRead DisplayCommandKind = iota
	DisplayCommandListFiles
	DisplayCommandSearch
	DisplayCommandUnknown
)

// DisplayCommand mirrors Rust's ParsedCommand.
type DisplayCommand struct {
	Kind DisplayCommandKind
	// Cmd is the command text attributed to the action (Rust `cmd`).
	Cmd string
	// Name is the display name of a read action (Rust `Read::name`).
	Name string
	// Path is the file or directory path when known.
	Path string
	// Query is a search pattern when known.
	Query string
}

// ParseDisplayCommands mirrors Rust `parse_command`: parse the command, drop
// consecutive duplicates, and collapse the sequence to a single Unknown action
// when any segment is unknown.
func ParseDisplayCommands(command []string) []DisplayCommand {
	parsed := parseDisplayCommandImpl(command)
	deduped := make([]DisplayCommand, 0, len(parsed))
	for _, cmd := range parsed {
		if len(deduped) > 0 && deduped[len(deduped)-1] == cmd {
			continue
		}
		deduped = append(deduped, cmd)
	}
	for _, cmd := range deduped {
		if cmd.Kind == DisplayCommandUnknown {
			return []DisplayCommand{{Kind: DisplayCommandUnknown, Cmd: singleUnknownForCommand(command)}}
		}
	}
	return deduped
}

func singleUnknownForCommand(command []string) string {
	if script, ok := extractShellCommandForDisplay(command); ok {
		return script
	}
	return ShlexJoin(command)
}

// extractShellCommandForDisplay mirrors Rust's extract_shell_command: the bash
// extraction (exactly `shell -c/-lc script`) or the PowerShell extraction
// (which tolerates shell flags such as -NoProfile before -Command/-c).
func extractShellCommandForDisplay(command []string) (string, bool) {
	if _, script, ok := ExtractPOSIXShellCommand(command); ok {
		return script, true
	}
	if _, script, ok := ExtractPowerShellCommand(command); ok {
		return script, true
	}
	return "", false
}

func parseDisplayCommandImpl(command []string) []DisplayCommand {
	if commands, ok := parseShellLCCommands(command); ok {
		return commands
	}
	powershellCommand := command
	if len(command) > 0 && strings.Contains(command[0], `\`) {
		normalized := append([]string(nil), command...)
		if index := strings.LastIndexAny(normalized[0], `/\`); index >= 0 {
			normalized[0] = normalized[0][index+1:]
		}
		powershellCommand = normalized
	}
	if _, script, ok := ExtractPowerShellCommand(powershellCommand); ok {
		tokens := TokenizePowerShellCommand(script)
		if len(tokens) > 0 && tokens[0] == "Get-Content" {
			if parsed := parseDisplayCommandImpl(tokens); len(parsed) == 1 && parsed[0].Kind == DisplayCommandRead {
				return []DisplayCommand{{Kind: DisplayCommandRead, Cmd: script, Name: parsed[0].Name, Path: parsed[0].Path}}
			}
		}
		return []DisplayCommand{{Kind: DisplayCommandUnknown, Cmd: script}}
	}

	normalized := normalizeCommandTokens(command)
	parts := [][]string{normalized}
	if containsDisplayConnectors(normalized) {
		parts = splitDisplayOnConnectors(normalized)
	}
	commands := make([]DisplayCommand, 0, len(parts))
	cwd := ""
	hasCWD := false
	for _, tokens := range parts {
		if len(tokens) > 0 && tokens[0] == "cd" {
			if target, ok := cdTarget(tokens[1:]); ok {
				if hasCWD {
					cwd = joinDisplayPaths(cwd, target)
				} else {
					cwd = target
					hasCWD = true
				}
			}
			continue
		}
		parsed := summarizeMainTokens(tokens)
		if parsed.Kind == DisplayCommandRead && hasCWD {
			parsed.Path = joinDisplayPaths(cwd, parsed.Path)
		}
		commands = append(commands, parsed)
	}
	for {
		next, ok := simplifyDisplayCommands(commands)
		if !ok {
			break
		}
		commands = next
	}
	return commands
}

func simplifyDisplayCommands(commands []DisplayCommand) ([]DisplayCommand, bool) {
	if len(commands) <= 1 {
		return nil, false
	}
	// echo ... && ...rest => ...rest
	if commands[0].Kind == DisplayCommandUnknown {
		if tokens := SplitCommandLine(commands[0].Cmd); len(tokens) > 0 && tokens[0] == "echo" {
			return append([]DisplayCommand(nil), commands[1:]...), true
		}
	}
	// cd foo && [any command] => [any command]
	for index, command := range commands {
		if command.Kind != DisplayCommandUnknown {
			continue
		}
		tokens := SplitCommandLine(command.Cmd)
		if len(tokens) > 0 && tokens[0] == "cd" && len(commands) > index+1 {
			out := make([]DisplayCommand, 0, len(commands)-1)
			out = append(out, commands[:index]...)
			out = append(out, commands[index+1:]...)
			return out, true
		}
	}
	// cmd || true => cmd
	for index, command := range commands {
		if command.Kind == DisplayCommandUnknown && command.Cmd == "true" {
			out := make([]DisplayCommand, 0, len(commands)-1)
			out = append(out, commands[:index]...)
			out = append(out, commands[index+1:]...)
			return out, true
		}
	}
	// nl -[any_flags] && ...rest => ...rest
	for index, command := range commands {
		if command.Kind != DisplayCommandUnknown {
			continue
		}
		tokens := SplitCommandLine(command.Cmd)
		if len(tokens) == 0 || tokens[0] != "nl" {
			continue
		}
		allFlags := true
		for _, token := range tokens[1:] {
			if !strings.HasPrefix(token, "-") {
				allFlags = false
				break
			}
		}
		if allFlags {
			out := make([]DisplayCommand, 0, len(commands)-1)
			out = append(out, commands[:index]...)
			out = append(out, commands[index+1:]...)
			return out, true
		}
	}
	return nil, false
}

func normalizeCommandTokens(cmd []string) []string {
	if len(cmd) >= 3 && (cmd[0] == "yes" || cmd[0] == "y" || cmd[0] == "no" || cmd[0] == "n") && cmd[1] == "|" {
		return append([]string(nil), cmd[2:]...)
	}
	if len(cmd) == 3 && (cmd[0] == "bash" || cmd[0] == "zsh") && (cmd[1] == "-c" || cmd[1] == "-lc") {
		if tokens := SplitCommandLine(cmd[2]); len(tokens) > 0 {
			return tokens
		}
		return append([]string(nil), cmd...)
	}
	return append([]string(nil), cmd...)
}

func containsDisplayConnectors(tokens []string) bool {
	for _, token := range tokens {
		switch token {
		case "&&", "||", "|", ";":
			return true
		}
	}
	return false
}

func splitDisplayOnConnectors(tokens []string) [][]string {
	out := [][]string{}
	current := []string{}
	for _, token := range tokens {
		switch token {
		case "&&", "||", "|", ";":
			if len(current) > 0 {
				out = append(out, current)
				current = []string{}
			}
		default:
			current = append(current, token)
		}
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out
}

func trimAtDisplayConnector(tokens []string) []string {
	for index, token := range tokens {
		switch token {
		case "|", "&&", "||", ";":
			return tokens[:index]
		}
	}
	return tokens
}

// shortDisplayPath shortens a path to its last meaningful component, mirroring
// Rust short_display_path (it skips build/dist/node_modules/src segments).
func shortDisplayPath(path string) string {
	normalized := strings.ReplaceAll(path, `\`, "/")
	trimmed := strings.TrimRight(normalized, "/")
	parts := strings.Split(trimmed, "/")
	for index := len(parts) - 1; index >= 0; index-- {
		part := parts[index]
		if part == "" || part == "build" || part == "dist" || part == "node_modules" || part == "src" {
			continue
		}
		return part
	}
	return trimmed
}

// skipFlagValues drops values consumed by the given flags and `--flag=value`
// forms, stopping at `--` where the remainder is positional.
func skipFlagValues(args []string, flagsWithValues []string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for index, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--" {
			out = append(out, args[index+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
			continue
		}
		if containsString(flagsWithValues, arg) {
			if index+1 < len(args) {
				skipNext = true
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

// displayOperand returns the first non-flag operand, skipping the values of
// the supplied flags (Rust parse_command::first_non_flag_operand).
func displayOperand(args []string, flagsWithValues ...string) (string, bool) {
	operands := nonFlagOperands(args, stringSet(flagsWithValues...))
	if len(operands) == 0 {
		return "", false
	}
	return operands[0], true
}

// displaySingleOperand returns the only non-flag operand, if there is exactly one.
func displaySingleOperand(args []string, flagsWithValues ...string) (string, bool) {
	operands := nonFlagOperands(args, stringSet(flagsWithValues...))
	if len(operands) != 1 {
		return "", false
	}
	return operands[0], true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func parseGrepLike(mainCmd []string, args []string) DisplayCommand {
	argsNoConnector := trimAtDisplayConnector(args)
	operands := []string{}
	pattern := ""
	hasPattern := false
	afterDoubleDash := false
	for index := 0; index < len(argsNoConnector); index++ {
		arg := argsNoConnector[index]
		if afterDoubleDash {
			operands = append(operands, arg)
			continue
		}
		if arg == "--" {
			afterDoubleDash = true
			continue
		}
		switch arg {
		case "-e", "--regexp", "-f", "--file":
			if index+1 < len(argsNoConnector) && !hasPattern {
				pattern = argsNoConnector[index+1]
				hasPattern = true
			}
			index++
			continue
		case "-m", "--max-count", "-C", "--context", "-A", "--after-context", "-B", "--before-context":
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		operands = append(operands, arg)
	}
	// Grep patterns may legitimately contain slashes; only paths are shortened.
	query := pattern
	if !hasPattern && len(operands) > 0 {
		query = operands[0]
	}
	pathIndex := 1
	if hasPattern {
		pathIndex = 0
	}
	path := ""
	if pathIndex < len(operands) {
		path = shortDisplayPath(operands[pathIndex])
	}
	return DisplayCommand{Kind: DisplayCommandSearch, Cmd: ShlexJoin(mainCmd), Query: query, Path: path}
}

func pythonWalksFiles(args []string) bool {
	argsNoConnector := trimAtDisplayConnector(args)
	for index := 0; index < len(argsNoConnector); index++ {
		if argsNoConnector[index] != "-c" || index+1 >= len(argsNoConnector) {
			continue
		}
		script := argsNoConnector[index+1]
		for _, marker := range []string{"os.walk", "os.listdir", "os.scandir", "glob.glob", "glob.iglob", "pathlib.Path", ".rglob("} {
			if strings.Contains(script, marker) {
				return true
			}
		}
		return false
	}
	return false
}

func isPythonCommand(cmd string) bool {
	return cmd == "python" || cmd == "python2" || cmd == "python3" ||
		strings.HasPrefix(cmd, "python2.") || strings.HasPrefix(cmd, "python3.")
}

func cdTarget(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	target := ""
	hasTarget := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			if index+1 < len(args) {
				return args[index+1], true
			}
			return "", false
		}
		if arg == "-L" || arg == "-P" || strings.HasPrefix(arg, "-") {
			continue
		}
		target = arg
		hasTarget = true
	}
	return target, hasTarget
}

// isPathish reports whether a token has an explicit path shape.
func isPathish(value string) bool {
	return value == "." || value == ".." ||
		strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") ||
		strings.Contains(value, "/") || strings.Contains(value, `\`)
}

func parseFdQueryAndPath(tail []string) (string, string) {
	argsNoConnector := trimAtDisplayConnector(tail)
	candidates := skipFlagValues(argsNoConnector, []string{"-t", "--type", "-e", "--extension", "-E", "--exclude", "--search-path"})
	nonFlags := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, "-") {
			nonFlags = append(nonFlags, candidate)
		}
	}
	switch len(nonFlags) {
	case 0:
		return "", ""
	case 1:
		if isPathish(nonFlags[0]) {
			return "", shortDisplayPath(nonFlags[0])
		}
		return nonFlags[0], ""
	default:
		return nonFlags[0], shortDisplayPath(nonFlags[1])
	}
}

func parseFindQueryAndPath(tail []string) (string, string) {
	argsNoConnector := trimAtDisplayConnector(tail)
	path := ""
	for _, arg := range argsNoConnector {
		if !strings.HasPrefix(arg, "-") && arg != "!" && arg != "(" && arg != ")" {
			path = shortDisplayPath(arg)
			break
		}
	}
	query := ""
	for index := 0; index < len(argsNoConnector); index++ {
		switch argsNoConnector[index] {
		case "-name", "-iname", "-path", "-regex":
			if index+1 < len(argsNoConnector) {
				query = argsNoConnector[index+1]
			}
			return query, path
		}
	}
	return query, path
}

// parseShellLCCommands mirrors Rust parse_shell_lc_commands: only bash/zsh
// `-c`/`-lc` invocations are unwrapped and parsed as shell scripts.
func parseShellLCCommands(original []string) ([]DisplayCommand, bool) {
	if _, script, ok := ExtractPOSIXShellCommand(original); ok {
		return ParseDisplayShellScript(script), true
	}
	return nil, false
}

// ParseDisplayShellScript mirrors Rust parse_shell_script: a script of
// word-only commands joined by `&&`, `||`, `;`, or `|` is summarized; anything
// else is a single Unknown action.
func ParseDisplayShellScript(script string) []DisplayCommand {
	allCommands, ok := parseWordOnlyCommandsSequence(script)
	if !ok || len(allCommands) == 0 {
		return []DisplayCommand{{Kind: DisplayCommandUnknown, Cmd: script}}
	}
	scriptTokens := SplitCommandLine(script)
	if len(scriptTokens) == 0 {
		scriptTokens = []string{script}
	}
	hadMultipleCommands := len(allCommands) > 1
	filtered := dropSmallFormattingCommands(allCommands)
	if len(filtered) == 0 {
		return []DisplayCommand{{Kind: DisplayCommandUnknown, Cmd: script}}
	}
	commands := make([]DisplayCommand, 0, len(filtered))
	cwd := ""
	hasCWD := false
	for _, tokens := range filtered {
		if len(tokens) > 0 && tokens[0] == "cd" {
			if target, ok := cdTarget(tokens[1:]); ok {
				if hasCWD {
					cwd = joinDisplayPaths(cwd, target)
				} else {
					cwd = target
					hasCWD = true
				}
			}
			continue
		}
		parsed := summarizeMainTokens(tokens)
		if parsed.Kind == DisplayCommandRead && hasCWD {
			parsed.Path = joinDisplayPaths(cwd, parsed.Path)
		}
		commands = append(commands, parsed)
	}
	if len(commands) > 1 {
		kept := commands[:0]
		for _, command := range commands {
			if command.Kind == DisplayCommandUnknown && command.Cmd == "true" {
				continue
			}
			kept = append(kept, command)
		}
		commands = kept
		for {
			next, ok := simplifyDisplayCommands(commands)
			if !ok {
				break
			}
			commands = next
		}
	}
	if len(commands) == 1 {
		// Attribute the full original script when there were no connectors; keep
		// the primary command for pipelines and multi-command scripts.
		hasConnectors := hadMultipleCommands
		if !hasConnectors {
			for _, token := range scriptTokens {
				switch token {
				case "|", "&&", "||", ";":
					hasConnectors = true
				}
			}
		}
		command := commands[0]
		switch command.Kind {
		case DisplayCommandRead:
			if hasConnectors {
				hasPipe := containsString(scriptTokens, "|")
				hasSedN := false
				for index := 0; index+1 < len(scriptTokens); index++ {
					if scriptTokens[index] == "sed" && scriptTokens[index+1] == "-n" {
						hasSedN = true
						break
					}
				}
				if hasPipe && hasSedN {
					command.Cmd = script
				}
			} else {
				command.Cmd = ShlexJoin(scriptTokens)
			}
		case DisplayCommandListFiles:
			if !hasConnectors {
				command.Cmd = ShlexJoin(scriptTokens)
			}
		case DisplayCommandSearch:
			if !hasConnectors {
				command.Cmd = ShlexJoin(scriptTokens)
			}
		}
		commands[0] = command
	}
	return commands
}

// parseWordOnlyCommandsSequence mirrors Rust
// try_parse_word_only_commands_sequence: the script must parse to plain word-only
// commands joined by the safe operators, with no redirections, substitutions,
// assignments, comments, or control flow.
func parseWordOnlyCommandsSequence(script string) ([][]string, bool) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, false
	}
	if file == nil || len(file.Last) > 0 {
		return nil, false
	}
	commands := [][]string{}
	for _, stmt := range file.Stmts {
		if !collectWordOnlyCommand(stmt, script, &commands) {
			return nil, false
		}
	}
	return commands, true
}

func collectWordOnlyCommand(stmt *syntax.Stmt, script string, commands *[][]string) bool {
	if stmt == nil || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) > 0 {
		return false
	}
	if len(stmt.Comments) > 0 {
		return false
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		if len(cmd.Assigns) > 0 {
			return false
		}
		words := make([]string, 0, len(cmd.Args))
		for _, arg := range cmd.Args {
			word, ok := literalWordValue(arg, script)
			if !ok {
				return false
			}
			words = append(words, word)
		}
		if len(words) == 0 {
			return false
		}
		*commands = append(*commands, words)
		return true
	case *syntax.BinaryCmd:
		if cmd.Op != syntax.AndStmt && cmd.Op != syntax.OrStmt && cmd.Op != syntax.Pipe {
			return false
		}
		return collectWordOnlyCommand(cmd.X, script, commands) && collectWordOnlyCommand(cmd.Y, script, commands)
	default:
		return false
	}
}

// literalWordValue extracts a word's literal value, mirroring Rust's
// parse_plain_command_from_node. A bare word must be free of expansions, globs,
// escapes, and brace/equals expansion (Rust's `is_literal_word_or_number`);
// single-quoted text is raw; double-quoted text keeps its content unless it
// carries an escape sequence the shell would strip or rewrite.
func literalWordValue(word *syntax.Word, script string) (string, bool) {
	if word == nil {
		return "", false
	}
	var builder strings.Builder
	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			if typed.Value == "" || !isLiteralBareWord(typed.Value) {
				return "", false
			}
			builder.WriteString(typed.Value)
		case *syntax.SglQuoted:
			if typed.Dollar {
				return "", false
			}
			builder.WriteString(typed.Value)
		case *syntax.DblQuoted:
			value, ok := literalDoubleQuotedValue(typed)
			if !ok {
				return "", false
			}
			builder.WriteString(value)
		default:
			return "", false
		}
	}
	value := builder.String()
	if value == "" {
		return "", false
	}
	return value, true
}

// isLiteralBareWord mirrors Rust's is_literal_word_or_number spelling check:
// a tree-sitter word can still undergo shell expansion or escape removal, so
// its source spelling is never taken as proof of the runtime argv.
func isLiteralBareWord(value string) bool {
	if strings.HasPrefix(value, "=") {
		return false
	}
	return !strings.ContainsAny(value, "{}*?[]\\~^#$`")
}

// literalDoubleQuotedValue mirrors Rust's parse_double_quoted_string: every part
// must be plain content, and the source may not contain a backslash escape of
// `$`, “ ` “, `"`, `\`, or a newline.
func literalDoubleQuotedValue(quoted *syntax.DblQuoted) (string, bool) {
	if quoted.Dollar {
		return "", false
	}
	var builder strings.Builder
	for _, part := range quoted.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			return "", false
		}
		builder.WriteString(lit.Value)
	}
	value := builder.String()
	for i := 0; i+1 < len(value); i++ {
		if value[i] != '\\' {
			continue
		}
		switch value[i+1] {
		case '$', '`', '"', '\\', '\n':
			return "", false
		}
	}
	return value, true
}

func dropSmallFormattingCommands(commands [][]string) [][]string {
	kept := commands[:0]
	for _, tokens := range commands {
		if isSmallFormattingCommand(tokens) {
			continue
		}
		kept = append(kept, tokens)
	}
	return kept
}

// isSmallFormattingCommand reports whether a pipeline segment is a small
// formatting helper rather than the primary command.
func isSmallFormattingCommand(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	switch tokens[0] {
	case "wc", "tr", "cut", "sort", "uniq", "tee", "column", "yes", "printf":
		return true
	case "xargs":
		return !isMutatingXargsCommand(tokens)
	case "awk":
		return awkReadPath(tokens[1:]) == ""
	case "head":
		switch len(tokens) {
		case 1:
			return true
		case 2:
			return strings.HasPrefix(tokens[1], "-")
		case 3:
			if tokens[1] == "-n" || tokens[1] == "-c" {
				return isDecimal(tokens[2], false)
			}
		}
		return false
	case "tail":
		switch len(tokens) {
		case 1:
			return true
		case 2:
			return strings.HasPrefix(tokens[1], "-")
		case 3:
			if tokens[1] == "-n" || tokens[1] == "-c" {
				count := strings.TrimPrefix(tokens[2], "+")
				return isDecimal(count, false)
			}
		}
		return false
	case "sed":
		args := tokens[1:]
		return !sedHasInPlaceFlag(args) && sedReadPath(args) == ""
	default:
		return false
	}
}

func isMutatingXargsCommand(tokens []string) bool {
	subcommand, ok := xargsSubcommand(tokens)
	return ok && xargsIsMutatingSubcommand(subcommand)
}

func xargsSubcommand(tokens []string) ([]string, bool) {
	if len(tokens) == 0 || tokens[0] != "xargs" {
		return nil, false
	}
	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		if token == "--" {
			if index+1 < len(tokens) {
				return tokens[index+1:], true
			}
			return nil, false
		}
		if !strings.HasPrefix(token, "-") {
			return tokens[index:], true
		}
		takesValue := token == "-E" || token == "-e" || token == "-I" || token == "-L" || token == "-n" || token == "-P" || token == "-s"
		if takesValue && len(token) == 2 {
			index++
		}
	}
	return nil, false
}

func xargsIsMutatingSubcommand(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	switch tokens[0] {
	case "perl", "ruby":
		return hasInPlaceFlag(tokens[1:])
	case "sed":
		return sedHasInPlaceFlag(tokens[1:])
	case "rg":
		return containsString(tokens[1:], "--replace")
	default:
		return false
	}
}

func hasInPlaceFlag(tokens []string) bool {
	for _, token := range tokens {
		if token == "-i" || strings.HasPrefix(token, "-i") || token == "-pi" || strings.HasPrefix(token, "-pi") ||
			token == "--in-place" || strings.HasPrefix(token, "--in-place=") {
			return true
		}
	}
	return false
}

func isAbsLike(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	if len(path) >= 3 && isASCIIAlpha(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}

func isASCIIAlpha(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func joinDisplayPaths(base string, rel string) string {
	if isAbsLike(rel) {
		return rel
	}
	if base == "" {
		return rel
	}
	if strings.HasPrefix(base, "/") {
		return strings.TrimRight(base, "/") + "/" + strings.TrimPrefix(rel, "./")
	}
	return filepath.Join(base, rel)
}

// summarizeMainTokens mirrors Rust summarize_main_tokens: classify the primary
// (non-formatting) segment of a pipeline into a read, listing, or search action.
func summarizeMainTokens(mainCmd []string) DisplayCommand {
	if len(mainCmd) == 0 {
		return DisplayCommand{Kind: DisplayCommandUnknown, Cmd: ShlexJoin(mainCmd)}
	}
	head := mainCmd[0]
	tail := mainCmd[1:]
	unknown := func() DisplayCommand {
		return DisplayCommand{Kind: DisplayCommandUnknown, Cmd: ShlexJoin(mainCmd)}
	}
	readCommand := func(path string) DisplayCommand {
		return DisplayCommand{Kind: DisplayCommandRead, Cmd: ShlexJoin(mainCmd), Name: shortDisplayPath(path), Path: path}
	}
	switch {
	case head == "ls" || head == "eza" || head == "exa":
		flagsWithValues := []string{}
		switch head {
		case "ls":
			flagsWithValues = []string{"-I", "-w", "--block-size", "--format", "--time-style", "--color", "--quoting-style"}
		default:
			flagsWithValues = []string{"-I", "--ignore-glob", "--color", "--sort", "--time-style", "--time"}
		}
		path := ""
		if operand, ok := displayOperand(tail, flagsWithValues...); ok {
			path = shortDisplayPath(operand)
		}
		return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
	case head == "tree":
		path := ""
		if operand, ok := displayOperand(tail, "-L", "-P", "-I", "--charset", "--filelimit", "--sort"); ok {
			path = shortDisplayPath(operand)
		}
		return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
	case head == "du":
		path := ""
		if operand, ok := displayOperand(tail, "-d", "--max-depth", "-B", "--block-size", "--exclude", "--time-style"); ok {
			path = shortDisplayPath(operand)
		}
		return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
	case head == "rg" || head == "rga" || head == "ripgrep-all":
		argsNoConnector := trimAtDisplayConnector(tail)
		hasFilesFlag := containsString(argsNoConnector, "--files")
		candidates := skipFlagValues(argsNoConnector, []string{
			"-g", "--glob", "--iglob", "-t", "--type", "--type-add", "--type-not",
			"-m", "--max-count", "-A", "-B", "-C", "--context", "--max-depth",
		})
		nonFlags := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			if !strings.HasPrefix(candidate, "-") {
				nonFlags = append(nonFlags, candidate)
			}
		}
		if hasFilesFlag {
			path := ""
			if len(nonFlags) > 0 {
				path = shortDisplayPath(nonFlags[0])
			}
			return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
		}
		query := ""
		path := ""
		if len(nonFlags) > 0 {
			query = nonFlags[0]
		}
		if len(nonFlags) > 1 {
			path = shortDisplayPath(nonFlags[1])
		}
		return DisplayCommand{Kind: DisplayCommandSearch, Cmd: ShlexJoin(mainCmd), Query: query, Path: path}
	case head == "git":
		if len(tail) == 0 {
			return unknown()
		}
		switch tail[0] {
		case "grep":
			return parseGrepLike(mainCmd, tail[1:])
		case "ls-files":
			path := ""
			if operand, ok := displayOperand(tail[1:], "--exclude", "--exclude-from", "--pathspec-from-file"); ok {
				path = shortDisplayPath(operand)
			}
			return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
		default:
			return unknown()
		}
	case head == "fd":
		query, path := parseFdQueryAndPath(tail)
		if query != "" {
			return DisplayCommand{Kind: DisplayCommandSearch, Cmd: ShlexJoin(mainCmd), Query: query, Path: path}
		}
		return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
	case head == "find":
		query, path := parseFindQueryAndPath(tail)
		if query != "" {
			return DisplayCommand{Kind: DisplayCommandSearch, Cmd: ShlexJoin(mainCmd), Query: query, Path: path}
		}
		return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd), Path: path}
	case head == "grep" || head == "egrep" || head == "fgrep":
		return parseGrepLike(mainCmd, tail)
	case head == "ag" || head == "ack" || head == "pt":
		argsNoConnector := trimAtDisplayConnector(tail)
		candidates := skipFlagValues(argsNoConnector, []string{"-G", "-g", "--file-search-regex", "--ignore-dir", "--ignore-file", "--path-to-ignore"})
		nonFlags := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			if !strings.HasPrefix(candidate, "-") {
				nonFlags = append(nonFlags, candidate)
			}
		}
		query := ""
		path := ""
		if len(nonFlags) > 0 {
			query = nonFlags[0]
		}
		if len(nonFlags) > 1 {
			path = shortDisplayPath(nonFlags[1])
		}
		return DisplayCommand{Kind: DisplayCommandSearch, Cmd: ShlexJoin(mainCmd), Query: query, Path: path}
	case head == "cat" || strings.EqualFold(head, "Get-Content"):
		if head == "cat" {
			if path, ok := displaySingleOperand(tail); ok {
				return readCommand(path)
			}
			return unknown()
		}
		// Conservative PowerShell handling: only simple paths are recognized.
		for _, argument := range tail {
			if strings.HasPrefix(argument, "-") &&
				!strings.EqualFold(argument, "-Raw") &&
				!strings.EqualFold(argument, "-Path") &&
				!strings.EqualFold(argument, "-LiteralPath") {
				return unknown()
			}
		}
		path, ok := displaySingleOperand(tail)
		if !ok || path == "" || strings.HasPrefix(path, "-") || !isSimplePowerShellPath(path) {
			return unknown()
		}
		return readCommand(path)
	case head == "bat" || head == "batcat":
		if path, ok := displaySingleOperand(tail, "--theme", "--language", "--style", "--terminal-width", "--tabs", "--line-range", "--map-syntax"); ok {
			return readCommand(path)
		}
		return unknown()
	case head == "less":
		if path, ok := displaySingleOperand(tail, "-p", "-P", "-x", "-y", "-z", "-j", "--pattern", "--prompt", "--tabs", "--shift", "--jump-target"); ok {
			return readCommand(path)
		}
		return unknown()
	case head == "more":
		if path, ok := displaySingleOperand(tail); ok {
			return readCommand(path)
		}
		return unknown()
	case head == "head":
		if path := headReadPath(tail); path != "" {
			return readCommand(path)
		}
		return unknown()
	case head == "tail":
		if path := tailReadPath(tail); path != "" {
			return readCommand(path)
		}
		return unknown()
	case head == "awk":
		if path := awkReadPath(tail); path != "" {
			return readCommand(path)
		}
		return unknown()
	case head == "nl":
		candidates := skipFlagValues(tail, []string{"-s", "-w", "-v", "-i", "-b"})
		for _, candidate := range candidates {
			if !strings.HasPrefix(candidate, "-") {
				return readCommand(candidate)
			}
		}
		return unknown()
	case head == "sed":
		if path := sedReadPath(tail); path != "" {
			return readCommand(path)
		}
		return unknown()
	case isPythonCommand(head):
		if pythonWalksFiles(tail) {
			return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: ShlexJoin(mainCmd)}
		}
		return unknown()
	default:
		return unknown()
	}
}

func isSimplePowerShellPath(path string) bool {
	for _, char := range path {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case ' ', '/', '\\', '.', '-', '_', ':':
			continue
		}
		return false
	}
	return true
}
