package onboarding

import (
	mousemsg "codex_go/tui/mousemsg"
	"context"
	"errors"
	"io"
	"strings"

	"codex_go/tui/keymsg"

	bubbletea "charm.land/bubbletea/v2"
)

// GovCloudGuidanceURL is the configuration and security guidance linked from
// the startup acknowledgment screen (Rust #52176).
const GovCloudGuidanceURL = "https://learn.chatgpt.com/docs/enterprise/govcloud-configuration"

// GovCloudGuidanceResult reports how the startup guidance ended.
type GovCloudGuidanceResult struct {
	Acknowledged bool
	Cancelled    bool
}

type govCloudGuidanceModel struct {
	noAltScreen bool
	width       int
	height      int
	offset      int
	done        bool
	accepted    bool
}

// RunGovCloudGuidance shows the blocking AWS GovCloud startup guidance.
func RunGovCloudGuidance(ctx context.Context, input io.Reader, output io.Writer, noAltScreen bool) (GovCloudGuidanceResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	model := &govCloudGuidanceModel{width: 80, height: 24, noAltScreen: noAltScreen}
	options := []bubbletea.ProgramOption{bubbletea.WithContext(ctx)}
	if input != nil {
		options = append(options, bubbletea.WithInput(input))
	}
	if output != nil {
		options = append(options, bubbletea.WithOutput(output))
	}
	final, err := bubbletea.NewProgram(model, options...).Run()
	if err != nil {
		return GovCloudGuidanceResult{}, err
	}
	finished, ok := final.(*govCloudGuidanceModel)
	if !ok || finished == nil {
		return GovCloudGuidanceResult{}, errors.New("GovCloud guidance returned an unexpected model")
	}
	return GovCloudGuidanceResult{
		Acknowledged: finished.accepted,
		Cancelled:    !finished.accepted,
	}, nil
}

func (m *govCloudGuidanceModel) Init() bubbletea.Cmd { return nil }

func (m *govCloudGuidanceModel) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	if key, ok := keymsg.FromTea(message); ok {
		message = key
	} else if mouse, ok := mousemsg.FromTea(message); ok {
		message = mouse
	}
	switch message := message.(type) {
	case bubbletea.WindowSizeMsg:
		if message.Width > 0 {
			m.width = message.Width
		}
		if message.Height > 0 {
			m.height = message.Height
		}
		return m, nil
	case keymsg.KeyMsg:
		switch message.Type {
		case keymsg.KeyEnter:
			m.accepted = true
			m.done = true
			return m, bubbletea.Quit
		case keymsg.KeyEsc, keymsg.KeyCtrlC:
			m.done = true
			return m, bubbletea.Quit
		case keymsg.KeyUp:
			if m.offset > 0 {
				m.offset--
			}
		case keymsg.KeyDown:
			m.offset++
		}
	}
	return m, nil
}

func (m *govCloudGuidanceModel) View() bubbletea.View {
	v := bubbletea.NewView(m.viewString())
	v.AltScreen = !m.noAltScreen
	return v
}

func (m *govCloudGuidanceModel) viewString() string {
	if m == nil || m.done {
		return ""
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	lines := []string{
		"Using Codex with AWS GovCloud",
		"",
		"You are using Codex with AWS GovCloud. Please ensure you or your administrator have read the application configuration and security guidance before proceeding.",
		"",
		GovCloudGuidanceURL,
		"",
	}
	footer := []string{
		"",
		"> Acknowledge",
		"up/down scroll \u00b7 enter continue \u00b7 esc quit",
	}
	contentRows := m.height - len(footer)
	if contentRows < 1 {
		contentRows = 1
	}
	maxOffset := max(len(lines)-contentRows, 0)
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	visible := lines[m.offset:min(m.offset+contentRows, len(lines))]
	return strings.Join(append(append([]string{}, visible...), footer...), "\n")
}
