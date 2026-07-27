package chatwidget

import (
	"strings"

	bottompane "codex_go/tui/bottom_pane"
)

const (
	KeymapPickerViewID             = "keymap-picker"
	KeymapActionMenuViewID         = "keymap-action-menu"
	KeymapReplaceBindingMenuViewID = "keymap-replace-binding-menu"
)

const keymapActionMenuMinDescriptionWidth = 24

const (
	KeymapActionOpenActionMenu UsageMenuAction = "keymap_open_action_menu"
	KeymapActionSetBinding     UsageMenuAction = "keymap_set_binding"
	KeymapActionAddBinding     UsageMenuAction = "keymap_add_binding"
	KeymapActionReplaceBinding UsageMenuAction = "keymap_replace_binding"
	KeymapActionUnsetBinding   UsageMenuAction = "keymap_unset_binding"
	KeymapActionDebug          UsageMenuAction = "keymap_debug"
)

type KeymapActionItem struct {
	Context          string
	Action           string
	Description      string
	Bindings         []string
	HasCustomBinding *bool
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
	initialSelectedIndex := 0
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
		if item.Context == config.SelectedContext && item.Action == config.SelectedAction {
			initialSelectedIndex = len(items) - 1
		}
	}
	items = append(items, SelectionItem{
		ID:              "debug",
		Name:            "Debug keypresses",
		Description:     "Inspect active keymap bindings.",
		Action:          KeymapActionDebug,
		DismissOnSelect: true,
	})
	return SelectionView{
		ViewID:               KeymapPickerViewID,
		Title:                "Keymap",
		FooterHint:           standardPopupHintLine,
		AllowCancel:          true,
		Searchable:           true,
		SearchPlaceholder:    "Type to search actions",
		Items:                items,
		InitialSelectedIndex: initialSelectedIndex,
	}
}

func NewKeymapActionMenuView(item KeymapActionItem) SelectionView {
	name := strings.TrimSpace(item.Action)
	hasCustomBinding := len(item.Bindings) > 0
	if item.HasCustomBinding != nil {
		hasCustomBinding = *item.HasCustomBinding
	}
	items := make([]SelectionItem, 0, 6)
	currentBinding := strings.Join(item.Bindings, ", ")
	switch len(item.Bindings) {
	case 0:
		items = append(items, SelectionItem{ID: "set", Name: "Set key", Description: "Capture a key for this unbound action.", SelectedDescription: "Capture one key and bind this action.", Action: KeymapActionSetBinding, DismissOnSelect: true})
	case 1:
		items = append(items,
			SelectionItem{ID: "set", Name: "Replace binding", Description: "Capture a replacement key.", SelectedDescription: "Capture one key and replace `" + currentBinding + "`.", Action: KeymapActionSetBinding, DismissOnSelect: true},
			SelectionItem{ID: "add", Name: "Add alternate binding", Description: "Keep the current binding and add another key.", SelectedDescription: "Capture one key and keep `" + currentBinding + "` as an alternate.", Action: KeymapActionAddBinding, DismissOnSelect: true},
		)
	default:
		items = append(items,
			SelectionItem{ID: "replace_one", Name: "Replace one binding...", Description: "Choose which existing binding to replace.", SelectedDescription: "Pick one current binding, then capture its replacement.", Action: KeymapActionReplaceBinding},
			SelectionItem{ID: "set", Name: "Replace all bindings", Description: "Replace every current binding with one key.", SelectedDescription: "Capture one key and replace `" + currentBinding + "`.", Action: KeymapActionSetBinding, DismissOnSelect: true},
			SelectionItem{ID: "add", Name: "Add alternate binding", Description: "Keep current bindings and add another key.", SelectedDescription: "Capture one key and keep `" + currentBinding + "`.", Action: KeymapActionAddBinding, DismissOnSelect: true},
		)
	}
	removeReason := ""
	if !hasCustomBinding {
		removeReason = "No custom root override to remove."
	}
	items = append(items,
		SelectionItem{ID: "unset", Name: "Remove custom binding", Description: func() string {
			if hasCustomBinding {
				return "Restore the default keymap binding."
			}
			return ""
		}(), Disabled: !hasCustomBinding, DisabledReason: removeReason, DisabledGutterMarker: "–", Action: KeymapActionUnsetBinding, DismissOnSelect: true},
		SelectionItem{ID: "back", Name: "Back to shortcuts", Description: "Return to the shortcut list.", DismissOnSelect: true},
	)
	return SelectionView{
		ViewID:            KeymapActionMenuViewID,
		Title:             "Edit " + name,
		Subtitle:          strings.Join(item.Bindings, ", "),
		FooterNote:        "Changes write the root `tui.keymap.*` override.",
		FooterHint:        standardPopupHintLine,
		AllowCancel:       true,
		ColumnWidth:       bottompane.NewColumnWidthConfig(bottompane.ColumnWidthAutoAllRows, nil),
		DescriptionLayout: bottompane.NewStackBelowWhenNarrowDescriptionLayout(keymapActionMenuMinDescriptionWidth),
		Items:             items,
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
