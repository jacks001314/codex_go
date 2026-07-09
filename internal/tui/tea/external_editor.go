package tea

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/internal/tui"
)

var (
	errExternalEditorMissing = errors.New("neither VISUAL nor EDITOR is set")
	errExternalEditorEmpty   = errors.New("editor command is empty")
	errExternalEditorParse   = errors.New("failed to parse editor command")
)

func (m *Model) openExternalEditor() bubbletea.Cmd {
	if m == nil || m.editorActive {
		return nil
	}
	runner := m.onExternalEditor
	if runner == nil {
		runner = defaultExternalEditorCmd
	}
	m.editorActive = true
	m.notice = ""
	seed := m.composer.Value()
	cmd := runner(seed)
	if cmd == nil {
		m.editorActive = false
		m.notice = "Failed to open editor: editor command did not start"
	}
	return cmd
}

func (m *Model) applyExternalEditorFinished(message ExternalEditorFinishedMsg) {
	if m == nil {
		return
	}
	m.editorActive = false
	if message.Err != nil {
		text := "Failed to open editor: " + message.Err.Error()
		if errors.Is(message.Err, errExternalEditorMissing) {
			text = "Cannot open external editor: set $VISUAL or $EDITOR before starting Codex."
		}
		m.notice = text
		m.State.AddMessage(codextui.RoleSystem, text)
		m.refreshTranscript()
		return
	}
	m.composer.SetValue(strings.TrimRightFunc(message.Text, unicode.IsSpace))
	m.notice = ""
}

func defaultExternalEditorCmd(seed string) bubbletea.Cmd {
	editor, err := resolveExternalEditorCommand()
	if err != nil {
		return func() bubbletea.Msg {
			return ExternalEditorFinishedMsg{Err: err}
		}
	}
	command := newExternalEditorCommand(seed, editor)
	return bubbletea.Exec(command, func(err error) bubbletea.Msg {
		if err != nil {
			return ExternalEditorFinishedMsg{Err: err}
		}
		return ExternalEditorFinishedMsg{Text: command.EditedText()}
	})
}

func resolveExternalEditorCommand() ([]string, error) {
	raw, ok := os.LookupEnv("VISUAL")
	if !ok {
		raw, ok = os.LookupEnv("EDITOR")
	}
	if !ok {
		return nil, errExternalEditorMissing
	}
	parts, err := splitEditorCommandLine(raw)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, errExternalEditorEmpty
	}
	return parts, nil
}

func newExternalEditorCommand(seed string, editor []string) *externalEditorCommand {
	return &externalEditorCommand{
		seed:   seed,
		editor: append([]string(nil), editor...),
	}
}

type externalEditorCommand struct {
	seed   string
	editor []string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	edited string
}

func (c *externalEditorCommand) SetStdin(reader io.Reader) {
	c.stdin = reader
}

func (c *externalEditorCommand) SetStdout(writer io.Writer) {
	c.stdout = writer
}

func (c *externalEditorCommand) SetStderr(writer io.Writer) {
	c.stderr = writer
}

func (c *externalEditorCommand) Run() error {
	if c == nil || len(c.editor) == 0 {
		return errExternalEditorEmpty
	}
	file, err := os.CreateTemp("", "codex-editor-*.md")
	if err != nil {
		return err
	}
	path := file.Name()
	if _, err := file.WriteString(c.seed); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	defer os.Remove(path)

	program := resolveExternalEditorProgram(c.editor[0])
	cmd := exec.Command(program, append(c.editor[1:], path)...)
	cmd.Stdin = c.stdin
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c.edited = string(data)
	return nil
}

func (c *externalEditorCommand) EditedText() string {
	if c == nil {
		return ""
	}
	return c.edited
}

func resolveExternalEditorProgram(program string) string {
	if runtime.GOOS != "windows" {
		return program
	}
	if resolved, err := exec.LookPath(program); err == nil {
		return resolved
	}
	return program
}

func splitEditorCommandLine(value string) ([]string, error) {
	var args []string
	var builder strings.Builder
	var quote rune
	escaped := false
	escapeBackslash := runtime.GOOS != "windows"

	flush := func() {
		if builder.Len() == 0 {
			return
		}
		args = append(args, builder.String())
		builder.Reset()
	}

	for _, r := range value {
		if escaped {
			builder.WriteRune(r)
			escaped = false
			continue
		}
		if escapeBackslash && r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				builder.WriteRune(r)
			}
			continue
		}
		switch {
		case unicode.IsSpace(r):
			flush()
		case r == '\'' || r == '"':
			quote = r
		default:
			builder.WriteRune(r)
		}
	}
	if escaped {
		builder.WriteRune('\\')
	}
	if quote != 0 {
		return nil, errExternalEditorParse
	}
	flush()
	if len(args) == 0 {
		return nil, errExternalEditorEmpty
	}
	for i, arg := range args {
		if strings.TrimSpace(arg) == "" {
			return nil, errExternalEditorEmpty
		}
		args[i] = arg
	}
	return args, nil
}
