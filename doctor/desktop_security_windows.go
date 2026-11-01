//go:build windows

package doctor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// desktopSecurityEnforcement mirrors Rust's windows_security module: it asks
// wevtutil for the last week of Defender, AppLocker and App Control events that
// mention a Codex executable and classifies what it finds.

const (
	desktopMaxEventsPerChannel = 256
	desktopMaxRenderedEvents   = 16
	desktopMaxEventOutputBytes = 2 * 1024 * 1024
	desktopSecurityQueryWindow = 604800000
)

type desktopSecurityChannel struct {
	source string
	name   string
	ids    []int
}

var desktopSecurityChannels = []desktopSecurityChannel{
	{
		source: "microsoft_defender",
		name:   "Microsoft-Windows-Windows Defender/Operational",
		ids:    []int{1006, 1007, 1116, 1117, 1121, 1122, 1123, 1124, 1125, 1126, 1127, 1128},
	},
	{source: "applocker", name: "Microsoft-Windows-AppLocker/EXE and DLL", ids: []int{8003, 8004}},
	{source: "applocker", name: "Microsoft-Windows-AppLocker/Packaged app-Execution", ids: []int{8021, 8022}},
	{source: "applocker", name: "Microsoft-Windows-AppLocker/MSI and Script", ids: []int{8006, 8007}},
	{source: "windows_app_control", name: "Microsoft-Windows-CodeIntegrity/Operational", ids: []int{3076, 3077}},
}

type desktopSecurityEvidence struct {
	status CheckStatus
	detail string
}

func desktopSecurityEnforcement() *DoctorCheck {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return classifyDesktopSecurity(nil)
	}
	wevtutil := filepath.Join(systemRoot, "System32", "wevtutil.exe")
	channels := make([]*[]desktopSecurityEvidence, 0, len(desktopSecurityChannels))
	for _, channel := range desktopSecurityChannels {
		channels = append(channels, queryDesktopSecurityChannel(wevtutil, channel))
	}
	return classifyDesktopSecurity(channels)
}

func queryDesktopSecurityChannel(wevtutil string, channel desktopSecurityChannel) *[]desktopSecurityEvidence {
	conditions := make([]string, 0, len(channel.ids))
	for _, id := range channel.ids {
		conditions = append(conditions, "EventID="+strconv.Itoa(id))
	}
	query := "*[System[(" + strings.Join(conditions, " or ") +
		") and TimeCreated[timediff(@SystemTime) <= " + strconv.Itoa(desktopSecurityQueryWindow) + "]]]"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, wevtutil, "qe", channel.name, "/q:"+query,
		"/c:"+strconv.Itoa(desktopMaxEventsPerChannel), "/rd:true", "/f:xml")
	command.Stdin = nil
	command.Stderr = nil
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := command.Start(); err != nil {
		return nil
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, desktopMaxEventOutputBytes+1))
	waitErr := command.Wait()
	if readErr != nil || waitErr != nil || len(output) > desktopMaxEventOutputBytes {
		return nil
	}
	events := parseDesktopSecurityEvents(string(output), channel)
	return &events
}

func parseDesktopSecurityEvents(xml string, channel desktopSecurityChannel) []desktopSecurityEvidence {
	events := []desktopSecurityEvidence{}
	for index, event := range strings.Split(xml, "</Event>") {
		if index >= desktopMaxEventsPerChannel {
			break
		}
		idText, ok := desktopXMLElement(event, "EventID")
		if !ok {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(idText))
		if err != nil || !desktopSecurityChannelHasID(channel, id) {
			continue
		}
		target, ok := desktopSecurityEventTarget(event)
		if !ok {
			continue
		}
		timestamp, ok := desktopSecurityEventTime(event)
		if !ok {
			continue
		}
		status, action, ok := desktopSecurityEventOutcome(event, id)
		if !ok {
			continue
		}
		events = append(events, desktopSecurityEvidence{
			status: status,
			detail: "source: " + channel.source + "; event: " + strconv.Itoa(id) +
				"; target: " + target + "; action: " + action + "; time: " + timestamp,
		})
	}
	return events
}

func desktopSecurityChannelHasID(channel desktopSecurityChannel, id int) bool {
	for _, candidate := range channel.ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func desktopSecurityEventTarget(event string) (string, bool) {
	values := []string{}
	for index, part := range strings.Split(event, "<Data") {
		if index == 0 {
			continue
		}
		_, rest, ok := strings.Cut(part, ">")
		if !ok {
			continue
		}
		value, _, ok := strings.Cut(rest, "</Data>")
		if !ok {
			continue
		}
		values = append(values, value)
	}
	for _, name := range []string{"FilePath", "FileName", "Image", "PackageName", "PackageFamilyName"} {
		if value, ok := desktopXMLElement(event, name); ok {
			values = append(values, value)
		}
	}
	for _, value := range values {
		if target, ok := desktopSecurityTargetName(value); ok {
			return target, true
		}
	}
	return "", false
}

// desktopSecurityTargetName maps an executable path onto the names Rust reports,
// and only reports ChatGPT/Electron/ripgrep binaries that live inside a Codex
// package or an OpenAI/Codex directory.
func desktopSecurityTargetName(value string) (string, bool) {
	lower := strings.ToLower(value)
	parts := strings.FieldsFunc(lower, func(unit rune) bool { return unit == '\\' || unit == '/' })
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	if len(parts) == 0 {
		return "", false
	}
	name := parts[len(parts)-1]
	inPackage := false
	for _, part := range parts {
		if strings.HasPrefix(part, "openai.codex_") {
			inPackage = true
		}
	}
	trusted := inPackage
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] == "openai" && parts[index+1] == "codex" {
			trusted = true
		}
	}
	switch name {
	case "codex-windows-sandbox-setup.exe":
		return "sandbox_setup", true
	case "codex-command-runner.exe":
		return "command_runner", true
	case "codex.exe":
		return "codex", true
	case "codex-desktop.exe":
		return "codex_desktop", true
	case "chatgpt.exe", "electron.exe":
		if trusted {
			return "codex_desktop", true
		}
	case "rg.exe":
		if trusted {
			return "ripgrep", true
		}
	}
	if inPackage {
		return "codex_desktop_package", true
	}
	return "", false
}

func desktopSecurityEventTime(event string) (string, bool) {
	_, rest, ok := strings.Cut(event, `SystemTime="`)
	if !ok {
		return "", false
	}
	timestamp, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return "", false
	}
	return timestamp, true
}

func desktopSecurityEventOutcome(event string, id int) (CheckStatus, string, bool) {
	switch id {
	case 1121, 1123, 1126, 1127, 8004, 8007, 8022, 3077:
		return CheckStatusFail, "blocked", true
	case 1122, 1124, 1125, 1128, 8003, 8006, 8021, 3076:
		return CheckStatusWarning, "audited", true
	case 1006, 1116:
		return CheckStatusWarning, "detected", true
	case 1007, 1117:
		status, action := desktopDefenderAction(event)
		return status, action, true
	}
	return "", "", false
}

func desktopDefenderAction(event string) (CheckStatus, string) {
	action := ""
	for _, name := range []string{"Action Name", "ActionName", "Action", "Action ID", "ActionID"} {
		if value, ok := desktopXMLDataValue(event, name); ok {
			action = value
			break
		}
	}
	if action == "" {
		if value, ok := desktopXMLElement(event, "ActionName"); ok {
			action = value
		}
	}
	switch strings.ToLower(action) {
	case "quarantine", "2":
		return CheckStatusFail, "quarantined"
	case "clean", "remove", "block", "1", "3", "10":
		return CheckStatusFail, "blocked"
	case "allow", "ignore", "none", "no action", "user defined", "6", "8", "9", "11":
		return CheckStatusOK, "allowed"
	default:
		return CheckStatusWarning, "detected"
	}
}

func classifyDesktopSecurity(channels []*[]desktopSecurityEvidence) *DoctorCheck {
	visible := false
	missing := false
	for _, channel := range channels {
		if channel == nil {
			missing = true
			continue
		}
		visible = true
	}
	status := CheckStatusOK
	if !visible {
		status = CheckStatusWarning
	}
	details := []string{}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		for _, evidence := range *channel {
			status = worseCheckStatus(status, evidence.status)
			if len(details) < desktopMaxRenderedEvents {
				details = append(details, evidence.detail)
			}
		}
	}
	summary := "endpoint security blocked or quarantined a Codex executable"
	switch status {
	case CheckStatusOK:
		if missing {
			summary = "security event coverage is incomplete"
		} else {
			summary = "no locally visible recent Codex security enforcement was found"
		}
	case CheckStatusWarning:
		if visible {
			summary = "recent Codex security audit or detection requires review"
		} else {
			summary = "security event channels could not be inspected"
		}
	}
	check := NewCheck("desktop.security.enforcement", "desktop", status, summary)
	for _, detail := range details {
		check = check.Detail(detail)
	}
	if status != CheckStatusOK {
		check = check.Remediate("ask your organization's security administrator to review endpoint security events and the approved Codex application policy")
	}
	return check
}

func desktopXMLElement(xml string, name string) (string, bool) {
	_, rest, ok := strings.Cut(xml, "<"+name)
	if !ok {
		return "", false
	}
	_, rest, ok = strings.Cut(rest, ">")
	if !ok {
		return "", false
	}
	body, _, ok := strings.Cut(rest, "</"+name+">")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(body), true
}

func desktopXMLDataValue(xml string, name string) (string, bool) {
	_, rest, ok := strings.Cut(xml, `Name="`+name+`"`)
	if !ok {
		return "", false
	}
	_, rest, ok = strings.Cut(rest, ">")
	if !ok {
		return "", false
	}
	value, _, ok := strings.Cut(rest, "</Data>")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}
