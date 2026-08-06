package tea

import (
	"fmt"
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

const keymapCaptureViewID = "keymap-capture"

type keymapCaptureIntent string

const (
	keymapCaptureReplaceAll keymapCaptureIntent = "replace_all"
	keymapCaptureAdd        keymapCaptureIntent = "add_alternate"
	keymapCaptureReplaceOne keymapCaptureIntent = "replace_one"
)

type keymapCaptureState struct {
	Context      string
	Action       string
	Intent       keymapCaptureIntent
	OldKey       string
	ErrorMessage string
}

func (m *Model) applyKeymapCommand(args string) {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "":
		m.openKeymapPicker("", "")
		return
	case "debug":
		m.State.AddMessage(codextui.RoleSystem, strings.TrimSpace(codextui.RenderKeymapCatalogWithConfig(codextui.KeymapActionFilter{FastModeEnabled: m.featureSettings["fast_mode"]}, m.keymapConfig)))
		m.notice = "Keymap debug"
		return
	default:
		m.notice = "Usage: /keymap [debug]"
		m.addErrorHistoryMessage(m.notice)
		return
	}
}

func (m *Model) applyKeymapEdit(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
	next := m.keymapConfig.Clone()
	if edit.Operation == codextui.KeymapEditUnset {
		if err := next.Unset(edit.Context, edit.Action); err != nil {
			return nil, "", err
		}
	} else if err := next.Set(edit.Context, edit.Action, edit.Bindings); err != nil {
		return nil, "", err
	}
	if err := next.Validate(); err != nil {
		return nil, "", err
	}
	if m.onKeymapEdit != nil {
		return m.onKeymapEdit(edit)
	}
	return next, "", nil
}

func (m *Model) openKeymapPicker(selectedContext string, selectedAction string) {
	if m == nil {
		return
	}
	descriptors := codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: m.featureSettings["fast_mode"]})
	items := make([]chatwidget.KeymapActionItem, 0, len(descriptors))
	for _, descriptor := range descriptors {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, descriptor.Context, descriptor.Action)
		items = append(items, chatwidget.KeymapActionItem{
			Context:     descriptor.Context,
			Action:      descriptor.Action,
			Description: descriptor.Description,
			Bindings:    bindings,
		})
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewKeymapPickerView(chatwidget.KeymapPickerConfig{
		Items:           items,
		SelectedContext: selectedContext,
		SelectedAction:  selectedAction,
		FastModeEnabled: m.featureSettings["fast_mode"],
	}))
	m.notice = "Keymap"
}

func (m *Model) openKeymapActionMenu(context string, action string) {
	if m == nil {
		return
	}
	descriptor, ok := codextui.FindKeymapAction(context, action)
	if !ok {
		m.notice = fmt.Sprintf("Keymap: unknown action %s.%s", context, action)
		m.openKeymapPicker("", "")
		return
	}
	m.keymapSelectedContext = context
	m.keymapSelectedAction = action
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action)
	custom := m.keymapConfig.HasCustomBinding(context, action)
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewKeymapActionMenuView(chatwidget.KeymapActionItem{
		Context:          context,
		Action:           action,
		Description:      descriptor.Description,
		Bindings:         bindings,
		HasCustomBinding: &custom,
	}))
}

func (m *Model) openKeymapReplaceBindingMenu(context string, action string) {
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action)
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewKeymapReplaceBindingMenuView(chatwidget.KeymapActionItem{
		Context:  context,
		Action:   action,
		Bindings: bindings,
	}))
}

func isKeymapModalID(id string) bool {
	switch id {
	case chatwidget.KeymapPickerViewID, chatwidget.KeymapActionMenuViewID, chatwidget.KeymapReplaceBindingMenuViewID, keymapCaptureViewID:
		return true
	default:
		return false
	}
}

func (m *Model) applyKeymapModalOption(viewID string, optionID string) bubbletea.Cmd {
	switch viewID {
	case chatwidget.KeymapPickerViewID:
		if optionID == "debug" {
			m.State.AddMessage(codextui.RoleSystem, strings.TrimSpace(codextui.RenderKeymapCatalogWithConfig(codextui.KeymapActionFilter{FastModeEnabled: m.featureSettings["fast_mode"]}, m.keymapConfig)))
			m.notice = "Keymap debug"
			return nil
		}
		context, action, ok := strings.Cut(optionID, ":")
		if !ok {
			m.notice = "Keymap: invalid action selection"
			return nil
		}
		m.openKeymapActionMenu(context, action)
	case chatwidget.KeymapActionMenuViewID:
		context, action := m.keymapSelectedContext, m.keymapSelectedAction
		switch optionID {
		case "set":
			m.openKeymapCapture(context, action, keymapCaptureReplaceAll, "")
		case "add":
			m.openKeymapCapture(context, action, keymapCaptureAdd, "")
		case "replace_one":
			m.openKeymapReplaceBindingMenu(context, action)
		case "unset":
			return m.applyKeymapUnset(context, action)
		case "back":
			m.openKeymapPicker(context, action)
		}
	case chatwidget.KeymapReplaceBindingMenuViewID:
		m.openKeymapCapture(m.keymapSelectedContext, m.keymapSelectedAction, keymapCaptureReplaceOne, optionID)
	}
	return nil
}

func (m *Model) cancelKeymapModal(viewID string) bubbletea.Cmd {
	switch viewID {
	case chatwidget.KeymapActionMenuViewID:
		m.openKeymapPicker(m.keymapSelectedContext, m.keymapSelectedAction)
	case chatwidget.KeymapReplaceBindingMenuViewID, keymapCaptureViewID:
		m.openKeymapActionMenu(m.keymapSelectedContext, m.keymapSelectedAction)
	default:
		m.notice = "Cancelled"
	}
	return nil
}

func (m *Model) openKeymapCapture(context string, action string, intent keymapCaptureIntent, oldKey string) {
	m.keymapSelectedContext = context
	m.keymapSelectedAction = action
	m.modal = &modalState{
		id:    keymapCaptureViewID,
		kind:  ModalKindGeneric,
		title: "Remap Shortcut",
		keymapCapture: &keymapCaptureState{
			Context: context,
			Action:  action,
			Intent:  intent,
			OldKey:  oldKey,
		},
	}
	m.notice = ""
}

func (m *Model) updateKeymapCapture(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.keymapCapture == nil {
		return nil
	}
	if message.Type == bubbletea.KeyEsc || message.Type == bubbletea.KeyCtrlC {
		m.openKeymapActionMenu(m.modal.keymapCapture.Context, m.modal.keymapCapture.Action)
		return nil
	}
	key := keySpecFromKeyMsg(message)
	if key == "" {
		m.modal.keymapCapture.ErrorMessage = "That key is not supported by `tui.keymap`."
		return nil
	}
	capture := *m.modal.keymapCapture
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, capture.Context, capture.Action)
	nextBindings := []string{key}
	switch capture.Intent {
	case keymapCaptureAdd:
		for _, binding := range bindings {
			if binding == key {
				m.openKeymapPicker(capture.Context, capture.Action)
				m.notice = fmt.Sprintf("No change: `%s.%s` already uses `%s`.", capture.Context, capture.Action, key)
				return nil
			}
		}
		nextBindings = append(append([]string(nil), bindings...), key)
	case keymapCaptureReplaceOne:
		nextBindings = append([]string(nil), bindings...)
		replaced := false
		for index, binding := range nextBindings {
			if binding == capture.OldKey {
				nextBindings[index] = key
				replaced = true
			}
		}
		if !replaced {
			m.modal.keymapCapture.ErrorMessage = fmt.Sprintf("`%s.%s` no longer uses `%s`.", capture.Context, capture.Action, capture.OldKey)
			return nil
		}
	}
	next, messageText, err := m.applyKeymapEdit(codextui.KeymapEdit{Operation: codextui.KeymapEditSet, Context: capture.Context, Action: capture.Action, Bindings: nextBindings})
	if err != nil {
		m.modal.keymapCapture.ErrorMessage = err.Error()
		return nil
	}
	m.keymapConfig = next.Clone()
	if strings.TrimSpace(messageText) == "" {
		messageText = fmt.Sprintf("Remapped `%s.%s` to `%s`.", capture.Context, capture.Action, key)
	}
	m.openKeymapPicker(capture.Context, capture.Action)
	m.notice = messageText
	return nil
}

func (m *Model) applyKeymapUnset(context string, action string) bubbletea.Cmd {
	next, messageText, err := m.applyKeymapEdit(codextui.KeymapEdit{Operation: codextui.KeymapEditUnset, Context: context, Action: action})
	if err != nil {
		m.openKeymapActionMenu(context, action)
		m.notice = "Keymap: " + err.Error()
		return nil
	}
	m.keymapConfig = next.Clone()
	if strings.TrimSpace(messageText) == "" {
		messageText = fmt.Sprintf("Removed custom binding for `%s.%s`.", context, action)
	}
	m.openKeymapPicker(context, action)
	m.notice = messageText
	return nil
}

func (m *Model) renderKeymapCapture() string {
	if m == nil || m.modal == nil || m.modal.keymapCapture == nil {
		return ""
	}
	capture := m.modal.keymapCapture
	descriptor, _ := codextui.FindKeymapAction(capture.Context, capture.Action)
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, capture.Context, capture.Action)
	current := "unbound"
	if len(bindings) > 0 {
		current = strings.Join(bindings, ", ")
	}
	lines := []string{
		"Remap Shortcut",
		fmt.Sprintf("Action: %s  %s.%s", descriptor.Label, capture.Context, capture.Action),
		"Current: " + current,
		"Press the new key now. Esc cancels.",
	}
	if capture.ErrorMessage != "" {
		lines = append(lines, "", "Error: "+capture.ErrorMessage)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) keyMatches(context string, action string, keySpec string) bool {
	if keySpec == "" {
		return false
	}
	return codextui.KeymapActionHasBinding(m.keymapConfig, context, action, keySpec)
}

func keySpecFromKeyMsg(message bubbletea.KeyMsg) string {
	key := bubbletea.Key(message)
	// Ctrl+/ is commonly encoded as the C0 unit-separator byte (0x1f).
	// Bubble Tea exposes that byte as Ctrl+_, while Rust/crossterm normalizes
	// the same byte to Ctrl+7 for Ctrl+/ compatibility.
	if key.Type == bubbletea.KeyCtrlUnderscore && !key.Alt {
		return "ctrl-7"
	}
	// Windows conhost cannot encode Ctrl+/ as a C0 byte, so it delivers the
	// physical key as a KEY_EVENT_RECORD whose Char is NUL; bubbletea's
	// coninput reader drops the virtual-key code and surfaces that record as
	// KeyRunes with a single NUL rune. The ANSI byte path never produces this
	// shape (a NUL byte becomes keyNUL there), so it only occurs on the
	// Windows console path. Treat it as the Ctrl+/ alias that crossterm
	// resolves for the same physical key.
	if key.Type == bubbletea.KeyRunes && len(key.Runes) == 1 && key.Runes[0] == 0 && !key.Alt && !key.Paste {
		return "ctrl-7"
	}
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
		} else if r == '-' {
			parts = append(parts, "minus")
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
