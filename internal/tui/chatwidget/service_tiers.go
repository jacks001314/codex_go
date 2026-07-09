package chatwidget

import "strings"

const ServiceTierDefaultRequestValue = "default"
const ServiceTierFastRequestValue = "priority"

type ServiceTierCommand struct {
	ID          string
	Name        string
	Description string
}

type ServiceTierState struct {
	ConfiguredServiceTier string
	EffectiveServiceTier  string
	HasChatGPTAccount     bool
	FastModeEnabled       bool
	CurrentModel          string
	ModelServiceTiers     []ServiceTierCommand
	UserTurnActive        bool
	ModalActive           bool
}

func (s ServiceTierState) CurrentServiceTier() string {
	return s.EffectiveServiceTier
}

func (s ServiceTierState) ConfiguredTier() string {
	return s.ConfiguredServiceTier
}

func (s ServiceTierState) ShouldShowFastStatus() bool {
	return s.HasChatGPTAccount &&
		s.CurrentServiceTier() == ServiceTierFastRequestValue &&
		s.ModelSupportsServiceTier(ServiceTierFastRequestValue)
}

func (s ServiceTierState) CanToggleFastModeFromKeybinding() bool {
	return s.FastModeEnabled && s.FastServiceTier() != nil && !s.UserTurnActive && !s.ModalActive
}

func (s ServiceTierState) NextServiceTierForToggle(command ServiceTierCommand) string {
	id := command.ID
	if s.EffectiveServiceTier == id {
		return ServiceTierDefaultRequestValue
	}
	return id
}

func (s ServiceTierState) FastServiceTier() *ServiceTierCommand {
	for _, tier := range s.ModelServiceTiers {
		if strings.EqualFold(tier.Name, "fast") {
			copy := tier
			return &copy
		}
	}
	return nil
}

func (s ServiceTierState) ModelSupportsServiceTier(id string) bool {
	for _, tier := range s.ModelServiceTiers {
		if tier.ID == id {
			return true
		}
	}
	return false
}
