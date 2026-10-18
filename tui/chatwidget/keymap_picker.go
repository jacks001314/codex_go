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
	KeymapActionSetChord       UsageMenuAction = "keymap_set_chord"
	KeymapActionAddChord       UsageMenuAction = "keymap_add_chord"
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
	// LeaderAvailable reports whether a leader prefix is active, which decides
	// whether the Use-leader shortcut is offered (Rust #52273, keymap_setup.rs:252-266).
	LeaderAvailable bool
	// IsLeaderAction marks the 1global.leader row, which never offers the
	// leader-chord entries itself.
	IsLeaderAction bool
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

// leaderActionMenuItems mirrors the leader entries Rust adds to every action
// menu except `global.leader` (keymap_setup.rs:252-278): one entry to capture a
// key that follows the configured leader, and — when no leader is active — one
// entry that navigates to the leader action so it can be configured first.
func leaderActionMenuItems(item KeymapActionItem) []SelectionItem {
	if item.IsLeaderAction {
		return nil
	}
	disabledReason := ""
	if !item.LeaderAvailable {
		disabledReason = "The leader is unbound"
	}
	items := []SelectionItem{{
		ID:                   "leader_set",
		Name:                 "Use leader...",
		Description:          "Capture the key that follows the configured leader.",
		Disabled:             !item.LeaderAvailable,
		DisabledReason:       disabledReason,
		DisabledGutterMarker: "–",
		Action:               KeymapActionSetBinding,
		DismissOnSelect:      true,
	}}
	if !item.LeaderAvailable {
		items = append(items, SelectionItem{
			ID:          "leader_config",
			Name:        "Configure leader...",
			Description: "Set a leader before assigning this shortcut.",
		})
	}
	return items
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
		ColumnWidth:          bottompane.NewColumnWidthConfig(bottompane.ColumnWidthAutoAllRows, nil).WithFullWidthSelection(),
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
		items = append(items,
			SelectionItem{ID: "set", Name: "Set key", Description: "Capture a key for this unbound action.", Action: KeymapActionSetBinding, DismissOnSelect: true},
			SelectionItem{ID: "set_chord", Name: "Set key chord", Description: "Capture two consecutive keys for this action", Action: KeymapActionSetChord, DismissOnSelect: true},
		)
	case 1:
		items = append(items,
			SelectionItem{ID: "set", Name: "Replace binding", Description: "Capture a replacement key for `" + currentBinding + "`.", Action: KeymapActionSetBinding, DismissOnSelect: true},
			SelectionItem{ID: "add", Name: "Add alternate binding", Description: "Keep `" + currentBinding + "` and add another key.", Action: KeymapActionAddBinding, DismissOnSelect: true},
			SelectionItem{ID: "set_chord", Name: "Replace with key chord", Description: "Replace `" + currentBinding + "` with a two-stroke key chord", Action: KeymapActionSetChord, DismissOnSelect: true},
			SelectionItem{ID: "add_chord", Name: "Add alternate key chord", Description: "Keep `" + currentBinding + "` and add a two-stroke key chord", Action: KeymapActionAddChord, DismissOnSelect: true},
		)
	default:
		items = append(items,
			SelectionItem{ID: "replace_one", Name: "Replace one binding...", Description: "Choose which existing binding to replace.", Action: KeymapActionReplaceBinding},
			SelectionItem{ID: "set", Name: "Replace all bindings", Description: "Replace `" + currentBinding + "` with one key.", Action: KeymapActionSetBinding, DismissOnSelect: true},
			SelectionItem{ID: "add", Name: "Add alternate binding", Description: "Keep `" + currentBinding + "` and add another key.", Action: KeymapActionAddBinding, DismissOnSelect: true},
			SelectionItem{ID: "set_chord", Name: "Replace with key chord", Description: "Replace `" + currentBinding + "` with a two-stroke key chord", Action: KeymapActionSetChord, DismissOnSelect: true},
			SelectionItem{ID: "add_chord", Name: "Add alternate key chord", Description: "Keep `" + currentBinding + "` and add a two-stroke key chord", Action: KeymapActionAddChord, DismissOnSelect: true},
		)
	}
	items = append(items, leaderActionMenuItems(item)...)
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
		ViewID:      KeymapActionMenuViewID,
		Title:       "Edit " + name,
		Subtitle:    strings.Join(item.Bindings, ", "),
		FooterNote:  "Changes write the root `tui.keymap.*` override.",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		ColumnWidth: bottompane.NewColumnWidthConfig(bottompane.ColumnWidthAutoAllRows, nil).WithFullWidthSelection(),
		// Rust's picker preset hides the description column below this width
		// (#46691 replaced stacking with hiding).
		DescriptionLayout: bottompane.NewHideWhenNarrowDescriptionLayout(keymapActionMenuMinDescriptionWidth),
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
			Description:     "Replace `" + binding + "` with another key.",
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
		ColumnWidth: bottompane.NewColumnWidthConfig(bottompane.ColumnWidthAutoAllRows, nil).WithFullWidthSelection(),
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
