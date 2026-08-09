package tea

import (
	codextui "codex_go/tui"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVirtualTerminalAppliesVT100CursorAndClear(t *testing.T) {
	terminal := newTestVirtualTerminal(8, 4)
	terminal.WriteString("alpha\nbravo")
	terminal.WriteString("\x1b[2;3HZ")
	terminal.WriteString("\x1b[3;1Htail")
	if got, want := terminal.Snapshot(), "alpha\nbrZvo\ntail"; got != want {
		t.Fatalf("snapshot after cursor writes = %q, want %q", got, want)
	}

	terminal.WriteString("\x1b[2J\x1b[Hok")
	if got, want := terminal.Snapshot(), "ok"; got != want {
		t.Fatalf("snapshot after clear = %q, want %q", got, want)
	}
}

func TestModelVT100TerminalSnapshotMainView(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-5",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
		ReasoningEffort: "high",
		Search:          true,
	})
	state.SetThreadID("thread-vt100")
	state.AddMessage(codextui.RoleUser, "summarize the repo")
	state.AddMessage(codextui.RoleAssistant, "The repo has a Go TUI shell.")
	model := NewModel(state, Options{Width: 82, Height: 18})

	assertVT100Snapshot(t, model.View(), 82, 24, `
Thread: thread-vt100 | Status: idle | Model: gpt-5 | Approval: on-request | San...

› summarize the repo


• The repo has a Go TUI shell.







> Ask gcode
>
>
Enter send | Ctrl+J newline | Ctrl+G editor | Ctrl+C quit | /help commands`)
}

func TestModelVT100TerminalSnapshotApprovalModal(t *testing.T) {
	model := NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5"}), Options{Width: 76, Height: 18})
	model.Update(ApprovalRequestMsg{
		ID:      "approval-vt100",
		Title:   "Run command?",
		Body:    "Reason: needs tests\nWorking directory: D:\\repo",
		Command: "go test ./...",
	})

	assertVT100Snapshot(t, model.View(), 76, 24, `
Thread: new | Status: idle | Model: gpt-5 | Approval: default | Sandbox: ...
No messages yet.






Run command?
  Reason: needs tests
  Working directory: D:\repo
  go test ./...
› 1. Allow for this turn (y)
  2. Allow for this session (a)
  3. Deny (d)
Esc cancel | Enter choose
Enter send | Ctrl+J newline | Ctrl+G editor | Ctrl+C quit | /help commands`)
}

func assertVT100Snapshot(t *testing.T, view string, width int, height int, want string) {
	t.Helper()
	terminal := newTestVirtualTerminal(width, height)
	terminal.WriteString("\x1b[2J\x1b[H")
	terminal.WriteString(view)
	got := strings.TrimSpace(terminal.Snapshot())
	want = strings.TrimSpace(strings.ReplaceAll(want, "\r\n", "\n"))
	if got != want {
		t.Fatalf("vt100 snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

type testVirtualTerminal struct {
	width  int
	height int
	x      int
	y      int
	wrap   bool
	savedX int
	savedY int
	saved  bool
	rows   [][]rune
}

func newTestVirtualTerminal(width int, height int) *testVirtualTerminal {
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	terminal := &testVirtualTerminal{width: width, height: height}
	terminal.clearAll()
	return terminal
}

func (t *testVirtualTerminal) WriteString(value string) {
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if r == '\x1b' {
			i += size
			t.consumeEscape(value, &i)
			continue
		}
		t.writeRune(r)
		i += size
	}
}

func (t *testVirtualTerminal) Snapshot() string {
	lines := make([]string, len(t.rows))
	for i := range t.rows {
		lines[i] = strings.TrimRight(string(t.rows[i]), " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func (t *testVirtualTerminal) writeRune(r rune) {
	switch r {
	case '\r':
		t.x = 0
		t.wrap = false
		return
	case '\n':
		t.x = 0
		t.wrap = false
		t.advanceLine()
		return
	case '\b':
		if t.x > 0 {
			t.x--
		}
		t.wrap = false
		return
	case '\t':
		next := ((t.x / 8) + 1) * 8
		for t.x < next {
			t.writePrintable(' ')
		}
		return
	}
	if r < ' ' {
		return
	}
	t.writePrintable(r)
}

func (t *testVirtualTerminal) writePrintable(r rune) {
	if t.wrap {
		t.x = 0
		t.wrap = false
		t.advanceLine()
	}
	if t.y < 0 || t.y >= t.height {
		return
	}
	if t.x >= 0 && t.x < t.width {
		t.rows[t.y][t.x] = r
	}
	if t.x == t.width-1 {
		t.wrap = true
		return
	}
	t.x++
}

func (t *testVirtualTerminal) consumeEscape(value string, index *int) {
	if *index >= len(value) {
		return
	}
	switch value[*index] {
	case '[':
		*index += 1
		start := *index
		for *index < len(value) {
			b := value[*index]
			if b >= 0x40 && b <= 0x7e {
				params := value[start:*index]
				*index += 1
				t.applyCSI(params, b)
				return
			}
			*index += 1
		}
	case ']':
		t.consumeStringEscape(value, index, true)
	case 'P', '_', '^':
		t.consumeStringEscape(value, index, false)
	case '7':
		t.savedX = t.x
		t.savedY = t.y
		t.saved = true
		*index += 1
	case '8':
		if t.saved {
			t.moveTo(t.savedX, t.savedY)
		}
		*index += 1
	default:
		*index += 1
	}
}

func (t *testVirtualTerminal) consumeStringEscape(value string, index *int, allowBell bool) {
	*index += 1
	for *index < len(value) {
		if allowBell && value[*index] == '\a' {
			*index += 1
			return
		}
		if value[*index] == '\x1b' && *index+1 < len(value) && value[*index+1] == '\\' {
			*index += 2
			return
		}
		*index += 1
	}
}

func (t *testVirtualTerminal) applyCSI(params string, final byte) {
	values := parseCSIParams(params)
	switch final {
	case 'H', 'f':
		row := csiParam(values, 0, 1)
		col := csiParam(values, 1, 1)
		t.moveTo(col-1, row-1)
	case 'A':
		t.moveTo(t.x, t.y-csiParam(values, 0, 1))
	case 'B':
		t.moveTo(t.x, t.y+csiParam(values, 0, 1))
	case 'C':
		t.moveTo(t.x+csiParam(values, 0, 1), t.y)
	case 'D':
		t.moveTo(t.x-csiParam(values, 0, 1), t.y)
	case 'G':
		t.moveTo(csiParam(values, 0, 1)-1, t.y)
	case 'd':
		t.moveTo(t.x, csiParam(values, 0, 1)-1)
	case 'J':
		t.clearScreen(csiParam(values, 0, 0))
	case 'K':
		t.clearLine(csiParam(values, 0, 0))
	case 'm', 'h', 'l':
		return
	}
}

func parseCSIParams(params string) []int {
	params = strings.TrimPrefix(params, "?")
	if params == "" {
		return nil
	}
	parts := strings.Split(params, ";")
	values := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err == nil {
			values[i] = value
		}
	}
	return values
}

func csiParam(values []int, index int, fallback int) int {
	if index >= len(values) || values[index] == 0 {
		return fallback
	}
	return values[index]
}

func (t *testVirtualTerminal) moveTo(x int, y int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= t.width {
		x = t.width - 1
	}
	if y >= t.height {
		y = t.height - 1
	}
	t.x = x
	t.y = y
	t.wrap = false
}

func (t *testVirtualTerminal) clearScreen(mode int) {
	switch mode {
	case 0:
		for y := t.y; y < t.height; y++ {
			start := 0
			if y == t.y {
				start = t.x
			}
			for x := start; x < t.width; x++ {
				t.rows[y][x] = ' '
			}
		}
	case 1:
		for y := 0; y <= t.y && y < t.height; y++ {
			end := t.width
			if y == t.y {
				end = t.x + 1
			}
			for x := 0; x < end && x < t.width; x++ {
				t.rows[y][x] = ' '
			}
		}
	case 2, 3:
		t.clearAll()
	}
}

func (t *testVirtualTerminal) clearLine(mode int) {
	if t.y < 0 || t.y >= t.height {
		return
	}
	start, end := 0, t.width
	switch mode {
	case 0:
		start = t.x
	case 1:
		end = t.x + 1
	}
	for x := start; x < end && x < t.width; x++ {
		t.rows[t.y][x] = ' '
	}
}

func (t *testVirtualTerminal) clearAll() {
	t.rows = make([][]rune, t.height)
	for y := range t.rows {
		t.rows[y] = make([]rune, t.width)
		for x := range t.rows[y] {
			t.rows[y][x] = ' '
		}
	}
	t.x = 0
	t.y = 0
	t.wrap = false
	t.saved = false
}

func (t *testVirtualTerminal) advanceLine() {
	t.y++
	if t.y < t.height {
		return
	}
	copy(t.rows, t.rows[1:])
	row := make([]rune, t.width)
	for x := range row {
		row[x] = ' '
	}
	t.rows[t.height-1] = row
	t.y = t.height - 1
	t.wrap = false
}
