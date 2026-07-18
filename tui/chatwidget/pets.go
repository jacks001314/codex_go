package chatwidget

import (
	"sort"
	"strings"

	petscore "codex_go/tui/pets"
)

const (
	PetsPickerViewID = "pets-picker"
	DisabledPetID    = petscore.DisabledPetID
	DefaultPetID     = petscore.DefaultPetID
)

const (
	PetsActionSelect  UsageMenuAction = "pets_select"
	PetsActionDisable UsageMenuAction = "pets_disable"
)

type PetImageSupportKind string

const (
	PetImageSupported PetImageSupportKind = "supported"
	PetImageTerminal  PetImageSupportKind = "terminal_unsupported"
	PetImageDisabled  PetImageSupportKind = "disabled"
)

type PetImageSupport struct {
	Kind    PetImageSupportKind
	Message string
}

type PetOption struct {
	ID          string
	Name        string
	Description string
	LegacyID    string
}

type PetPickerResult struct {
	View        SelectionView
	InfoMessage string
}

func NewPetsPickerView(currentPet string, support PetImageSupport, options []PetOption) PetPickerResult {
	if message := support.UnsupportedMessage(); message != "" {
		return PetPickerResult{InfoMessage: message}
	}
	currentConfigured := strings.TrimSpace(currentPet) != ""
	currentPet = normalizePetID(currentPet)
	preferredPet := currentPet
	if preferredPet == "" {
		preferredPet = DefaultPetID
	}
	items := []SelectionItem{{
		ID:              DisabledPetID,
		Name:            "Disable terminal pets",
		SearchValue:     "disable disabled hide hidden off none",
		Action:          PetsActionDisable,
		IsCurrent:       currentConfigured && currentPet == DisabledPetID,
		DismissOnSelect: true,
	}}
	options = append([]PetOption(nil), options...)
	sort.SliceStable(options, func(i, j int) bool {
		left := strings.TrimSpace(options[i].Name)
		if left == "" {
			left = options[i].ID
		}
		right := strings.TrimSpace(options[j].Name)
		if right == "" {
			right = options[j].ID
		}
		return strings.ToLower(left) < strings.ToLower(right)
	})
	seen := map[string]bool{DisabledPetID: true}
	for _, option := range options {
		id := normalizePetID(option.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(option.Name)
		if name == "" {
			name = id
		}
		items = append(items, SelectionItem{
			ID:              id,
			Name:            name,
			Description:     strings.TrimSpace(option.Description),
			SearchValue:     petSearchValue(id, option.LegacyID),
			Action:          PetsActionSelect,
			IsCurrent:       currentConfigured && (currentPet == id || normalizePetID(option.LegacyID) == currentPet),
			DismissOnSelect: true,
		})
	}
	initialSelected := 0
	for i, item := range items {
		if item.ID == preferredPet || normalizePetID(item.SearchValue) == preferredPet {
			initialSelected = i
			break
		}
	}
	return PetPickerResult{View: SelectionView{
		ViewID:               PetsPickerViewID,
		Title:                "Select Pet",
		Subtitle:             "Choose a pet to wake in the terminal.",
		FooterHint:           standardPopupHintLine,
		AllowCancel:          true,
		Items:                items,
		InitialSelectedIndex: initialSelected,
		Searchable:           true,
		SearchPlaceholder:    "Type to filter pets...",
	}}
}

func (s PetImageSupport) UnsupportedMessage() string {
	if strings.TrimSpace(s.Message) != "" {
		return strings.TrimSpace(s.Message)
	}
	switch s.Kind {
	case "", PetImageSupported:
		return ""
	case PetImageDisabled:
		return "Terminal pet images are disabled in this session."
	default:
		return "Pet previews require terminal image protocol support."
	}
}

func normalizePetID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return ""
	case "default":
		return DefaultPetID
	case "off", "none", "hide", "hidden", "disable", "disabled":
		return DisabledPetID
	default:
		return value
	}
}

func BuiltinPetOptions() []PetOption {
	catalog := petscore.BuiltinCatalog()
	out := make([]PetOption, 0, len(catalog))
	for _, pet := range catalog {
		option := PetOption{ID: pet.ID, Name: pet.DisplayName, Description: pet.Description}
		if pet.ID == DefaultPetID {
			option.LegacyID = "default"
		}
		out = append(out, option)
	}
	return out
}

func petSearchValue(id string, legacyID string) string {
	parts := []string{id}
	if strings.TrimSpace(legacyID) != "" {
		parts = append(parts, strings.TrimSpace(legacyID))
	}
	return strings.Join(parts, " ")
}
