package tea

import (
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func (m *Model) applyKeymapCommand(args string) {
	result, err := codextui.HandleKeymapCommand(args, m.keymapConfig, func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
		if m.onKeymapEdit != nil {
			return m.onKeymapEdit(edit)
		}
		next := m.keymapConfig.Clone()
		if edit.Operation == codextui.KeymapEditUnset {
			if err := next.Unset(edit.Context, edit.Action); err != nil {
				return nil, "", err
			}
		} else if err := next.Set(edit.Context, edit.Action, edit.Bindings); err != nil {
			return nil, "", err
		}
		return next, "", nil
	})
	if err != nil {
		m.notice = "Keymap: " + err.Error()
		return
	}
	m.keymapConfig = result.Config.Clone()
	m.State.AddMessage(codextui.RoleSystem, result.Text)
	m.notice = ""
}

func (m *Model) keyMatches(context string, action string, keySpec string) bool {
	if keySpec == "" {
		return false
	}
	return codextui.KeymapActionHasBinding(m.keymapConfig, context, action, keySpec)
}

func keySpecFromKeyMsg(message bubbletea.KeyMsg) string {
	key := bubbletea.Key(message)
	if key.Type == bubbletea.KeyRunes {
		if len(key.Runes) != 1 || key.Paste {
			return ""
		}
		r := key.Runes[0]
		parts := []string{}
		if key.Alt {
			parts = append(parts, "alt")
		}
		if unicode.IsUpper(r) {
			parts = append(parts, "shift")
			r = unicode.ToLower(r)
		}
		if r == ' ' {
			parts = append(parts, "space")
		} else {
			parts = append(parts, string(r))
		}
		normalized, err := codextui.NormalizeKeybindingSpec(strings.Join(parts, "-"))
		if err != nil {
			return ""
		}
		return normalized
	}
	raw := strings.ReplaceAll(message.String(), "+", "-")
	switch raw {
	case "pgup":
		raw = "page-up"
	case "pgdown":
		raw = "page-down"
	case " ":
		raw = "space"
	}
	normalized, err := codextui.NormalizeKeybindingSpec(raw)
	if err != nil {
		return ""
	}
	return normalized
}
