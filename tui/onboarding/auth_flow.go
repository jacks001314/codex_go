package onboarding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/auth"
)

const (
	chatGPTLoginDisabledMessage = "ChatGPT login is disabled."
	apiKeyLoginDisabledMessage  = "API key login is disabled."
)

var (
	authCyanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	authCyanDimStyle  = authCyanStyle.Copy().Faint(true)
	authDimStyle      = lipgloss.NewStyle().Faint(true)
	authErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	authSuccessStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	authInputBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6")).Padding(0, 1)
)

type AuthFlowOptions struct {
	CodexHome        string
	StoreOptions     *auth.StoreOptions
	ChatGPTAllowed   bool
	APIKeyAllowed    bool
	ForcedWorkspaces []string
	APIKeyFromEnv    string
	NoAltScreen      bool

	services *authFlowServices
}

type AuthFlowResult struct {
	Authenticated bool
	ShouldExit    bool
}

type authFlowServices struct {
	startBrowser       func(context.Context, *auth.OAuthOptions) (*auth.BrowserLoginServer, error)
	requestDeviceCode  func(context.Context, *auth.OAuthOptions) (*auth.DeviceCode, error)
	completeDeviceCode func(context.Context, *auth.OAuthOptions, *auth.DeviceCode) error
	saveAPIKey         func(*auth.Store, string) error
}

func defaultAuthFlowServices() *authFlowServices {
	return &authFlowServices{
		startBrowser:       auth.StartBrowserLogin,
		requestDeviceCode:  auth.RequestDeviceCode,
		completeDeviceCode: auth.CompleteDeviceCodeLogin,
		saveAPIKey: func(store *auth.Store, apiKey string) error {
			return store.Save(auth.FromAPIKey(apiKey))
		},
	}
}

type authFlowModel struct {
	ctx                 context.Context
	options             AuthFlowOptions
	services            *authFlowServices
	state               SignInState
	highlighted         AuthChoice
	errorMessage        string
	width               int
	apiKey              string
	apiKeyPrepopulated  bool
	savingAPIKey        bool
	browserURL          string
	browserServer       *auth.BrowserLoginServer
	deviceCode          *auth.DeviceCode
	nextAttemptID       uint64
	activeAttemptID     uint64
	activeAttemptCtx    context.Context
	activeAttemptCancel context.CancelFunc
	completed           bool
	shouldExit          bool
}

type browserStartedMsg struct {
	attemptID uint64
	server    *auth.BrowserLoginServer
	err       error
}

type deviceCodeStartedMsg struct {
	attemptID uint64
	code      *auth.DeviceCode
	err       error
}

type loginCompletedMsg struct {
	attemptID uint64
	err       error
}

type apiKeySavedMsg struct {
	err error
}

func RunAuthFlow(ctx context.Context, options AuthFlowOptions, input io.Reader, output io.Writer) (AuthFlowResult, error) {
	model := newAuthFlowModel(ctx, options)
	programOptions := make([]bubbletea.ProgramOption, 0, 4)
	if ctx != nil {
		programOptions = append(programOptions, bubbletea.WithContext(ctx))
	}
	if input != nil {
		programOptions = append(programOptions, bubbletea.WithInput(input))
	}
	if output != nil {
		programOptions = append(programOptions, bubbletea.WithOutput(output))
	}
	if !options.NoAltScreen {
		programOptions = append(programOptions, bubbletea.WithAltScreen())
	}
	final, err := bubbletea.NewProgram(model, programOptions...).Run()
	if err != nil {
		return AuthFlowResult{}, err
	}
	finalModel, ok := final.(*authFlowModel)
	if !ok || finalModel == nil {
		return AuthFlowResult{}, errors.New("authentication onboarding returned an unexpected model")
	}
	return finalModel.result(), nil
}

func newAuthFlowModel(ctx context.Context, options AuthFlowOptions) *authFlowModel {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(options.CodexHome) == "" {
		options.CodexHome = auth.DefaultCodexHome()
	}
	services := options.services
	if services == nil {
		services = defaultAuthFlowServices()
	}
	apiKeyFromEnv := strings.TrimSpace(options.APIKeyFromEnv)
	if apiKeyFromEnv == "" {
		apiKeyFromEnv = strings.TrimSpace(os.Getenv(auth.OpenAIAPIKeyEnv))
	}
	options.APIKeyFromEnv = apiKeyFromEnv
	model := &authFlowModel{
		ctx:      ctx,
		options:  options,
		services: services,
		state:    SignInPickMode,
		width:    80,
	}
	model.highlighted = model.firstSelectableChoice()
	return model
}

func (m *authFlowModel) Init() bubbletea.Cmd {
	return nil
}

func (m *authFlowModel) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch message := message.(type) {
	case bubbletea.WindowSizeMsg:
		if message.Width > 0 {
			m.width = message.Width
		}
		return m, nil
	case browserStartedMsg:
		return m.handleBrowserStarted(message)
	case deviceCodeStartedMsg:
		return m.handleDeviceCodeStarted(message)
	case loginCompletedMsg:
		return m.handleLoginCompleted(message)
	case apiKeySavedMsg:
		m.savingAPIKey = false
		if message.err != nil {
			m.errorMessage = "Failed to save API key: " + message.err.Error()
			return m, nil
		}
		m.errorMessage = ""
		m.state = SignInAPIKeyConfigured
		m.completed = true
		return m, bubbletea.Quit
	case bubbletea.KeyMsg:
		return m.handleKey(message)
	default:
		return m, nil
	}
}

func (m *authFlowModel) View() string {
	if m.completed || m.shouldExit {
		return ""
	}
	switch m.state {
	case SignInChatGPTContinueInBrowser:
		return m.renderBrowserLogin()
	case SignInChatGPTDeviceCode:
		return m.renderDeviceCodeLogin()
	case SignInChatGPTSuccessMessage:
		return m.renderChatGPTSuccessMessage()
	case SignInChatGPTSuccess:
		return authSuccessStyle.Render("? Signed in with your ChatGPT account")
	case SignInAPIKeyEntry:
		return m.renderAPIKeyEntry()
	case SignInAPIKeyConfigured:
		return authSuccessStyle.Render("? API key configured")
	default:
		return m.renderPickMode()
	}
}

func (m *authFlowModel) result() AuthFlowResult {
	return AuthFlowResult{Authenticated: m.completed, ShouldExit: m.shouldExit}
}

func (m *authFlowModel) handleKey(message bubbletea.KeyMsg) (bubbletea.Model, bubbletea.Cmd) {
	if message.Type == bubbletea.KeyCtrlC {
		m.shouldExit = true
		return m, bubbletea.Batch(m.cancelActiveAttempt(), bubbletea.Quit)
	}
	if m.state == SignInAPIKeyEntry {
		return m.handleAPIKeyEntryKey(message)
	}
	if message.Type == bubbletea.KeyEsc {
		switch m.state {
		case SignInChatGPTContinueInBrowser, SignInChatGPTDeviceCode:
			m.state = SignInPickMode
			m.errorMessage = ""
			m.browserURL = ""
			m.deviceCode = nil
			return m, m.cancelActiveAttempt()
		}
		return m, nil
	}
	if m.state == SignInChatGPTSuccessMessage && message.Type == bubbletea.KeyEnter {
		m.state = SignInChatGPTSuccess
		m.completed = true
		return m, bubbletea.Quit
	}
	if m.state != SignInPickMode {
		return m, nil
	}
	switch message.Type {
	case bubbletea.KeyUp:
		m.moveHighlight(-1)
		return m, nil
	case bubbletea.KeyDown:
		m.moveHighlight(1)
		return m, nil
	case bubbletea.KeyEnter:
		return m.startChoice(m.highlighted)
	case bubbletea.KeyRunes:
		if len(message.Runes) == 0 {
			return m, nil
		}
		switch strings.ToLower(string(message.Runes)) {
		case "k":
			m.moveHighlight(-1)
		case "j":
			m.moveHighlight(1)
		case "1", "y":
			return m.selectDisplayedOption(0)
		case "2", "n":
			return m.selectDisplayedOption(1)
		case "3":
			return m.selectDisplayedOption(2)
		case "q":
			m.shouldExit = true
			return m, bubbletea.Quit
		}
	}
	return m, nil
}

func (m *authFlowModel) handleAPIKeyEntryKey(message bubbletea.KeyMsg) (bubbletea.Model, bubbletea.Cmd) {
	switch message.Type {
	case bubbletea.KeyEsc:
		m.state = SignInPickMode
		m.errorMessage = ""
		m.savingAPIKey = false
		return m, nil
	case bubbletea.KeyEnter:
		if m.savingAPIKey {
			return m, nil
		}
		apiKey := strings.TrimSpace(m.apiKey)
		if apiKey == "" {
			m.errorMessage = "API key cannot be empty"
			return m, nil
		}
		m.savingAPIKey = true
		m.errorMessage = ""
		store := auth.NewStoreWithOptions(m.options.CodexHome, m.options.StoreOptions)
		return m, func() bubbletea.Msg {
			return apiKeySavedMsg{err: m.services.saveAPIKey(store, apiKey)}
		}
	case bubbletea.KeyBackspace, bubbletea.KeyDelete:
		if m.savingAPIKey {
			return m, nil
		}
		if m.apiKeyPrepopulated {
			m.apiKey = ""
			m.apiKeyPrepopulated = false
		} else {
			runes := []rune(m.apiKey)
			if len(runes) > 0 {
				m.apiKey = string(runes[:len(runes)-1])
			}
		}
		m.errorMessage = ""
		return m, nil
	case bubbletea.KeyRunes:
		if m.savingAPIKey || message.Alt || len(message.Runes) == 0 {
			return m, nil
		}
		value := string(message.Runes)
		if message.Paste {
			value = strings.TrimSpace(value)
		}
		if value == "" {
			return m, nil
		}
		if m.apiKeyPrepopulated {
			m.apiKey = value
			m.apiKeyPrepopulated = false
		} else {
			m.apiKey += value
		}
		m.errorMessage = ""
	}
	return m, nil
}

func (m *authFlowModel) startChoice(choice AuthChoice) (bubbletea.Model, bubbletea.Cmd) {
	switch choice {
	case AuthChoiceChatGPT:
		if !m.options.ChatGPTAllowed {
			m.errorMessage = chatGPTLoginDisabledMessage
			return m, nil
		}
		attemptID, attemptCtx := m.beginAttempt()
		m.state = SignInChatGPTContinueInBrowser
		m.browserURL = ""
		m.errorMessage = ""
		oauthOptions := m.oauthOptions(true)
		return m, func() bubbletea.Msg {
			server, err := m.services.startBrowser(attemptCtx, oauthOptions)
			return browserStartedMsg{attemptID: attemptID, server: server, err: err}
		}
	case AuthChoiceDeviceCode:
		if !m.options.ChatGPTAllowed {
			m.errorMessage = chatGPTLoginDisabledMessage
			return m, nil
		}
		attemptID, attemptCtx := m.beginAttempt()
		m.state = SignInChatGPTDeviceCode
		m.deviceCode = nil
		m.errorMessage = ""
		oauthOptions := m.oauthOptions(false)
		return m, func() bubbletea.Msg {
			code, err := m.services.requestDeviceCode(attemptCtx, oauthOptions)
			return deviceCodeStartedMsg{attemptID: attemptID, code: code, err: err}
		}
	case AuthChoiceAPIKey:
		if !m.options.APIKeyAllowed {
			m.errorMessage = apiKeyLoginDisabledMessage
			return m, nil
		}
		m.state = SignInAPIKeyEntry
		m.errorMessage = ""
		if m.apiKey == "" && m.options.APIKeyFromEnv != "" {
			m.apiKey = m.options.APIKeyFromEnv
			m.apiKeyPrepopulated = true
		}
	}
	return m, nil
}

func (m *authFlowModel) selectDisplayedOption(index int) (bubbletea.Model, bubbletea.Cmd) {
	options := m.displayedOptions()
	if index < 0 || index >= len(options) {
		return m, nil
	}
	return m.startChoice(options[index].Choice)
}

func (m *authFlowModel) handleBrowserStarted(message browserStartedMsg) (bubbletea.Model, bubbletea.Cmd) {
	if message.attemptID != m.activeAttemptID || m.state != SignInChatGPTContinueInBrowser {
		return m, cancelBrowserServerCmd(message.server)
	}
	if message.err != nil {
		m.failAttempt(message.err)
		return m, nil
	}
	if message.server == nil {
		m.failAttempt(errors.New("browser login did not start"))
		return m, nil
	}
	m.browserServer = message.server
	m.browserURL = strings.TrimSpace(message.server.AuthURL)
	return m, func() bubbletea.Msg {
		return loginCompletedMsg{attemptID: message.attemptID, err: <-message.server.Done}
	}
}

func (m *authFlowModel) handleDeviceCodeStarted(message deviceCodeStartedMsg) (bubbletea.Model, bubbletea.Cmd) {
	if message.attemptID != m.activeAttemptID || m.state != SignInChatGPTDeviceCode {
		return m, nil
	}
	if message.err != nil {
		m.failAttempt(message.err)
		return m, nil
	}
	if message.code == nil {
		m.failAttempt(errors.New("device code login did not return a code"))
		return m, nil
	}
	m.deviceCode = message.code
	oauthOptions := m.oauthOptions(false)
	attemptCtx := m.activeAttemptCtx
	return m, func() bubbletea.Msg {
		return loginCompletedMsg{
			attemptID: message.attemptID,
			err:       m.services.completeDeviceCode(attemptCtx, oauthOptions, message.code),
		}
	}
}

func (m *authFlowModel) handleLoginCompleted(message loginCompletedMsg) (bubbletea.Model, bubbletea.Cmd) {
	if message.attemptID != m.activeAttemptID {
		return m, nil
	}
	if message.err != nil {
		m.failAttempt(message.err)
		return m, nil
	}
	m.finishAttempt()
	m.errorMessage = ""
	m.state = SignInChatGPTSuccessMessage
	return m, nil
}

func (m *authFlowModel) beginAttempt() (uint64, context.Context) {
	if m.activeAttemptCancel != nil {
		m.activeAttemptCancel()
	}
	m.nextAttemptID++
	m.activeAttemptID = m.nextAttemptID
	attemptCtx, cancel := context.WithCancel(m.ctx)
	m.activeAttemptCtx = attemptCtx
	m.activeAttemptCancel = cancel
	m.browserServer = nil
	return m.activeAttemptID, attemptCtx
}

func (m *authFlowModel) finishAttempt() {
	if m.activeAttemptCancel != nil {
		m.activeAttemptCancel()
	}
	m.activeAttemptCtx = nil
	m.activeAttemptCancel = nil
	m.activeAttemptID = 0
	m.browserServer = nil
}

func (m *authFlowModel) failAttempt(err error) {
	m.finishAttempt()
	m.state = SignInPickMode
	m.browserURL = ""
	m.deviceCode = nil
	if err != nil && !errors.Is(err, context.Canceled) {
		m.errorMessage = err.Error()
	} else {
		m.errorMessage = ""
	}
}

func (m *authFlowModel) cancelActiveAttempt() bubbletea.Cmd {
	cancel := m.activeAttemptCancel
	server := m.browserServer
	m.activeAttemptCtx = nil
	m.activeAttemptCancel = nil
	m.activeAttemptID = 0
	m.browserServer = nil
	if cancel != nil {
		cancel()
	}
	return cancelBrowserServerCmd(server)
}

func cancelBrowserServerCmd(server *auth.BrowserLoginServer) bubbletea.Cmd {
	if server == nil {
		return nil
	}
	return func() bubbletea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Cancel(ctx)
		return nil
	}
}

func (m *authFlowModel) oauthOptions(openBrowser bool) *auth.OAuthOptions {
	return &auth.OAuthOptions{
		CodexHome:        m.options.CodexHome,
		OpenBrowser:      openBrowser,
		ForcedWorkspaces: append([]string(nil), m.options.ForcedWorkspaces...),
		StoreOptions:     m.options.StoreOptions,
	}
}

func (m *authFlowModel) displayedOptions() []AuthOption {
	return AuthOptionsForPolicy(m.options.ChatGPTAllowed, m.options.APIKeyAllowed)
}

func (m *authFlowModel) selectableChoices() []AuthChoice {
	options := m.displayedOptions()
	choices := make([]AuthChoice, 0, len(options))
	for _, option := range options {
		if !option.Disabled {
			choices = append(choices, option.Choice)
		}
	}
	return choices
}

func (m *authFlowModel) firstSelectableChoice() AuthChoice {
	choices := m.selectableChoices()
	if len(choices) == 0 {
		return AuthChoiceChatGPT
	}
	return choices[0]
}

func (m *authFlowModel) moveHighlight(delta int) {
	choices := m.selectableChoices()
	if len(choices) == 0 {
		return
	}
	current := 0
	for index, choice := range choices {
		if choice == m.highlighted {
			current = index
			break
		}
	}
	next := (current + delta) % len(choices)
	if next < 0 {
		next += len(choices)
	}
	m.highlighted = choices[next]
}

func (m *authFlowModel) renderPickMode() string {
	lines := []string{
		"  Sign in with ChatGPT to use Codex as part of your paid plan",
		"  or connect an API key for usage-based billing",
		"",
	}
	for index, option := range m.displayedOptions() {
		selected := option.Choice == m.highlighted && !option.Disabled
		prefix := "  "
		if selected {
			prefix = "> "
		}
		label := fmt.Sprintf("%s%d. %s", prefix, index+1, option.Label)
		description := "     " + option.Description
		switch {
		case selected:
			lines = append(lines, authCyanStyle.Render(label), authCyanDimStyle.Render(description))
		case option.Disabled:
			lines = append(lines, authDimStyle.Render(label), authDimStyle.Render("     ChatGPT login is disabled"))
		default:
			lines = append(lines, label, authDimStyle.Render(description))
		}
		lines = append(lines, "")
	}
	if !m.options.APIKeyAllowed {
		lines = append(lines,
			authDimStyle.Render("  API key login is disabled by this workspace. Sign in with ChatGPT to continue."),
			"",
		)
	}
	lines = append(lines, authDimStyle.Render("  Press enter to continue"))
	if m.errorMessage != "" {
		lines = append(lines, "", authErrorStyle.Render(m.errorMessage))
	}
	return strings.Join(lines, "\n")
}

func (m *authFlowModel) renderBrowserLogin() string {
	lines := []string{"  Finish signing in via your browser", ""}
	if m.browserURL == "" {
		lines = append(lines, authDimStyle.Render("  Starting browser login..."), "")
	} else {
		lines = append(lines,
			"  If the link doesn't open automatically, open the following link to authenticate:",
			"",
			"  "+authCyanStyle.Underline(true).Render(m.browserURL),
			"",
			"  On a remote or headless machine? Press esc and choose "+authCyanStyle.Render("Sign in with Device Code")+".",
			"",
		)
	}
	lines = append(lines, authDimStyle.Render("  Press esc to cancel"))
	return strings.Join(lines, "\n")
}

func (m *authFlowModel) renderDeviceCodeLogin() string {
	banner := "  Preparing device code login"
	if m.deviceCode != nil {
		banner = "  Finish signing in via your browser"
	}
	lines := []string{banner, ""}
	if m.deviceCode == nil {
		lines = append(lines, authDimStyle.Render("  Requesting a one-time code..."), "")
	} else {
		lines = append(lines,
			"  1. Open this link in your browser and sign in",
			"",
			"  "+authCyanStyle.Underline(true).Render(strings.TrimSpace(m.deviceCode.VerificationURL)),
			"",
			"  2. Enter this one-time code after you are signed in (expires in 15 minutes)",
			"",
			"  "+authCyanStyle.Bold(true).Render(strings.TrimSpace(m.deviceCode.UserCode)),
			"",
			authDimStyle.Render("  Continue only if you started this login in Codex. If a website or another person gave you this code, cancel."),
			"",
		)
	}
	lines = append(lines, authDimStyle.Render("  Press esc to cancel"))
	return strings.Join(lines, "\n")
}

func (m *authFlowModel) renderChatGPTSuccessMessage() string {
	return strings.Join([]string{
		authSuccessStyle.Render("? Signed in with your ChatGPT account"),
		"",
		"  Before you start:",
		"",
		"  Decide how much autonomy you want to grant Codex",
		authDimStyle.Render("  Review the code it writes and commands it runs"),
		"",
		authCyanStyle.Render("  Press enter to continue"),
	}, "\n")
}

func (m *authFlowModel) renderAPIKeyEntry() string {
	input := m.apiKey
	if input == "" {
		input = authDimStyle.Render("Paste or type your API key")
	}
	inputWidth := m.width - 8
	if inputWidth < 24 {
		inputWidth = 24
	}
	if inputWidth > 72 {
		inputWidth = 72
	}
	box := authInputBoxStyle.Copy().Width(inputWidth).Render(input)
	lines := []string{
		"> " + lipgloss.NewStyle().Bold(true).Render("Use your own OpenAI API key for usage-based billing"),
		"",
		"  Paste or type your API key below. It will be stored locally in auth.json.",
		"",
	}
	if m.apiKeyPrepopulated {
		lines = append(lines,
			"  Detected OPENAI_API_KEY environment variable.",
			authDimStyle.Render("  Paste a different key if you prefer to use another account."),
			"",
		)
	}
	lines = append(lines, box, "")
	if m.savingAPIKey {
		lines = append(lines, authDimStyle.Render("  Saving API key..."))
	} else {
		lines = append(lines,
			authDimStyle.Render("  Press enter to save"),
			authDimStyle.Render("  Press esc to go back"),
		)
	}
	if m.errorMessage != "" {
		lines = append(lines, "", authErrorStyle.Render(m.errorMessage))
	}
	return strings.Join(lines, "\n")
}
