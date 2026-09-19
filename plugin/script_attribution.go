package plugin

import (
	"os"
	"path/filepath"
	"strings"

	"codex_go/shell"
)

const trustedRemoteMarketplaceName = "openai-curated-remote"

type PluginCommandAttribution struct {
	PluginID   string
	ScriptPath string
}

type trustedPluginRoot struct {
	pluginID                string
	// version is the plugin version this root belongs to (Rust #46528). Trusted
	// roots keep the version so measurement declarations stay bound to the
	// root that actually matched the script.
	version                 string
	root                    string
	metricsOperationsByPath map[string]PluginMetricsOperation
}

type TrustedPluginRoots struct {
	roots []trustedPluginRoot
}

// NewTrustedPluginRoots builds attribution roots only from verified, installed
// remote plugin cache entries. Local versions and entries without remote
// identity metadata are intentionally excluded.
func NewTrustedPluginRoots(codexHome string, pluginIDs []string) TrustedPluginRoots {
	store, err := NewPluginStore(codexHome)
	if err != nil {
		return TrustedPluginRoots{}
	}
	seen := map[string]bool{}
	roots := make([]trustedPluginRoot, 0, len(pluginIDs))
	for _, value := range pluginIDs {
		id, err := ParsePluginId(value)
		if err != nil || id.MarketplaceName != trustedRemoteMarketplaceName {
			continue
		}
		version, ok := store.ActivePluginVersion(id)
		if !ok || version == DefaultPluginVersion {
			continue
		}
		remoteID, err := store.RemotePluginID(id)
		if err != nil || strings.TrimSpace(remoteID) == "" {
			continue
		}
		root, err := filepath.EvalSymlinks(store.PluginRoot(id, version))
		if err != nil {
			continue
		}
		root, err = filepath.Abs(root)
		if err != nil {
			continue
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		key := id.Key() + "\x00" + root
		if seen[key] {
			continue
		}
		seen[key] = true
		metrics := loadPluginMetricsOperations(root)
		roots = append(roots, trustedPluginRoot{pluginID: id.Key(), version: version, root: filepath.Clean(root), metricsOperationsByPath: metrics})
	}
	return TrustedPluginRoots{roots: roots}
}

// matchedPluginScript is one trusted root plus the normalized script that
// matched inside it (Rust #46528's MatchedPluginScript).
type matchedPluginScript struct {
	attribution PluginCommandAttribution
	metrics     *PluginMetricsOperation
}

func (m matchedPluginScript) metricsOperation(pluginID string) *ResolvedPluginMetricsOperation {
	if m.metrics == nil {
		return nil
	}
	return &ResolvedPluginMetricsOperation{PluginID: pluginID, Operation: *m.metrics}
}

// matchedScript resolves one exact command to the single trusted root that owns
// the canonical script. Reusing this for attribution and for measurement
// lookup binds a manifest declaration to the version that matched instead of
// looking the declaration up across every version of the plugin (#46528).
func (r TrustedPluginRoots) matchedScript(command []string, cwd string) *matchedPluginScript {
	plain, ok := singlePlainPluginCommand(command)
	if !ok {
		return nil
	}
	script, ok := pluginScriptArgument(plain)
	if !ok {
		return nil
	}
	if !filepath.IsAbs(script) {
		script = filepath.Join(cwd, script)
	}
	script, err := filepath.EvalSymlinks(script)
	if err != nil {
		return nil
	}
	script, err = filepath.Abs(script)
	if err != nil {
		return nil
	}
	info, err := os.Stat(script)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}

	var match *matchedPluginScript
	for _, root := range r.roots {
		relative, err := filepath.Rel(root.root, script)
		if err != nil || relative == "." || pathEscapesRoot(relative) {
			continue
		}
		normalized := filepath.ToSlash(relative)
		if !IsSafePluginRelativePath(normalized) {
			continue
		}
		if match != nil {
			return nil
		}
		var metrics *PluginMetricsOperation
		if operation, ok := root.metricsOperationsByPath[normalized]; ok {
			operationCopy := operation
			metrics = &operationCopy
		}
		match = &matchedPluginScript{
			attribution: PluginCommandAttribution{PluginID: root.pluginID, ScriptPath: normalized},
			metrics:     metrics,
		}
	}
	return match
}

func (r TrustedPluginRoots) Resolve(command []string, cwd string) *PluginCommandAttribution {
	matched := r.matchedScript(command, cwd)
	if matched == nil {
		return nil
	}
	attribution := matched.attribution
	return &attribution
}

func IsSafePluginRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
		return false
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
		if len(component) >= 2 && isASCIIAlpha(component[0]) && component[1] == ':' {
			return false
		}
	}
	return true
}

func singlePlainPluginCommand(command []string) ([]string, bool) {
	if len(command) == 0 {
		return nil, false
	}
	program := strings.ToLower(strings.TrimSuffix(filepath.Base(command[0]), ".exe"))
	if (program == "bash" || program == "sh" || program == "zsh") && len(command) == 3 && (command[1] == "-c" || command[1] == "-lc") {
		return plainShellTokens(command[2])
	}
	if program == "cmd" && len(command) == 3 && strings.EqualFold(command[1], "/c") {
		return plainShellTokens(command[2])
	}
	if program == "powershell" || program == "pwsh" {
		if len(command) < 3 {
			return command, true
		}
		commandFlag := strings.ToLower(command[len(command)-2])
		if commandFlag != "-command" && commandFlag != "-c" {
			return command, true
		}
		for _, flag := range command[1 : len(command)-2] {
			switch strings.ToLower(flag) {
			case "-nologo", "-noprofile", "-noninteractive":
			default:
				return nil, false
			}
		}
		return plainShellTokens(command[len(command)-1])
	}
	return append([]string(nil), command...), true
}

func plainShellTokens(script string) ([]string, bool) {
	tokens := shell.SplitCommandLine(script)
	if len(tokens) == 0 {
		return nil, false
	}
	for _, token := range tokens {
		switch token {
		case "&&", "||", ";", "|", "&", ">", ">>", "<":
			return nil, false
		}
	}
	return tokens, true
}

func pluginScriptArgument(command []string) (string, bool) {
	if len(command) == 0 {
		return "", false
	}
	program := strings.ToLower(strings.TrimSuffix(filepath.Base(command[0]), ".exe"))
	switch program {
	case "powershell", "pwsh":
		if len(command) >= 3 && strings.EqualFold(command[1], "-file") && !strings.HasPrefix(command[2], "-") {
			return command[2], true
		}
		return "", false
	case "bash", "sh", "zsh", "python", "python3", "node", "nodejs", "perl", "php", "ruby":
		args := command[1:]
		for len(args) > 0 {
			if args[0] == "--" && len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				return args[1], true
			}
			if safePluginInterpreterFlag(program, args[0]) {
				args = args[1:]
				continue
			}
			if !strings.HasPrefix(args[0], "-") {
				return args[0], true
			}
			return "", false
		}
		return "", false
	default:
		if filepath.IsAbs(command[0]) || strings.ContainsAny(command[0], `/\`) || strings.HasPrefix(command[0], ".") {
			return command[0], true
		}
		return "", false
	}
}

func safePluginInterpreterFlag(interpreter string, flag string) bool {
	return ((interpreter == "python" || interpreter == "python3") && flag == "-u") ||
		((interpreter == "bash" || interpreter == "sh" || interpreter == "zsh") && flag == "-e")
}

func pathEscapesRoot(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative)
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}
