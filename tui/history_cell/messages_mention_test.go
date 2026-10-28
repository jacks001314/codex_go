package historycell

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func TestStyleUserMessageMentionsColorsMention(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 229, G: 229, B: 229},
		BG: codextui.RGBColor{},
	})
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restoreLevel()
	got := styleUserMessageMentions("use $openai-docs and @plugin")
	if !strings.Contains(got, "\x1b[36m$openai-docs\x1b[0m") {
		t.Fatalf("tool mention not cyan: %q", got)
	}
	if !strings.Contains(got, "\x1b[36m@plugin\x1b[0m") {
		t.Fatalf("plugin mention not cyan: %q", got)
	}
	if utils.StripANSI(got) != "use $openai-docs and @plugin" {
		t.Fatalf("mention styling changed visible text: %q", utils.StripANSI(got))
	}
}

func TestStyleUserMessageMentionsLeavesEnvVarsAndEmails(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 229, G: 229, B: 229},
		BG: codextui.RGBColor{},
	})
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restoreLevel()
	got := styleUserMessageMentions("set $HOME and email user@example.com")
	if strings.Contains(got, "\x1b[36m$HOME\x1b[0m") {
		t.Fatalf("env var $HOME should not be cyan: %q", got)
	}
	if strings.Contains(got, "\x1b[36m@example\x1b[0m") {
		t.Fatalf("email @example should not be cyan: %q", got)
	}
	if utils.StripANSI(got) != "set $HOME and email user@example.com" {
		t.Fatalf("visible text changed: %q", utils.StripANSI(got))
	}
}
