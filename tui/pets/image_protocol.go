package pets

import (
	"encoding/base64"
	"os"
	"path/filepath"
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
		return "Pets are disabled in tmux. Terminal images don't stay pane-local in tmux and can corrupt scrollback or move between panes. Run Codex outside tmux to use pets."
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
	if strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || strings.Contains(term, "wezterm") ||
		strings.Contains(termProgram, "kitty") || strings.Contains(termProgram, "ghostty") || strings.Contains(termProgram, "wezterm") {
		return PetImageSupport{Protocol: ImageProtocolKitty}
	}
	if strings.Contains(term, "sixel") || strings.Contains(term, "mlterm") || strings.Contains(term, "foot") ||
		strings.Contains(termProgram, "windows terminal") {
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

func KittyTransmitPNGWithID(path string, columns uint16, rows uint16, imageID *uint32, env map[string]string) (string, error) {
	png, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	payload := base64.StdEncoding.EncodeToString(png)
	if payload == "" {
		payload = ""
	}
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
			command.WriteString(kittyImageIDArg(imageID))
			command.WriteString(",m=")
			command.WriteString(more)
			command.WriteString(";")
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

func KittyTransmitPNGFileWithID(path string, columns uint16, rows uint16, imageID *uint32, env map[string]string) (string, error) {
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
		kittyImageIDArg(imageID) +
		";" +
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

func kittyImageIDArg(imageID *uint32) string {
	if imageID == nil {
		return ""
	}
	return ",i=" + uintToString(*imageID)
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
