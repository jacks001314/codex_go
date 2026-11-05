package pets

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	escapeSequence = "\x1b"
	stringTerm     = "\x1b\\"
	kittyChunkSize = 4096
)

type ImageProtocol string

const (
	ImageProtocolKitty          ImageProtocol = "kitty"
	ImageProtocolKittyLocalFile ImageProtocol = "kitty_local_file"
	ImageProtocolSixel          ImageProtocol = "sixel"
	ImageProtocolITerm2         ImageProtocol = "iterm2"
)

type ProtocolSelection string

const (
	ProtocolSelectionAuto  ProtocolSelection = "auto"
	ProtocolSelectionKitty ProtocolSelection = "kitty"
	ProtocolSelectionSixel ProtocolSelection = "sixel"
)

type PetImageUnsupportedReason string

const (
	PetImageUnsupportedTmux      PetImageUnsupportedReason = "tmux"
	PetImageUnsupportedZellij    PetImageUnsupportedReason = "zellij"
	PetImageUnsupportedIterm2Old PetImageUnsupportedReason = "iterm2_too_old"
	PetImageUnsupportedTerminal  PetImageUnsupportedReason = "terminal"
	PetImageUnsupportedDisabled  PetImageUnsupportedReason = "disabled"
)

type PetImageSupport struct {
	Protocol ImageProtocol
	Reason   PetImageUnsupportedReason
}

func (s PetImageSupport) Supported() bool {
	return s.Protocol != ""
}

func (s PetImageSupport) UnsupportedMessage() string {
	switch s.Reason {
	case PetImageUnsupportedTmux:
		return "Pets are unavailable in this tmux pane. Use fullscreen Codex with allow-passthrough all, or run Codex outside tmux."
	case PetImageUnsupportedZellij:
		return "Pets are disabled in Zellij. Terminal images don't stay reliably pane-local in Zellij. Run Codex outside Zellij to use pets."
	case PetImageUnsupportedIterm2Old:
		return "Pets require iTerm2 3.6 or newer. Upgrade iTerm2 to use terminal pets."
	case PetImageUnsupportedDisabled:
		return "Terminal pet images are disabled in this session."
	case "", PetImageUnsupportedTerminal:
		return "Pets aren't available in this terminal. Terminal pets need image support, and this terminal environment doesn't expose a supported image protocol. Try a terminal with Kitty graphics or Sixel support, or run Codex outside tmux."
	default:
		return string(s.Reason)
	}
}

func ResolveProtocolSelection(selection ProtocolSelection, env map[string]string) PetImageSupport {
	switch selection {
	case ProtocolSelectionKitty:
		return PetImageSupport{Protocol: ImageProtocolKitty}
	case ProtocolSelectionSixel:
		return PetImageSupport{Protocol: ImageProtocolSixel}
	default:
		return DetectImageSupport(env)
	}
}

// TmuxProbe carries the tmux client fields that decide whether pane-local
// Kitty passthrough is safe (Rust #59579/#59580).
type TmuxProbe struct {
	AllowPassthrough   string
	ClientTermname     string
	ClientTermtype     string
	ClientTermfeatures string
	PaneID             string
}

// ParseTmuxProbe parses the tab-separated `tmux display-message -p` output.
func ParseTmuxProbe(output string) (TmuxProbe, bool) {
	fields := strings.Split(strings.TrimRight(output, "\r\n"), "\t")
	if len(fields) < 4 {
		return TmuxProbe{}, false
	}
	return TmuxProbe{
		AllowPassthrough:   strings.TrimSpace(fields[0]),
		ClientTermname:     strings.TrimSpace(fields[1]),
		ClientTermtype:     strings.TrimSpace(fields[2]),
		ClientTermfeatures: strings.TrimSpace(fields[3]),
		PaneID:             strings.TrimSpace(fields[4]),
	}, true
}

// KittyPassthrough reports whether the tmux client can carry pane-local Kitty
// images: passthrough set to all on a Kitty or Ghostty client with RGB.
func (p TmuxProbe) KittyPassthrough() bool {
	if p.AllowPassthrough != "all" {
		return false
	}
	terminal := strings.ToLower(p.ClientTermname)
	kind := strings.ToLower(p.ClientTermtype)
	if terminal != "xterm-ghostty" && terminal != "xterm-kitty" &&
		!strings.HasPrefix(kind, "ghostty ") && !strings.HasPrefix(kind, "kitty(") {
		return false
	}
	for _, feature := range strings.Split(p.ClientTermfeatures, ",") {
		if strings.EqualFold(strings.TrimSpace(feature), "RGB") {
			return true
		}
	}
	return false
}

// KittyImageBase returns the pane-specific image-id base Rust reserves in the
// upper half of the 24-bit placeholder space (0x800000 + pane << 8).
func (p TmuxProbe) KittyImageBase() (uint32, bool) {
	pane := strings.TrimPrefix(p.PaneID, "%")
	value, err := strconv.ParseUint(pane, 10, 32)
	if err != nil || value >= 0x8000 {
		return 0, false
	}
	return 0x800000 + uint32(value)<<8, true
}

// DetectImageSupportInFullscreen resolves image support for the owned screen.
// Inside tmux, Kitty passthrough is only available in fullscreen with
// `allow-passthrough all`; otherwise tmux images stay disabled.
func DetectImageSupportInFullscreen(env map[string]string, probe *TmuxProbe, fullscreen bool) PetImageSupport {
	if !hasEnv(env, "TMUX") && !hasEnv(env, "TMUX_PANE") {
		return DetectImageSupport(env)
	}
	if fullscreen && probe != nil && probe.KittyPassthrough() {
		return PetImageSupport{Protocol: ImageProtocolKitty}
	}
	return PetImageSupport{Reason: PetImageUnsupportedTmux}
}

func DetectImageSupport(env map[string]string) PetImageSupport {
	if hasEnv(env, "TMUX") || hasEnv(env, "TMUX_PANE") {
		return PetImageSupport{Reason: PetImageUnsupportedTmux}
	}
	if hasEnv(env, "ZELLIJ") || hasEnv(env, "ZELLIJ_SESSION_NAME") || hasEnv(env, "ZELLIJ_VERSION") {
		return PetImageSupport{Reason: PetImageUnsupportedZellij}
	}
	if hasEnv(env, "KITTY_WINDOW_ID") || hasEnv(env, "WEZTERM_EXECUTABLE") || hasEnv(env, "WEZTERM_VERSION") {
		return PetImageSupport{Protocol: ImageProtocolKitty}
	}
	term := strings.ToLower(envValue(env, "TERM"))
	termProgram := strings.ToLower(envValue(env, "TERM_PROGRAM"))
	if strings.Contains(termProgram, "iterm") {
		if versionAtLeast(envValue(env, "TERM_PROGRAM_VERSION"), 3, 6, 0) {
			return PetImageSupport{Protocol: ImageProtocolKittyLocalFile}
		}
		return PetImageSupport{Reason: PetImageUnsupportedIterm2Old}
	}
	if strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || strings.Contains(term, "wezterm") || strings.Contains(term, "rex") ||
		strings.Contains(termProgram, "kitty") || strings.Contains(termProgram, "ghostty") || strings.Contains(termProgram, "wezterm") || termProgram == "rex" {
		return PetImageSupport{Protocol: ImageProtocolKitty}
	}
	if strings.Contains(term, "sixel") || strings.Contains(term, "mlterm") || strings.Contains(term, "foot") ||
		strings.Contains(termProgram, "windows terminal") || hasEnv(env, "WT_SESSION") {
		return PetImageSupport{Protocol: ImageProtocolSixel}
	}
	return PetImageSupport{Reason: PetImageUnsupportedTerminal}
}

func hasEnv(env map[string]string, key string) bool {
	value, ok := env[key]
	return ok && strings.TrimSpace(value) != ""
}

func envValue(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return env[key]
}

func versionAtLeast(value string, wantMajor int, wantMinor int, wantPatch int) bool {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return false
	}
	numbers := [3]int{}
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" {
			return false
		}
		n := 0
		for _, r := range parts[i] {
			if r < '0' || r > '9' {
				return false
			}
			n = n*10 + int(r-'0')
		}
		numbers[i] = n
	}
	want := [3]int{wantMajor, wantMinor, wantPatch}
	return numbers[0] > want[0] ||
		(numbers[0] == want[0] && numbers[1] > want[1]) ||
		(numbers[0] == want[0] && numbers[1] == want[1] && numbers[2] >= want[2])
}

func KittyDeleteImage(imageID uint32, env map[string]string) string {
	return WrapForTmuxIfNeeded(escapeSequence+"_Ga=d,d=I,i="+uintToString(imageID)+",q=2;"+stringTerm, env)
}

// KittyPlacementKind selects how Kitty anchors one image. Virtual placements
// attach the image to Unicode placeholder cells so tmux moves it with the pane
// instead of the terminal cursor (Rust #59579).
type KittyPlacementKind int

const (
	// KittyPlacementCursor draws the image at the cursor position.
	KittyPlacementCursor KittyPlacementKind = iota
	// KittyPlacementVirtual draws the image into placeholder cells.
	KittyPlacementVirtual
)

// KittyPlacement pairs an image id with its anchoring mode.
type KittyPlacement struct {
	Kind KittyPlacementKind
	ID   uint32
}

// CursorKittyPlacement anchors an image at the cursor.
func CursorKittyPlacement(id uint32) KittyPlacement {
	return KittyPlacement{Kind: KittyPlacementCursor, ID: id}
}

// VirtualKittyPlacement anchors an image to placeholder cells.
func VirtualKittyPlacement(id uint32) KittyPlacement {
	return KittyPlacement{Kind: KittyPlacementVirtual, ID: id}
}

// IsVirtual reports whether the placement uses Unicode placeholder cells.
func (p KittyPlacement) IsVirtual() bool {
	return p.Kind == KittyPlacementVirtual
}

const placeholderRune = "\U0010EEEE"

// kittyPlaceholderMarks addresses the image row of each placeholder cell with
// the combining marks Kitty documents for virtual placements.
var kittyPlaceholderMarks = [...]rune{'\u0305', '\u030d', '\u030e', '\u0310', '\u0312', '\u033d'}

// WriteVirtualKittyPlaceholders paints the placeholder grid for a virtual
// placement. The image id travels as a 24-bit foreground colour and Kitty
// inherits the first placeholder across the rest of each row.
func WriteVirtualKittyPlaceholders(writer io.Writer, placement KittyPlacement, x int, y int, columns int, rows int) error {
	if writer == nil || !placement.IsVirtual() || columns <= 0 || rows <= 0 {
		return nil
	}
	var builder strings.Builder
	builder.WriteString("\x1b[38;2;")
	builder.WriteString(uintToString((placement.ID >> 16) & 0xFF))
	builder.WriteByte(';')
	builder.WriteString(uintToString((placement.ID >> 8) & 0xFF))
	builder.WriteByte(';')
	builder.WriteString(uintToString(placement.ID & 0xFF))
	builder.WriteByte('m')
	remaining := strings.Repeat(placeholderRune, max(columns-1, 0))
	for row := 0; row < rows && row < len(kittyPlaceholderMarks); row++ {
		builder.WriteString("\x1b[")
		builder.WriteString(uintToString(uint32(max(y, 0) + row + 1)))
		builder.WriteByte(';')
		builder.WriteString(uintToString(uint32(max(x, 0) + 1)))
		builder.WriteByte('H')
		builder.WriteString(placeholderRune)
		builder.WriteRune(kittyPlaceholderMarks[row])
		builder.WriteString(remaining)
	}
	builder.WriteString("\x1b[39m")
	_, err := io.WriteString(writer, builder.String())
	return err
}

func KittyTransmitPNGWithID(path string, columns uint16, rows uint16, placement KittyPlacement, env map[string]string) (string, error) {
	png, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	payload := base64.StdEncoding.EncodeToString(png)
	chunks := splitKittyPayload(payload)
	var command strings.Builder
	for index, chunk := range chunks {
		more := "0"
		if index+1 < len(chunks) {
			more = "1"
		}
		if index == 0 {
			command.WriteString(escapeSequence)
			command.WriteString("_Ga=T,t=d,f=100,c=")
			command.WriteString(uintToString(uint32(columns)))
			command.WriteString(",r=")
			command.WriteString(uintToString(uint32(rows)))
			command.WriteString(",q=2")
			command.WriteString(kittyImageIDArg(placement))
			command.WriteString(",m=")
			command.WriteString(more)
			command.WriteString(",C=1;")
			command.WriteString(chunk)
			command.WriteString(stringTerm)
			continue
		}
		command.WriteString(escapeSequence)
		command.WriteString("_Gm=")
		command.WriteString(more)
		command.WriteString(";")
		command.WriteString(chunk)
		command.WriteString(stringTerm)
	}
	return WrapForTmuxIfNeeded(command.String(), env), nil
}

func KittyTransmitPNGFileWithID(path string, columns uint16, rows uint16, placement KittyPlacement, env map[string]string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	payload := base64.StdEncoding.EncodeToString([]byte(absolute))
	command := escapeSequence + "_Ga=T,t=f,f=100,c=" +
		uintToString(uint32(columns)) +
		",r=" +
		uintToString(uint32(rows)) +
		",q=2" +
		kittyImageIDArg(placement) +
		",C=1;" +
		payload +
		stringTerm
	return WrapForTmuxIfNeeded(command, env), nil
}

func WrapForTmuxIfNeeded(command string, env map[string]string) string {
	if !hasEnv(env, "TMUX") {
		return command
	}
	escaped := strings.ReplaceAll(command, escapeSequence, escapeSequence+escapeSequence)
	return escapeSequence + "Ptmux;" + escaped + stringTerm
}

func splitKittyPayload(payload string) []string {
	if payload == "" {
		return []string{""}
	}
	var chunks []string
	for len(payload) > kittyChunkSize {
		chunks = append(chunks, payload[:kittyChunkSize])
		payload = payload[kittyChunkSize:]
	}
	chunks = append(chunks, payload)
	return chunks
}

func kittyImageIDArg(placement KittyPlacement) string {
	if placement.IsVirtual() {
		return ",i=" + uintToString(placement.ID) + ",U=1"
	}
	return ",i=" + uintToString(placement.ID)
}

func uintToString(value uint32) string {
	if value == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
