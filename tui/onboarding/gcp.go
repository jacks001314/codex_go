package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
)

// Google Cloud Vertex AI setup collects provider settings and delegates
// validation and persistence to app-server. Setup stays in progress until the
// server finishes writing the configuration (Rust #57857,
// codex-rs/tui/src/onboarding/gcp.rs).

type gcpView int

const (
	gcpViewProject gcpView = iota
	gcpViewLocation
	gcpViewCredentials
	gcpViewConfiguring
)

type gcpState struct {
	view           gcpView
	projectID      string
	location       string
	credentialFile string
}

func newGCPState() *gcpState {
	return &gcpState{view: gcpViewProject}
}

func (s *gcpState) isTextEntryActive() bool {
	if s == nil {
		return false
	}
	return s.view != gcpViewConfiguring
}

func (s *gcpState) activeInput() *string {
	if s == nil {
		return nil
	}
	switch s.view {
	case gcpViewProject:
		return &s.projectID
	case gcpViewLocation:
		return &s.location
	case gcpViewCredentials:
		return &s.credentialFile
	default:
		return nil
	}
}

// handleKey advances the wizard. back reports that cancel returned to the
// provider menu; submit reports that a setup request must be dispatched;
// validation is a required-field message to show instead of advancing.
func (s *gcpState) handleKey(message bubbletea.KeyMsg) (back bool, submit bool, validation string) {
	if s == nil {
		return false, false, ""
	}
	if message.Type == bubbletea.KeyEsc {
		switch s.view {
		case gcpViewProject:
			return true, false, ""
		case gcpViewLocation:
			s.view = gcpViewProject
		case gcpViewCredentials:
			s.view = gcpViewLocation
		}
		return false, false, ""
	}
	// A dispatched setup can persist settings. Do not allow a second submission
	// until its result arrives.
	if !s.isTextEntryActive() {
		return false, false, ""
	}
	if message.Type == bubbletea.KeyEnter {
		switch s.view {
		case gcpViewProject:
			if strings.TrimSpace(s.projectID) == "" {
				return false, false, "Enter a Google Cloud project ID."
			}
			s.view = gcpViewLocation
		case gcpViewLocation:
			s.view = gcpViewCredentials
		case gcpViewCredentials:
			s.view = gcpViewConfiguring
			return false, true, ""
		}
		return false, false, ""
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
	return false, false, ""
}

func (s *gcpState) render(width int, errorMessage string) string {
	if s == nil {
		return ""
	}
	lines := []string{"> Set up Google Cloud Vertex AI", ""}
	var guidance, label, placeholder string
	showField := true
	switch s.view {
	case gcpViewProject:
		guidance = "Enter the Google Cloud project ID to use with Vertex AI."
		label, placeholder = "  Project ID: ", "my-project"
	case gcpViewLocation:
		guidance = "Press Enter to use global, or enter a Vertex AI location."
		label, placeholder = "  Location (optional): ", "global"
	case gcpViewCredentials:
		guidance = "Enter an absolute file path on the host running Codex, or press Enter to use Application Default Credentials (ADC)."
		label, placeholder = "  Credential file (optional): ", ""
	case gcpViewConfiguring:
		guidance = "Validating credentials and saving configuration..."
		showField = false
	}
	lines = append(lines, "  "+guidance)
	if showField {
		lines = append(lines, "")
		lines = append(lines, renderGCPField(label, s.activeInputValue(), placeholder))
	}
	if s.view == gcpViewCredentials {
		lines = append(lines,
			"",
			authDimStyle.Render("  To set up ADC with the Google Cloud CLI, run"),
			authCyanStyle.Render("  gcloud auth application-default login"),
		)
	}
	if s.isTextEntryActive() {
		action := " continue"
		if s.view == gcpViewCredentials {
			action = " set up"
		}
		lines = append(lines, "", authDimStyle.Render("  Press enter to"+action+" · Press esc to go back"))
	}
	if errorMessage != "" {
		lines = append(lines, "", authErrorStyle.Render(errorMessage))
	}
	return wrapOnboardingLines(lines, width)
}

func (s *gcpState) activeInputValue() string {
	if input := s.activeInput(); input != nil {
		return *input
	}
	return ""
}

// renderGCPField renders one single-line provider field: the placeholder when
// empty, otherwise the focused cyan value.
func renderGCPField(label, value, placeholder string) string {
	if strings.TrimSpace(value) == "" && placeholder != "" {
		return label + authDimStyle.Render(placeholder)
	}
	return label + authCyanStyle.Render(value)
}

type gcpConfiguredMsg struct {
	err error
}

func (m *authFlowModel) handleGCPKey(message bubbletea.KeyMsg) (bubbletea.Model, bubbletea.Cmd) {
	if m.gcp == nil {
		return m, nil
	}
	back, submit, validation := m.gcp.handleKey(message)
	if back {
		m.state = SignInPickMode
		m.gcp = nil
		m.highlighted = m.firstSelectableChoice()
		m.errorMessage = ""
		return m, nil
	}
	m.errorMessage = validation
	if submit {
		return m, m.configureGCPCmd(m.gcp)
	}
	return m, nil
}

func (m *authFlowModel) configureGCPCmd(state *gcpState) bubbletea.Cmd {
	setup := m.options.GcpSetup
	projectID := strings.TrimSpace(state.projectID)
	location := strings.TrimSpace(state.location)
	credentialFile := strings.TrimSpace(state.credentialFile)
	return func() bubbletea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if setup == nil {
			return gcpConfiguredMsg{err: fmt.Errorf("Google Cloud setup is unavailable in this build")}
		}
		params := appserver.GcpSetupParams{ProjectID: projectID}
		if location != "" {
			params.Location = &location
		}
		if credentialFile != "" {
			params.CredentialFile = &credentialFile
		}
		return gcpConfiguredMsg{err: setup(ctx, params)}
	}
}

func (m *authFlowModel) handleGCPConfigured(message gcpConfiguredMsg) (bubbletea.Model, bubbletea.Cmd) {
	if m.state != SignInGCP || m.gcp == nil || m.gcp.view != gcpViewConfiguring {
		return m, nil
	}
	if message.err != nil {
		m.errorMessage = "Unable to set up Google Cloud Vertex AI: " + message.err.Error()
		m.gcp.view = gcpViewCredentials
		return m, nil
	}
	m.errorMessage = ""
	m.state = SignInGCPConfigured
	m.completed = true
	return m, bubbletea.Quit
}
