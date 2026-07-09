package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const MaxFunctionKey = 24

type KeymapConfig struct {
	bindings map[string]map[string][]string
}

type KeymapEditOperation string

const (
	KeymapEditSet    KeymapEditOperation = "set"
	KeymapEditUnbind KeymapEditOperation = "unbind"
	KeymapEditUnset  KeymapEditOperation = "unset"
)

type KeymapEdit struct {
	Operation KeymapEditOperation
	Context   string
	Action    string
	Bindings  []string
}

type KeymapCommandResult struct {
	Text    string
	Config  *KeymapConfig
	Mutated bool
}

type KeymapEditApplier func(edit KeymapEdit) (*KeymapConfig, string, error)

func NewKeymapConfig() *KeymapConfig {
	return &KeymapConfig{bindings: map[string]map[string][]string{}}
}

func (c *KeymapConfig) Clone() *KeymapConfig {
	out := NewKeymapConfig()
	if c == nil {
		return out
	}
	for context, actions := range c.bindings {
		for action, bindings := range actions {
			out.setRaw(context, action, bindings)
		}
	}
	return out
}

func (c *KeymapConfig) Set(context string, action string, bindings []string) error {
	context, action, err := normalizeKeymapTarget(context, action)
	if err != nil {
		return err
	}
	normalized := make([]string, 0, len(bindings))
	seen := map[string]bool{}
	for _, binding := range bindings {
		value, err := NormalizeKeybindingSpec(binding)
		if err != nil {
			return err
		}
		if seen[value] {
			return fmt.Errorf("duplicate keybinding `%s` for %s.%s", value, context, action)
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	c.setRaw(context, action, normalized)
	return nil
}

func (c *KeymapConfig) Unset(context string, action string) error {
	context, action, err := normalizeKeymapTarget(context, action)
	if err != nil {
		return err
	}
	if c == nil || c.bindings == nil {
		return nil
	}
	actions := c.bindings[context]
	delete(actions, action)
	if len(actions) == 0 {
		delete(c.bindings, context)
	}
	return nil
}

func (c *KeymapConfig) Binding(context string, action string) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	actions := c.bindings[strings.TrimSpace(context)]
	if actions == nil {
		return nil, false
	}
	bindings, ok := actions[strings.TrimSpace(action)]
	if !ok {
		return nil, false
	}
	return append([]string(nil), bindings...), true
}

func (c *KeymapConfig) HasCustomBinding(context string, action string) bool {
	_, ok := c.Binding(context, action)
	return ok
}

func (c *KeymapConfig) setRaw(context string, action string, bindings []string) {
	if c.bindings == nil {
		c.bindings = map[string]map[string][]string{}
	}
	if c.bindings[context] == nil {
		c.bindings[context] = map[string][]string{}
	}
	c.bindings[context][action] = append([]string(nil), bindings...)
}

func KeymapConfigFromConfigValues(values map[string]any) (*KeymapConfig, error) {
	config := NewKeymapConfig()
	keymap, ok, err := keymapTableFromValues(values)
	if err != nil || !ok {
		return config, err
	}
	for context, rawContext := range keymap {
		context = strings.TrimSpace(context)
		if _, ok := knownKeymapConfigContexts()[context]; !ok {
			if _, actionOK := knownKeymapConfigActions("global")[context]; actionOK {
				return nil, fmt.Errorf("keymap action `%s` must be placed under a context table like [tui.keymap.global]", context)
			}
			return nil, fmt.Errorf("unknown keymap context `%s`", context)
		}
		actionValues, ok := rawContext.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tui.keymap.%s must be a table", context)
		}
		for action, rawBinding := range actionValues {
			if _, ok := knownKeymapConfigActions(context)[action]; !ok {
				return nil, fmt.Errorf("unknown keymap action `%s` in context `%s`", action, context)
			}
			bindings, err := parseKeybindingsConfigValue(rawBinding)
			if err != nil {
				return nil, fmt.Errorf("tui.keymap.%s.%s: %w", context, action, err)
			}
			config.setRaw(context, action, bindings)
		}
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

func (c *KeymapConfig) Validate() error {
	if c == nil {
		return nil
	}
	for context, actions := range c.bindings {
		seen := map[string]string{}
		for action, bindings := range actions {
			if _, ok := knownKeymapConfigActions(context)[action]; !ok {
				return fmt.Errorf("unknown keymap action `%s` in context `%s`", action, context)
			}
			for _, binding := range bindings {
				if previous := seen[binding]; previous != "" {
					return fmt.Errorf("keybinding `%s` is assigned to both %s.%s and %s.%s", binding, context, previous, context, action)
				}
				seen[binding] = action
			}
		}
	}
	return nil
}

func ResolvedKeymapBindings(config *KeymapConfig, context string, action string) ([]string, string, bool) {
	if config != nil {
		if bindings, ok := config.Binding(context, action); ok {
			return bindings, "custom", true
		}
		if context == "composer" {
			if bindings, ok := config.Binding("global", action); ok {
				return bindings, "custom global", true
			}
		}
	}
	if descriptor, ok := FindKeymapAction(context, action); ok {
		return append([]string(nil), descriptor.DefaultBindings...), "default", false
	}
	return nil, "unknown", false
}

func KeymapActionHasBinding(config *KeymapConfig, context string, action string, binding string) bool {
	normalized, err := NormalizeKeybindingSpec(binding)
	if err != nil {
		return false
	}
	bindings, _, _ := ResolvedKeymapBindings(config, context, action)
	for _, candidate := range bindings {
		if candidate == normalized {
			return true
		}
	}
	return false
}

func NormalizeKeybindingSpec(raw string) (string, error) {
	original := raw
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return "", fmt.Errorf("keybinding cannot be empty. Use values like `ctrl-a` or `shift-enter`")
	}
	segments := strings.Split(lower, "-")
	filtered := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment != "" {
			filtered = append(filtered, segment)
		}
	}
	if len(filtered) == 0 {
		return "", fmt.Errorf("invalid keybinding `%s`. Use values like `ctrl-a`, `shift-enter`, or `page-down`", original)
	}

	modifiers := map[string]bool{"ctrl": false, "alt": false, "shift": false}
	keySegments := []string{}
	sawKey := false
	for _, segment := range filtered {
		modifier := ""
		switch segment {
		case "ctrl", "control":
			modifier = "ctrl"
		case "alt", "option":
			modifier = "alt"
		case "shift":
			modifier = "shift"
		}
		if !sawKey && modifier != "" {
			if modifiers[modifier] {
				return "", fmt.Errorf("duplicate modifier in keybinding `%s`. Use each modifier at most once", original)
			}
			modifiers[modifier] = true
			continue
		}
		sawKey = true
		keySegments = append(keySegments, segment)
	}
	if len(keySegments) == 0 {
		return "", fmt.Errorf("missing key in keybinding `%s`. Add a key name like `a`, `enter`, or `page-down`", original)
	}
	for _, segment := range keySegments {
		switch segment {
		case "ctrl", "control", "alt", "option", "shift":
			return "", fmt.Errorf("invalid keybinding `%s`: modifiers must come before the key", original)
		}
	}
	key, err := normalizeKeyName(strings.Join(keySegments, "-"), original)
	if err != nil {
		return "", err
	}
	out := []string{}
	for _, modifier := range []string{"ctrl", "alt", "shift"} {
		if modifiers[modifier] {
			out = append(out, modifier)
		}
	}
	out = append(out, key)
	return strings.Join(out, "-"), nil
}

func HandleKeymapCommand(args string, config *KeymapConfig, apply KeymapEditApplier) (*KeymapCommandResult, error) {
	args = strings.TrimSpace(args)
	switch {
	case args == "", args == "list", args == "catalog":
		return &KeymapCommandResult{Text: strings.TrimSpace(RenderKeymapCatalogWithConfig(KeymapActionFilter{}, config)), Config: config.Clone()}, nil
	case args == "help":
		return &KeymapCommandResult{Text: strings.TrimSpace(RenderKeymapCommandHelp()), Config: config.Clone()}, nil
	}

	fields := strings.Fields(args)
	if len(fields) == 0 {
		return &KeymapCommandResult{Text: strings.TrimSpace(RenderKeymapCatalogWithConfig(KeymapActionFilter{}, config)), Config: config.Clone()}, nil
	}
	switch fields[0] {
	case "show":
		context, action, err := parseKeymapTargetFields(fields[1:])
		if err != nil {
			return nil, err
		}
		return &KeymapCommandResult{Text: strings.TrimSpace(RenderKeymapAction(context, action, config)), Config: config.Clone()}, nil
	case "set", "bind":
		if len(fields) < 3 {
			return nil, fmt.Errorf("usage: /keymap set <context.action> <key> [key...]")
		}
		context, action, err := parseKeymapTarget(fields[1])
		if err != nil {
			return nil, err
		}
		bindings, err := ParseKeybindingList(strings.Join(fields[2:], " "))
		if err != nil {
			return nil, err
		}
		edit := KeymapEdit{Operation: KeymapEditSet, Context: context, Action: action, Bindings: bindings}
		return applyKeymapEditCommand(edit, config, apply)
	case "unbind":
		context, action, err := parseKeymapTargetFields(fields[1:])
		if err != nil {
			return nil, err
		}
		edit := KeymapEdit{Operation: KeymapEditUnbind, Context: context, Action: action}
		return applyKeymapEditCommand(edit, config, apply)
	case "unset", "reset":
		context, action, err := parseKeymapTargetFields(fields[1:])
		if err != nil {
			return nil, err
		}
		edit := KeymapEdit{Operation: KeymapEditUnset, Context: context, Action: action}
		return applyKeymapEditCommand(edit, config, apply)
	default:
		return nil, fmt.Errorf("unknown /keymap command `%s`. Try `/keymap help`", fields[0])
	}
}

func RenderKeymapCommandHelp() string {
	return strings.Join([]string{
		"Codex TUI keymap commands:",
		"  /keymap                         show active key bindings",
		"  /keymap show CONTEXT.ACTION     show one action",
		"  /keymap set CONTEXT.ACTION KEY  set custom binding(s)",
		"  /keymap unbind CONTEXT.ACTION   explicitly unbind an action",
		"  /keymap unset CONTEXT.ACTION    remove custom binding and use defaults",
		"Examples:",
		"  /keymap set global.open_external_editor ctrl-e",
		"  /keymap set composer.submit ctrl-s alt-enter",
		"  /keymap unbind global.open_external_editor",
	}, "\n") + "\n"
}

func RenderKeymapAction(context string, action string, config *KeymapConfig) string {
	descriptor, ok := FindKeymapAction(context, action)
	if !ok {
		return ""
	}
	bindings, source, custom := ResolvedKeymapBindings(config, context, action)
	summary := "unbound"
	if len(bindings) > 0 {
		summary = strings.Join(bindings, ", ")
	}
	customLabel := "default"
	if custom {
		customLabel = source
	}
	return fmt.Sprintf("%s.%s\n  %s | %s | %s\n  %s\n", context, action, descriptor.Label, summary, customLabel, descriptor.Description)
}

func ParseKeybindingList(raw string) ([]string, error) {
	parts := splitKeybindingList(raw)
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		normalized, err := NormalizeKeybindingSpec(part)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			return nil, fmt.Errorf("duplicate keybinding `%s`", normalized)
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one keybinding is required")
	}
	return out, nil
}

func (e KeymapEdit) KeyPath() string {
	return "tui.keymap." + e.Context + "." + e.Action
}

func (e KeymapEdit) ConfigValue() any {
	if e.Operation == KeymapEditUnset {
		return nil
	}
	if len(e.Bindings) == 1 {
		return e.Bindings[0]
	}
	out := make([]string, len(e.Bindings))
	copy(out, e.Bindings)
	return out
}

func (e *KeymapEdit) Validate() error {
	if e == nil {
		return fmt.Errorf("keymap edit is nil")
	}
	context, action, err := normalizeKeymapTarget(e.Context, e.Action)
	if err != nil {
		return err
	}
	switch e.Operation {
	case KeymapEditSet:
		if len(e.Bindings) == 0 {
			return fmt.Errorf("set requires at least one keybinding")
		}
	case KeymapEditUnbind, KeymapEditUnset:
	default:
		return fmt.Errorf("unknown keymap edit operation `%s`", e.Operation)
	}
	e.Context = context
	e.Action = action
	return nil
}

func keymapTableFromValues(values map[string]any) (map[string]any, bool, error) {
	if values == nil {
		return nil, false, nil
	}
	tuiValue, ok := values["tui"]
	if !ok {
		return nil, false, nil
	}
	tuiTable, ok := tuiValue.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("tui must be a table")
	}
	keymapValue, ok := tuiTable["keymap"]
	if !ok {
		return nil, false, nil
	}
	keymapTable, ok := keymapValue.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("tui.keymap must be a table")
	}
	return keymapTable, true, nil
}

func parseKeybindingsConfigValue(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		normalized, err := NormalizeKeybindingSpec(typed)
		if err != nil {
			return nil, err
		}
		return []string{normalized}, nil
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			normalized, err := NormalizeKeybindingSpec(item)
			if err != nil {
				return nil, err
			}
			out = append(out, normalized)
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("keybinding arrays must contain only strings")
			}
			normalized, err := NormalizeKeybindingSpec(value)
			if err != nil {
				return nil, err
			}
			out = append(out, normalized)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("keybinding must be a string or array of strings")
	}
}

func applyKeymapEditCommand(edit KeymapEdit, config *KeymapConfig, apply KeymapEditApplier) (*KeymapCommandResult, error) {
	if err := edit.Validate(); err != nil {
		return nil, err
	}
	if apply != nil {
		next, message, err := apply(edit)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(message) == "" {
			message = keymapEditMessage(edit)
		}
		return &KeymapCommandResult{
			Text:    strings.TrimSpace(message + "\n\n" + RenderKeymapAction(edit.Context, edit.Action, next)),
			Config:  next.Clone(),
			Mutated: true,
		}, nil
	}

	next := config.Clone()
	if edit.Operation == KeymapEditUnset {
		if err := next.Unset(edit.Context, edit.Action); err != nil {
			return nil, err
		}
	} else {
		if err := next.Set(edit.Context, edit.Action, edit.Bindings); err != nil {
			return nil, err
		}
	}
	return &KeymapCommandResult{
		Text:    strings.TrimSpace(keymapEditMessage(edit) + "\n\n" + RenderKeymapAction(edit.Context, edit.Action, next)),
		Config:  next,
		Mutated: true,
	}, nil
}

func keymapEditMessage(edit KeymapEdit) string {
	switch edit.Operation {
	case KeymapEditUnbind:
		return "Unbound " + edit.Context + "." + edit.Action + "."
	case KeymapEditUnset:
		return "Reset " + edit.Context + "." + edit.Action + " to defaults."
	default:
		return "Updated " + edit.Context + "." + edit.Action + " to " + strings.Join(edit.Bindings, ", ") + "."
	}
}

func normalizeKeyName(key string, original string) (string, error) {
	switch key {
	case "escape":
		key = "esc"
	case "return":
		key = "enter"
	case "spacebar":
		key = "space"
	case "pgup", "pageup":
		key = "page-up"
	case "pgdn", "pagedown":
		key = "page-down"
	case "del":
		key = "delete"
	}
	if len(key) == 1 {
		ch := key[0]
		if ch >= 0x20 && ch <= 0x7e && ch != '-' {
			return key, nil
		}
	}
	switch key {
	case "enter", "tab", "backspace", "esc", "delete", "up", "down", "left", "right", "home", "end", "page-up", "page-down", "space", "minus", "insert":
		return key, nil
	}
	if number, ok := strings.CutPrefix(key, "f"); ok {
		parsed, err := strconv.Atoi(number)
		if err == nil && parsed >= 1 && parsed <= MaxFunctionKey {
			return key, nil
		}
	}
	return "", fmt.Errorf("unknown key `%s` in keybinding `%s`", key, original)
}

func splitKeybindingList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func parseKeymapTargetFields(fields []string) (string, string, error) {
	if len(fields) == 1 {
		return parseKeymapTarget(fields[0])
	}
	if len(fields) == 2 {
		return normalizeKeymapTarget(fields[0], fields[1])
	}
	return "", "", fmt.Errorf("usage: /keymap show <context.action>")
}

func parseKeymapTarget(value string) (string, string, error) {
	context, action, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok {
		return "", "", fmt.Errorf("keymap target must be CONTEXT.ACTION")
	}
	return normalizeKeymapTarget(context, action)
}

func normalizeKeymapTarget(context string, action string) (string, string, error) {
	context = strings.TrimSpace(context)
	action = strings.TrimSpace(action)
	if context == "" || action == "" {
		return "", "", fmt.Errorf("keymap target must include context and action")
	}
	if _, ok := knownKeymapConfigActions(context)[action]; !ok {
		if _, contextOK := knownKeymapConfigContexts()[context]; !contextOK {
			return "", "", fmt.Errorf("unknown keymap context `%s`", context)
		}
		return "", "", fmt.Errorf("unknown keymap action `%s` in context `%s`", action, context)
	}
	return context, action, nil
}

func knownKeymapConfigContexts() map[string]struct{} {
	out := map[string]struct{}{}
	for _, action := range keymapActionCatalog {
		out[action.Context] = struct{}{}
	}
	return out
}

func knownKeymapConfigActions(context string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, action := range keymapActionCatalog {
		if action.Context == context {
			out[action.Action] = struct{}{}
		}
	}
	if context == "global" {
		for _, action := range []string{"submit", "queue", "toggle_shortcuts"} {
			out[action] = struct{}{}
		}
	}
	return out
}

func sortedKeymapContexts() []string {
	contexts := make([]string, 0, len(knownKeymapConfigContexts()))
	for context := range knownKeymapConfigContexts() {
		contexts = append(contexts, context)
	}
	sort.Strings(contexts)
	return contexts
}
