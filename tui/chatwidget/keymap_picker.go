package chatwidget

import "strings"

const (
	KeymapPickerViewID             = "keymap-picker"
	KeymapActionMenuViewID         = "keymap-action-menu"
	KeymapReplaceBindingMenuViewID = "keymap-replace-binding-menu"
)

const (
	KeymapActionOpenActionMenu UsageMenuAction = "keymap_open_action_menu"
	KeymapActionSetBinding     UsageMenuAction = "keymap_set_binding"
	KeymapActionAddBinding     UsageMenuAction = "keymap_add_binding"
	KeymapActionReplaceBinding UsageMenuAction = "keymap_replace_binding"
	KeymapActionUnsetBinding   UsageMenuAction = "keymap_unset_binding"
	KeymapActionDebug          UsageMenuAction = "keymap_debug"
)

type KeymapActionItem struct {
	Context     string
	Action      string
	Description string
	Bindings    []string
}

type KeymapPickerConfig struct {
	Items           []KeymapActionItem
	SelectedContext string
	SelectedAction  string
	FastModeEnabled bool
}

type KeymapRuntimeBindings struct {
	AppCopyLastResponse   string
	ChatEditQueuedMessage string
}

type KeymapApplyUpdateResult struct {
	CopyLastResponseBinding   string
	ChatEditQueuedBinding     string
	BottomPaneBindingsUpdated bool
	RequestRedraw             bool
}

func NewKeymapPickerView(config KeymapPickerConfig) SelectionView {
	items := make([]SelectionItem, 0, len(config.Items)+1)
	for _, item := range config.Items {
		id := strings.TrimSpace(item.Context) + ":" + strings.TrimSpace(item.Action)
		if strings.TrimSpace(id) == ":" {
			continue
		}
		description := strings.TrimSpace(item.Description)
		if len(item.Bindings) > 0 {
			if description != "" {
				description += " "
			}
			description += "(" + strings.Join(item.Bindings, ", ") + ")"
		}
		items = append(items, SelectionItem{
			ID:          id,
			Name:        strings.TrimSpace(item.Action),
			Description: description,
			SearchValue: strings.TrimSpace(item.Context) + " " + strings.TrimSpace(item.Action) + " " + strings.Join(item.Bindings, " "),
			Action:      KeymapActionOpenActionMenu,
			IsCurrent:   item.Context == config.SelectedContext && item.Action == config.SelectedAction,
		})
	}
	items = append(items, SelectionItem{
		ID:              "debug",
		Name:            "Debug keypresses",
		Description:     "Inspect active keymap bindings.",
		Action:          KeymapActionDebug,
		DismissOnSelect: true,
	})
	return SelectionView{
		ViewID:            KeymapPickerViewID,
		Title:             "Keymap",
		FooterHint:        standardPopupHintLine,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search actions",
		Items:             items,
	}
}

func NewKeymapActionMenuView(item KeymapActionItem) SelectionView {
	name := strings.TrimSpace(item.Action)
	return SelectionView{
		ViewID:      KeymapActionMenuViewID,
		Title:       "Edit " + name,
		Subtitle:    strings.Join(item.Bindings, ", "),
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{ID: "set", Name: "Set binding", Action: KeymapActionSetBinding, DismissOnSelect: true},
			{ID: "add", Name: "Add alternate binding", Action: KeymapActionAddBinding, DismissOnSelect: true},
			{ID: "unset", Name: "Unset binding", Action: KeymapActionUnsetBinding, Disabled: len(item.Bindings) == 0, DismissOnSelect: true},
		},
	}
}

func NewKeymapReplaceBindingMenuView(item KeymapActionItem) SelectionView {
	items := make([]SelectionItem, 0, len(item.Bindings))
	for _, binding := range item.Bindings {
		binding = strings.TrimSpace(binding)
		if binding == "" {
			continue
		}
		items = append(items, SelectionItem{
			ID:              binding,
			Name:            binding,
			Description:     "Replace this binding",
			Action:          KeymapActionReplaceBinding,
			DismissOnSelect: true,
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No bindings configured", Disabled: true})
	}
	return SelectionView{
		ViewID:      KeymapReplaceBindingMenuViewID,
		Title:       "Replace " + strings.TrimSpace(item.Action),
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items:       items,
	}
}

func ApplyKeymapRuntimeUpdate(bindings KeymapRuntimeBindings) KeymapApplyUpdateResult {
	return KeymapApplyUpdateResult{
		CopyLastResponseBinding:   strings.TrimSpace(bindings.AppCopyLastResponse),
		ChatEditQueuedBinding:     strings.TrimSpace(bindings.ChatEditQueuedMessage),
		BottomPaneBindingsUpdated: true,
		RequestRedraw:             true,
	}
}
