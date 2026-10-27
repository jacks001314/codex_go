package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/auth"
)

type bedrockView int

const (
	bedrockViewDiscovering bedrockView = iota
	bedrockViewMethodsDetected
	bedrockViewMethodsAll
	bedrockViewProfileEntry
	bedrockViewAccessKeyEntry
	bedrockViewAPIKeyEntry
	bedrockViewRegionEntry
	bedrockViewEnvironmentInstructions
	bedrockViewConfiguring
	bedrockViewGovCloudWarning
)

type bedrockCredentialKind int

const (
	bedrockCredentialProfile bedrockCredentialKind = iota
	bedrockCredentialEnvironment
	bedrockCredentialAccessKeys
	bedrockCredentialAPIKey
)

type bedrockCredential struct {
	kind            bedrockCredentialKind
	profile         string
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	apiKey          string
}

type bedrockMethodKind int

const (
	bedrockMethodProfile bedrockMethodKind = iota
	bedrockMethodEnvironment
	bedrockMethodOther
	bedrockMethodManualProfile
	bedrockMethodAccessKeys
	bedrockMethodEnvironmentInstructions
	bedrockMethodAPIKey
)

type bedrockMethod struct {
	kind         bedrockMethodKind
	profileIndex int
}

type bedrockState struct {
	view                   bedrockView
	highlighted            int
	profiles               []appserver.BedrockAwsProfile
	environmentCredentials []appserver.BedrockEnvironmentCredential
	profileInput           string
	apiKeyInput            string
	regionInput            string
	accessKeys             [3]string
	selectedField          int
	credential             bedrockCredential
	govCloudScroll         int
}

type bedrockDiscoveredMsg struct {
	response *appserver.BedrockDiscoverResponse
	err      error
}

type bedrockConfiguredMsg struct {
	err error
}

type bedrockGovCloudMsg struct {
	govCloud bool
	err      error
}

func newBedrockStateDiscovering() *bedrockState {
	return &bedrockState{view: bedrockViewDiscovering}
}

func newBedrockStateDiscovered(response *appserver.BedrockDiscoverResponse) *bedrockState {
	state := &bedrockState{view: bedrockViewMethodsDetected}
	if response != nil {
		state.profiles = append([]appserver.BedrockAwsProfile(nil), response.Profiles...)
		state.environmentCredentials = append([]appserver.BedrockEnvironmentCredential(nil), response.EnvironmentCredentials...)
	}
	return state
}

func (s *bedrockState) methods() []bedrockMethod {
	if s == nil {
		return nil
	}
	if s.view == bedrockViewEnvironmentInstructions {
		return []bedrockMethod{{kind: bedrockMethodOther}}
	}
	detected := s.view == bedrockViewMethodsDetected
	if detected && len(s.profiles) > 0 {
		methods := make([]bedrockMethod, 0, len(s.profiles)+3)
		for index := range s.profiles {
			methods = append(methods, bedrockMethod{kind: bedrockMethodProfile, profileIndex: index})
		}
		if len(s.environmentCredentials) > 0 {
			methods = append(methods, bedrockMethod{kind: bedrockMethodEnvironment})
		}
		methods = append(methods, bedrockMethod{kind: bedrockMethodOther})
		return methods
	}
	if detected && len(s.environmentCredentials) > 0 {
		return []bedrockMethod{
			{kind: bedrockMethodEnvironment},
			{kind: bedrockMethodOther},
		}
	}
	return []bedrockMethod{
		{kind: bedrockMethodManualProfile},
		{kind: bedrockMethodAccessKeys},
		{kind: bedrockMethodEnvironmentInstructions},
		{kind: bedrockMethodAPIKey},
	}
}

func (s *bedrockState) isTextEntry() bool {
	if s == nil {
		return false
	}
	switch s.view {
	case bedrockViewProfileEntry, bedrockViewAccessKeyEntry, bedrockViewAPIKeyEntry, bedrockViewRegionEntry:
		return true
	default:
		return false
	}
}

func (s *bedrockState) activeInput() *string {
	if s == nil {
		return nil
	}
	switch s.view {
	case bedrockViewProfileEntry:
		return &s.profileInput
	case bedrockViewAPIKeyEntry:
		return &s.apiKeyInput
	case bedrockViewRegionEntry:
		return &s.regionInput
	case bedrockViewAccessKeyEntry:
		return &s.accessKeys[s.selectedField]
	default:
		return nil
	}
}

func (s *bedrockState) selectMethod(method bedrockMethod) (bedrockCredential, string, bool) {
	s.highlighted = 0
	switch method.kind {
	case bedrockMethodProfile:
		profile := s.profiles[method.profileIndex]
		credential := bedrockCredential{kind: bedrockCredentialProfile, profile: profile.Name}
		if profile.Region != nil && strings.TrimSpace(*profile.Region) != "" {
			return credential, strings.TrimSpace(*profile.Region), true
		}
		s.credential = credential
		s.regionInput = ""
		s.view = bedrockViewRegionEntry
	case bedrockMethodEnvironment:
		credential := bedrockCredential{kind: bedrockCredentialEnvironment}
		for _, discovered := range s.environmentCredentials {
			if discovered.Region != nil && strings.TrimSpace(*discovered.Region) != "" {
				return credential, strings.TrimSpace(*discovered.Region), true
			}
		}
		s.credential = credential
		s.regionInput = ""
		s.view = bedrockViewRegionEntry
	case bedrockMethodOther:
		s.view = bedrockViewMethodsAll
	case bedrockMethodManualProfile:
		s.profileInput = ""
		s.view = bedrockViewProfileEntry
	case bedrockMethodAccessKeys:
		s.accessKeys = [3]string{}
		s.selectedField = 0
		s.view = bedrockViewAccessKeyEntry
	case bedrockMethodEnvironmentInstructions:
		s.view = bedrockViewEnvironmentInstructions
	case bedrockMethodAPIKey:
		s.apiKeyInput = ""
		s.view = bedrockViewAPIKeyEntry
	}
	return bedrockCredential{}, "", false
}

func (s *bedrockState) handleKey(message bubbletea.KeyMsg) (back bool, credential *bedrockCredential, region string) {
	if message.Type == bubbletea.KeyEsc {
		if s.view == bedrockViewDiscovering || s.view == bedrockViewMethodsDetected {
			return true, nil, ""
		}
		s.view = bedrockViewMethodsDetected
		s.highlighted = 0
		return false, nil, ""
	}
	if s.view == bedrockViewConfiguring {
		return false, nil, ""
	}
	if s.view == bedrockViewGovCloudWarning {
		if message.Type == bubbletea.KeyEnter {
			return false, &bedrockCredential{}, ""
		}
		if message.Type == bubbletea.KeyUp && s.govCloudScroll > 0 {
			s.govCloudScroll--
		}
		if message.Type == bubbletea.KeyDown {
			s.govCloudScroll++
		}
		return false, nil, ""
	}
	if s.view == bedrockViewMethodsDetected || s.view == bedrockViewMethodsAll || s.view == bedrockViewEnvironmentInstructions {
		methods := s.methods()
		if len(methods) == 0 {
			return false, nil, ""
		}
		switch message.Type {
		case bubbletea.KeyUp:
			s.highlighted = (s.highlighted + len(methods) - 1) % len(methods)
			return false, nil, ""
		case bubbletea.KeyDown:
			s.highlighted = (s.highlighted + 1) % len(methods)
			return false, nil, ""
		case bubbletea.KeyEnter:
			selected, selectedRegion, configure := s.selectMethod(methods[s.highlighted])
			if configure {
				return false, &selected, selectedRegion
			}
			return false, nil, ""
		case bubbletea.KeyRunes:
			if len(message.Runes) == 1 && message.Runes[0] >= '1' && message.Runes[0] <= '9' {
				index := int(message.Runes[0] - '1')
				if index < len(methods) {
					selected, selectedRegion, configure := s.selectMethod(methods[index])
					if configure {
						return false, &selected, selectedRegion
					}
				}
			}
		}
		return false, nil, ""
	}
	if s.view == bedrockViewAccessKeyEntry {
		switch message.Type {
		case bubbletea.KeyUp, bubbletea.KeyShiftTab:
			s.selectedField = (s.selectedField + len(s.accessKeys) - 1) % len(s.accessKeys)
			return false, nil, ""
		case bubbletea.KeyDown, bubbletea.KeyTab:
			s.selectedField = (s.selectedField + 1) % len(s.accessKeys)
			return false, nil, ""
		}
	}
	if message.Type == bubbletea.KeyEnter {
		selected, selectedRegion, configure := s.confirmInput()
		if configure {
			return false, &selected, selectedRegion
		}
		return false, nil, ""
	}
	if input := s.activeInput(); input != nil {
		switch message.Type {
		case bubbletea.KeyBackspace, bubbletea.KeyDelete:
			runes := []rune(*input)
			if len(runes) > 0 {
				*input = string(runes[:len(runes)-1])
			}
		case bubbletea.KeyRunes:
			if !message.Alt && len(message.Runes) > 0 {
				value := string(message.Runes)
				if message.Paste {
					value = strings.TrimSpace(value)
				}
				*input += value
			}
		}
	}
	return false, nil, ""
}

func (s *bedrockState) confirmInput() (bedrockCredential, string, bool) {
	switch s.view {
	case bedrockViewProfileEntry:
		profile := strings.TrimSpace(s.profileInput)
		if profile == "" {
			return bedrockCredential{}, "", false
		}
		credential := bedrockCredential{kind: bedrockCredentialProfile, profile: profile}
		for _, discovered := range s.profiles {
			if discovered.Name == profile && discovered.Region != nil {
				return credential, strings.TrimSpace(*discovered.Region), true
			}
		}
		s.credential = credential
		s.regionInput = ""
		s.view = bedrockViewRegionEntry
	case bedrockViewAPIKeyEntry:
		apiKey := strings.TrimSpace(s.apiKeyInput)
		if apiKey == "" {
			return bedrockCredential{}, "", false
		}
		s.credential = bedrockCredential{kind: bedrockCredentialAPIKey, apiKey: apiKey}
		s.regionInput = ""
		s.view = bedrockViewRegionEntry
	case bedrockViewRegionEntry:
		region := strings.TrimSpace(s.regionInput)
		if region == "" {
			return bedrockCredential{}, "", false
		}
		return s.credential, region, true
	case bedrockViewAccessKeyEntry:
		if s.selectedField < 2 {
			if strings.TrimSpace(s.accessKeys[s.selectedField]) != "" {
				s.selectedField++
			}
			return bedrockCredential{}, "", false
		}
		if strings.TrimSpace(s.accessKeys[0]) == "" || strings.TrimSpace(s.accessKeys[1]) == "" {
			if strings.TrimSpace(s.accessKeys[0]) == "" {
				s.selectedField = 0
			} else {
				s.selectedField = 1
			}
			return bedrockCredential{}, "", false
		}
		s.credential = bedrockCredential{
			kind:            bedrockCredentialAccessKeys,
			accessKeyID:     strings.TrimSpace(s.accessKeys[0]),
			secretAccessKey: strings.TrimSpace(s.accessKeys[1]),
			sessionToken:    strings.TrimSpace(s.accessKeys[2]),
		}
		s.regionInput = ""
		s.view = bedrockViewRegionEntry
	}
	return bedrockCredential{}, "", false
}

func (s *bedrockState) render(width int, errorMessage string) string {
	if s == nil {
		return ""
	}
	lines := []string{"> Set up Amazon Bedrock", ""}
	switch s.view {
	case bedrockViewDiscovering:
		lines = append(lines, authDimStyle.Render("  Checking for existing AWS credentials..."))
	case bedrockViewConfiguring:
		lines = append(lines, authDimStyle.Render("  Setting up Amazon Bedrock..."))
	case bedrockViewMethodsDetected, bedrockViewMethodsAll:
		if s.view == bedrockViewMethodsDetected && len(s.profiles) == 1 {
			lines = append(lines, "  AWS profile detected: "+s.profiles[0].Name)
			if s.profiles[0].Region != nil {
				lines = append(lines, authDimStyle.Render("  Region: "+strings.TrimSpace(*s.profiles[0].Region)))
			}
		} else if s.view == bedrockViewMethodsDetected && len(s.profiles) > 1 {
			lines = append(lines, "  Choose an AWS profile.")
		} else if s.view == bedrockViewMethodsDetected && len(s.environmentCredentials) > 0 {
			lines = append(lines, "  AWS credentials detected in your environment.")
		} else {
			if s.view == bedrockViewMethodsDetected {
				lines = append(lines, "  No AWS credentials found.")
			}
			lines = append(lines, "  Choose how you authenticate with AWS.")
		}
		lines = append(lines, "")
		lines = append(lines, s.renderMethods()...)
	case bedrockViewProfileEntry:
		lines = append(lines, "  Enter the name of your AWS profile.", "", "  AWS profile: "+authCyanStyle.Render(s.profileInput))
	case bedrockViewAPIKeyEntry:
		lines = append(lines, "  Enter your Amazon Bedrock API key.", "", "  Bedrock API key: "+authCyanStyle.Render(maskSecret(s.apiKeyInput)))
	case bedrockViewRegionEntry:
		lines = append(lines, "  Enter the AWS Region to use with Amazon Bedrock.", "", "  AWS Region: "+authCyanStyle.Render(s.regionInput))
	case bedrockViewAccessKeyEntry:
		lines = append(lines, "  Enter your AWS access keys.", "")
		labels := []string{"AWS access key ID", "AWS secret access key", "AWS session token (optional)"}
		for index, label := range labels {
			marker := " "
			if index == s.selectedField {
				marker = ">"
			}
			value := s.accessKeys[index]
			if index == 0 {
				// Access key ids remain visible.
			} else {
				value = maskSecret(value)
			}
			line := fmt.Sprintf("%s %s: %s", marker, label, value)
			if index == s.selectedField {
				line = authCyanStyle.Render(line)
			}
			lines = append(lines, line)
		}
	case bedrockViewEnvironmentInstructions:
		lines = append(lines,
			"  Configure AWS credentials in your environment, then restart Codex.",
			"",
			"  Setup guide: "+authCyanStyle.Underline(true).Render("https://learn.chatgpt.com/docs/amazon-bedrock"),
			"",
		)
		lines = append(lines, s.renderMethods()...)
	case bedrockViewGovCloudWarning:
		lines = []string{
			"Using Codex with AWS GovCloud",
			"",
			"You are using Codex with AWS GovCloud. Please ensure you or your administrator have read the application configuration and security guidance before proceeding.",
			"",
			"> Acknowledge",
		}
	}
	if s.view != bedrockViewDiscovering && s.view != bedrockViewConfiguring {
		lines = append(lines, "", authDimStyle.Render("  Press enter to continue"))
		if s.view != bedrockViewGovCloudWarning {
			lines = append(lines, authDimStyle.Render("  Press esc to go back"))
		}
	}
	if errorMessage != "" {
		lines = append(lines, "", authErrorStyle.Render(errorMessage))
	}
	return wrapOnboardingLines(lines, width)
}

func (s *bedrockState) renderMethods() []string {
	methods := s.methods()
	lines := make([]string, 0, len(methods)*3)
	for index, method := range methods {
		title, description := s.methodCopy(method)
		marker := " "
		if index == s.highlighted {
			marker = ">"
		}
		line := fmt.Sprintf("%s %d. %s", marker, index+1, title)
		if index == s.highlighted {
			line = authCyanStyle.Render(line)
		}
		lines = append(lines, line)
		if description != "" {
			lines = append(lines, authDimStyle.Render("     "+description))
		}
		lines = append(lines, "")
	}
	return lines
}

func (s *bedrockState) methodCopy(method bedrockMethod) (string, string) {
	switch method.kind {
	case bedrockMethodProfile:
		profile := s.profiles[method.profileIndex]
		title := profile.Name
		if len(s.profiles) == 1 {
			title = "Continue with " + profile.Name
		}
		description := ""
		if len(s.profiles) == 1 {
			description = "Use your existing AWS credentials"
		} else if profile.Region != nil {
			description = strings.TrimSpace(*profile.Region)
		}
		return title, description
	case bedrockMethodEnvironment:
		description := "Use your existing AWS credentials"
		for _, credential := range s.environmentCredentials {
			if credential.CredentialType == appserver.AwsCredentialTypeBedrockAPIKey {
				description = "Use your existing Amazon Bedrock API key"
				break
			}
		}
		return "Continue with detected credentials", description
	case bedrockMethodOther:
		if s.view == bedrockViewEnvironmentInstructions {
			return "Choose another sign-in method", ""
		}
		return "Other AWS sign-in methods", "Use another profile, access keys, environment variables, or a Bedrock API key"
	case bedrockMethodManualProfile:
		return "AWS profile", "Use AWS SSO or a named profile"
	case bedrockMethodAccessKeys:
		return "AWS access keys", "Enter an access key ID and secret access key"
	case bedrockMethodEnvironmentInstructions:
		return "Environment variables", "Configure AWS credentials in your environment, then return here"
	default:
		return "Bedrock API key", "Enter a Bedrock API key"
	}
}

func maskSecret(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return ""
	}
	return strings.Repeat("\u2022", len(runes)-1) + string(runes[len(runes)-1])
}

func wrapOnboardingLines(lines []string, width int) string {
	if width < 20 {
		width = 20
	}
	if width > 100 {
		width = 100
	}
	return strings.Join(lines, "\n")
}

func (m *authFlowModel) discoverBedrockCmd() bubbletea.Cmd {
	discover := m.options.BedrockDiscover
	if discover == nil {
		return nil
	}
	return func() bubbletea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		response, err := discover(ctx)
		return bedrockDiscoveredMsg{response: response, err: err}
	}
}

func (m *authFlowModel) handleBedrockDiscovered(message bedrockDiscoveredMsg) (bubbletea.Model, bubbletea.Cmd) {
	if m.state != SignInBedrock || m.bedrock == nil || m.bedrock.view != bedrockViewDiscovering {
		return m, nil
	}
	m.bedrock = newBedrockStateDiscovered(message.response)
	if message.err != nil {
		m.errorMessage = "Unable to check AWS credentials: " + message.err.Error()
	}
	return m, nil
}

func (m *authFlowModel) handleBedrockKey(message bubbletea.KeyMsg) (bubbletea.Model, bubbletea.Cmd) {
	if m.bedrock == nil {
		return m, nil
	}
	m.errorMessage = ""
	back, credential, region := m.bedrock.handleKey(message)
	if back {
		m.state = SignInPickMode
		m.bedrock = nil
		m.highlighted = m.firstSelectableChoice()
		return m, nil
	}
	if credential == nil {
		return m, nil
	}
	if m.bedrock.view == bedrockViewGovCloudWarning {
		m.state = SignInBedrockConfigured
		m.completed = true
		return m, bubbletea.Quit
	}
	m.bedrock.view = bedrockViewConfiguring
	return m, m.configureBedrockCmd(*credential, region)
}

func (m *authFlowModel) configureBedrockCmd(credential bedrockCredential, region string) bubbletea.Cmd {
	return func() bubbletea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		switch credential.kind {
		case bedrockCredentialAPIKey:
			if m.options.BedrockLoginAPIKey == nil {
				err = fmt.Errorf("Bedrock API key login is unavailable")
			} else {
				err = m.options.BedrockLoginAPIKey(ctx, credential.apiKey, region)
			}
		case bedrockCredentialAccessKeys:
			if m.options.BedrockLoginAccessKeys == nil {
				err = fmt.Errorf("Bedrock access-key login is unavailable")
			} else {
				err = m.options.BedrockLoginAccessKeys(ctx, auth.LoginAccountParams{
					Type:            "amazonBedrockAccessKeys",
					AccessKeyID:     credential.accessKeyID,
					SecretAccessKey: credential.secretAccessKey,
					SessionToken:    credential.sessionToken,
					Region:          region,
				})
			}
		case bedrockCredentialEnvironment:
			if m.options.BedrockSetup == nil {
				err = fmt.Errorf("Bedrock environment setup is unavailable")
			} else {
				err = m.options.BedrockSetup(ctx, appserver.BedrockSetupParams{
					Type:   appserver.BedrockSetupTypeEnvironment,
					Region: region,
				})
			}
		default:
			if m.options.BedrockSetup == nil {
				err = fmt.Errorf("Bedrock profile setup is unavailable")
			} else {
				err = m.options.BedrockSetup(ctx, appserver.BedrockSetupParams{
					Type:    appserver.BedrockSetupTypeProfile,
					Profile: credential.profile,
					Region:  region,
				})
			}
		}
		return bedrockConfiguredMsg{err: err}
	}
}

func (m *authFlowModel) handleBedrockConfigured(message bedrockConfiguredMsg) (bubbletea.Model, bubbletea.Cmd) {
	if m.state != SignInBedrock || m.bedrock == nil || m.bedrock.view != bedrockViewConfiguring {
		return m, nil
	}
	if message.err != nil {
		m.errorMessage = "Unable to set up Amazon Bedrock: " + message.err.Error()
		m.bedrock.view = bedrockViewRegionEntry
		return m, nil
	}
	m.errorMessage = ""
	if m.options.BedrockCheckGovCloud != nil {
		return m, func() bubbletea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			govCloud, err := m.options.BedrockCheckGovCloud(ctx)
			return bedrockGovCloudMsg{govCloud: govCloud, err: err}
		}
	}
	m.state = SignInBedrockConfigured
	m.completed = true
	return m, bubbletea.Quit
}

func (m *authFlowModel) handleBedrockGovCloud(message bedrockGovCloudMsg) (bubbletea.Model, bubbletea.Cmd) {
	if m.state != SignInBedrock || m.bedrock == nil || m.bedrock.view != bedrockViewConfiguring {
		return m, nil
	}
	if message.err == nil && message.govCloud {
		m.bedrock.view = bedrockViewGovCloudWarning
		return m, nil
	}
	m.state = SignInBedrockConfigured
	m.completed = true
	return m, bubbletea.Quit
}
