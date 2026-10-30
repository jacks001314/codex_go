package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust #54378 (upstream 0884c8047a, "Support inline model, reasoning, and speed
// selection in `/model`"): `/model [model] --reasoning <level> --speed <tier>`
// updates the current session without touching saved defaults, while a bare
// `/model` still opens the picker. The argument grammar is
// codex-rs/tui/src/chatwidget/model_command.rs (parse_model_command_args /
// resolve_model_command) and the session-only application plus its confirmation
// message are codex-rs/tui/src/app/model_defaults.rs:19-119
// (select_session_model) with the reasoning/speed labels of
// app/config_persistence.rs:1038-1050.
//
// Go keeps the session model, reasoning level, and speed in its own State (the
// model is sent with each turn), so the command applies all three locally. The
// server-side settings sync (`thread/settings/update`), the per-thread speed
// restore, and the destination-defaults rule for new sessions are app-side: see
// the lane contract handed to the app lane.

// modelCommandArgs is the parsed `/model` argument string (Rust
// ModelCommandArgs).
type modelCommandArgs struct {
	model     string
	effort    string
	speed     string
	hasModel  bool
	hasEffort bool
	hasSpeed  bool
}

// parseModelCommandArgs mirrors parse_model_command_args
// (chatwidget/model_command.rs:23-35, post-#54385): the shared slash-command
// parser tokenizes the arguments (value settings `--reasoning`/`--speed`, no
// switches or boolean settings, quoted values allowed) and only the first
// positional is the model. The returned string is the Rust error message, empty
// on success.
func parseModelCommandArgs(args string) (modelCommandArgs, string) {
	command, failure := parseSlashCommandArgs(args, []string{"--reasoning", "--speed"}, nil, nil)
	if failure != "" {
		return modelCommandArgs{}, failure
	}
	if len(command.positionals) > 1 {
		return modelCommandArgs{}, "Unexpected argument '" + command.positionals[1] + "'. Use '--reasoning=<level>' or '--speed=<tier>'."
	}
	var parsed modelCommandArgs
	if len(command.positionals) == 1 {
		parsed.model, parsed.hasModel = command.positionals[0], true
	}
	if effort, ok := command.value("--reasoning"); ok {
		parsed.effort, parsed.hasEffort = effort, true
	}
	if speed, ok := command.value("--speed"); ok {
		parsed.speed, parsed.hasSpeed = speed, true
	}
	return parsed, ""
}

// modelCommandSelection is the validated command (Rust ModelCommand): the
// resolved model, its reasoning level, and -- only when the user asked for one --
// the service tier request value.
type modelCommandSelection struct {
	model       string
	effort      string
	serviceTier string
	hasTier     bool
}

// modelCommandCatalog returns the hidden-inclusive catalog the way Rust's
// resolve_model_command reads `chat.model_catalog.models`.
func (m *Model) modelCommandCatalog() []codextui.ModelPickerOption {
	if len(m.modelCatalogOpts) > 0 {
		return m.modelCatalogOpts
	}
	return m.modelPickerOpts
}

// modelCommandCatalogOption finds the target model. An explicitly named model
// must be picker-visible (Rust `args.model.is_none() || model.show_in_picker`),
// so an explicitly named hidden model stays unavailable, while the current
// hidden model still accepts settings-only updates.
func (m *Model) modelCommandCatalogOption(name string, explicit bool) (codextui.ModelPickerOption, bool) {
	for _, option := range m.modelCommandCatalog() {
		if option.ID != name {
			continue
		}
		if explicit && !option.ShowInPicker {
			continue
		}
		return option, true
	}
	return codextui.ModelPickerOption{}, false
}

// modelArgumentsRestrictedToReserve mirrors
// ChatWidget::restrict_model_picker_to_luna_reserve (chatwidget/
// backend_banners.rs): while the session runs on Reserve without a recovered
// ordinary-usage read, `/model` arguments are unavailable.
func (m *Model) modelArgumentsRestrictedToReserve() bool {
	if m == nil {
		return false
	}
	return m.currentBannerModel() == LunaReserveModel && !m.backendBanner.ordinaryUsageRecovered
}

// resolveModelCommandArgs mirrors resolve_model_command
// (chatwidget/model_command.rs:139-181).
func (m *Model) resolveModelCommandArgs(args string) (modelCommandSelection, string) {
	parsed, failure := parseModelCommandArgs(args)
	if failure != "" {
		return modelCommandSelection{}, failure
	}
	if m == nil || m.State == nil {
		return modelCommandSelection{}, "Session is still starting; try /model again in a moment."
	}
	if strings.TrimSpace(m.currentThreadID()) == "" {
		return modelCommandSelection{}, "Session is still starting; try /model again in a moment."
	}
	if m.modelArgumentsRestrictedToReserve() {
		return modelCommandSelection{}, "Model arguments are unavailable while using Reserve. Use /model to adjust effort."
	}
	modelName := parsed.model
	if !parsed.hasModel {
		modelName = strings.TrimSpace(m.State.Model)
	}
	option, ok := m.modelCommandCatalogOption(modelName, parsed.hasModel)
	if !ok {
		return modelCommandSelection{}, "Model '" + modelName + "' is unavailable. Use /model to see available models."
	}
	effort, failure := resolveModelCommandEffort(parsed, option, m.State.EffectiveReasoningEffort())
	if failure != "" {
		return modelCommandSelection{}, failure
	}
	tier, hasTier, failure := resolveModelCommandSpeed(parsed, option, m.featureSettings)
	if failure != "" {
		return modelCommandSelection{}, failure
	}
	return modelCommandSelection{model: option.ID, effort: effort, serviceTier: tier, hasTier: hasTier}, ""
}

// resolveModelCommandEffort mirrors resolve_reasoning_effort
// (chatwidget/model_command.rs:81-124). currentEffort is Rust's
// chat.current_reasoning_effort(), which only applies when the model itself was
// not named.
func resolveModelCommandEffort(parsed modelCommandArgs, option codextui.ModelPickerOption, currentEffort string) (string, string) {
	if !parsed.hasEffort {
		if !parsed.hasModel {
			if effort := strings.TrimSpace(currentEffort); effort != "" {
				return effort, ""
			}
		}
		return option.DefaultReasoningEffort, ""
	}
	if len(option.SupportedReasoningEfforts) == 0 {
		if option.DefaultReasoningEffort == parsed.effort {
			return option.DefaultReasoningEffort, ""
		}
		return "", "Reasoning level '" + parsed.effort + "' is unavailable for " + option.ID +
			". Available: " + option.DefaultReasoningEffort + "."
	}
	for _, candidate := range option.SupportedReasoningEfforts {
		if candidate.Effort == parsed.effort {
			return candidate.Effort, ""
		}
	}
	available := make([]string, 0, len(option.SupportedReasoningEfforts))
	for _, candidate := range option.SupportedReasoningEfforts {
		available = append(available, candidate.Effort)
	}
	return "", "Reasoning level '" + parsed.effort + "' is unavailable for " + option.ID +
		". Available: " + strings.Join(available, ", ") + "."
}

// resolveModelCommandSpeed mirrors resolve_service_tier
// (chatwidget/model_command.rs:126-166): `standard` is the default request value,
// `flex` is always available, and every other speed matches a catalog tier by
// request value or by display name, gated by the service-tier policy. Go's tier
// display names come from bottom_pane.ServiceTierCommandsFromIDs, the same
// reduction the live service-tier commands use.
func resolveModelCommandSpeed(parsed modelCommandArgs, option codextui.ModelPickerOption, featureSettings map[string]bool) (string, bool, string) {
	if !parsed.hasSpeed {
		return "", false, ""
	}
	speed := parsed.speed
	switch speed {
	case "standard":
		return codextui.ServiceTierDefaultRequestValue, true, ""
	case codextui.ServiceTierFlexRequestValue:
		return codextui.ServiceTierFlexRequestValue, true, ""
	}
	commands := bottompane.ServiceTierCommandsFromIDs(option.ServiceTiers)
	for _, command := range commands {
		if command.ID != speed && !strings.EqualFold(strings.TrimSpace(command.Name), speed) {
			continue
		}
		if !codextui.ServiceTierEnabled(featureSettings, command.ID) {
			continue
		}
		return command.ID, true, ""
	}
	available := []string{"standard", codextui.ServiceTierFlexRequestValue}
	for _, command := range commands {
		if codextui.ServiceTierEnabled(featureSettings, command.ID) {
			available = append(available, command.ID)
		}
	}
	return "", false, "Speed '" + speed + "' is unavailable for " + option.ID +
		". Available: " + strings.Join(available, ", ") + "."
}

// modelCommandConfirmationMessage mirrors the info message
// select_session_model builds (app/model_defaults.rs:102-118).
func modelCommandConfirmationMessage(selection modelCommandSelection) string {
	message := "Model changed to " + selection.model
	if label, ok := modelCommandReasoningLabel(selection.model, selection.effort); ok {
		message += " " + label
	}
	if selection.hasTier {
		message += " " + modelCommandSpeedLabel(selection.serviceTier)
	}
	return message + " for this session only"
}

// modelCommandReasoningLabel mirrors App::reasoning_label_for / reasoning_label
// (app/config_persistence.rs:1038-1050): auto models carry no label and an unset
// effort reads "default".
func modelCommandReasoningLabel(model string, effort string) (string, bool) {
	if strings.HasPrefix(model, "codex-auto-") {
		return "", false
	}
	if strings.TrimSpace(effort) == "" {
		return "default", true
	}
	return effort, true
}

// modelCommandSpeedLabel mirrors the speed label of the confirmation message.
func modelCommandSpeedLabel(serviceTier string) string {
	switch serviceTier {
	case codextui.ServiceTierDefaultRequestValue:
		return "standard"
	case codextui.ServiceTierFastRequestValue:
		return "fast"
	default:
		return serviceTier
	}
}

// applyModelCommandArgs validates and then applies a `/model` argument string to
// the session (Rust handle_model_command_args plus the app's session-only
// application). A rejected command reports the Rust error message as a notice and
// changes nothing.
func (m *Model) applyModelCommandArgs(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	selection, failure := m.resolveModelCommandArgs(args)
	if failure != "" {
		m.notice = failure
		m.refreshTranscript()
		return nil
	}
	modelChanged := strings.TrimSpace(m.State.Model) != selection.model
	if modelChanged {
		m.State.Model = selection.model
	}
	if m.State.PlanMode {
		m.State.PlanModeReasoningEffort = selection.effort
	} else {
		m.State.ReasoningEffort = selection.effort
	}
	if selection.hasTier {
		m.State.ServiceTier = selection.serviceTier
		m.savedServiceTier = selection.serviceTier
	}
	m.refreshServiceTierCommands()
	if modelChanged {
		// A manual model change releases any Reserve-switch hold.
		m.clearRateLimitRecoveryHold()
	}
	m.notice = modelCommandConfirmationMessage(selection)
	m.refreshTranscript()
	return nil
}
