package doctor

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Desktop diagnostics mirror Rust cli/src/doctor/desktop.rs (#39060/#39067):
// the installed desktop application, whether one of its sessions is running,
// the newest app-server handshake and the platform security enforcement scan.
// A host without the desktop application reports no desktop rows at all, the
// way Rust's collect() returns None.

const (
	desktopHandshakeCheckID    = "desktop.app_server.handshake"
	desktopMaxDirectoryEntries = 256
	desktopMaxLogFiles         = 64
	desktopMaxSessionLogFiles  = 8
	desktopMaxLogSegmentBytes  = 10 * 1024 * 1024
)

type desktopApp struct {
	identity string
	version  string
}

var (
	desktopAppProbe      = func() (*desktopApp, error) { return desktopInstalledApp() }
	desktopLogRootProbe  = func(identity string) string { return desktopLogRoot(identity) }
	desktopRunningProbe  = func(app *desktopApp, pid int) bool { return desktopApplicationRunning(app, pid) }
	desktopSecurityProbe = func() *DoctorCheck { return desktopSecurityEnforcement() }
)

func desktopChecks() []*DoctorCheck {
	app, err := desktopAppProbe()
	if err != nil {
		checks := []*DoctorCheck{desktopUnavailableCheck("desktop.app.version")}
		if security := desktopSecurityProbe(); security != nil {
			checks = append(checks, security)
		}
		return checks
	}
	if app == nil {
		return nil
	}
	root := desktopLogRootProbe(app.identity)
	running := false
	handshake := desktopStoppedCheck()
	if root != "" {
		running, handshake = inspectDesktopSession(
			root+`\`+time.Now().Format("2006/01/02"),
			func(pid int) bool { return desktopRunningProbe(app, pid) },
		)
	}
	logDirectory := "unavailable"
	if root != "" {
		logDirectory = redactedDesktopPath(root)
	}
	checks := []*DoctorCheck{
		NewCheck("desktop.app.version", "desktop", CheckStatusOK, "the desktop application is installed").
			Detail("version: " + app.version).
			Detail("running: " + strconv.FormatBool(running)).
			Detail("log directory: " + logDirectory),
		handshake,
	}
	if security := desktopSecurityProbe(); security != nil {
		checks = append(checks, security)
	}
	return checks
}

func desktopUnavailableCheck(id string) *DoctorCheck {
	return NewCheck(id, "desktop", CheckStatusWarning, "the desktop application installation could not be inspected").
		Remediate("restore desktop diagnostic access and rerun codex doctor")
}

func desktopStoppedCheck() *DoctorCheck {
	return NewCheck(desktopHandshakeCheckID, "desktop", CheckStatusOK, "the desktop application is not running")
}

func desktopNoHandshakeCheck() *DoctorCheck {
	return NewCheck(desktopHandshakeCheckID, "desktop", CheckStatusOK, "no desktop app-server handshake was recorded")
}

func desktopHandshakeOutcomeCheck(success bool) *DoctorCheck {
	if success {
		return NewCheck(desktopHandshakeCheckID, "desktop", CheckStatusOK, "the desktop app-server initialized successfully")
	}
	return NewCheck(desktopHandshakeCheckID, "desktop", CheckStatusFail, "the desktop app-server failed to initialize")
}

// desktopLogRoot is Rust's desktop_log_root: the per-user log directory the
// desktop application writes its session logs into.
func desktopLogRoot(identity string) string {
	switch runtime.GOOS {
	case "darwin":
		home := os.Getenv("HOME")
		if home == "" {
			return ""
		}
		return home + `/Library/Logs/` + identity
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			if home := os.Getenv("USERPROFILE"); home != "" {
				local = home + `\AppData\Local`
			}
		}
		if local == "" {
			return ""
		}
		return local + `\Codex/Logs`
	default:
		return ""
	}
}

// redactedDesktopPath renders a log directory the way Rust's redacted_path does:
// under the user's home it becomes $HOME-relative, otherwise only the last
// path element survives.
func redactedDesktopPath(path string) string {
	for _, variable := range []string{"HOME", "USERPROFILE"} {
		home := strings.TrimSuffix(os.Getenv(variable), `\`)
		if home == "" {
			continue
		}
		if strings.HasPrefix(path, home+`\`) || strings.HasPrefix(path, home+`/`) {
			return "$HOME" + strings.TrimPrefix(path, home)
		}
	}
	if index := strings.LastIndexAny(path, `\/`); index >= 0 {
		return path[index+1:]
	}
	return path
}

type desktopLog struct {
	path      string
	sessionID string
	processID int
	modified  time.Time
}

// discoverDesktopLogs lists the desktop session logs in a day directory the way
// Rust does: only codex-desktop-<session>-<pid>-t<timestamp>.log files, newest
// first, capped at 64.
func discoverDesktopLogs(directory string) []desktopLog {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	logs := make([]desktopLog, 0, len(entries))
	for index, entry := range entries {
		if index >= desktopMaxDirectoryEntries {
			break
		}
		if entry.IsDir() {
			continue
		}
		session, pid, ok := parseDesktopLogName(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		logs = append(logs, desktopLog{
			path:      directory + `\` + entry.Name(),
			sessionID: session,
			processID: pid,
			modified:  info.ModTime(),
		})
	}
	for i := 1; i < len(logs); i++ {
		for j := i; j > 0 && logs[j].modified.After(logs[j-1].modified); j-- {
			logs[j], logs[j-1] = logs[j-1], logs[j]
		}
	}
	if len(logs) > desktopMaxLogFiles {
		logs = logs[:desktopMaxLogFiles]
	}
	return logs
}

func parseDesktopLogName(name string) (string, int, bool) {
	stripped, ok := strings.CutPrefix(name, "codex-desktop-")
	if !ok {
		return "", 0, false
	}
	stripped, ok = strings.CutSuffix(stripped, ".log")
	if !ok {
		return "", 0, false
	}
	sessionAndPID, _, ok := strings.Cut(stripped, "-t")
	if !ok {
		return "", 0, false
	}
	separator := strings.LastIndex(sessionAndPID, "-")
	if separator < 0 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(sessionAndPID[separator+1:])
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	return sessionAndPID[:separator], pid, true
}

// inspectDesktopSession reports whether a running desktop process still owns one
// of the day's logs and, if so, the newest handshake of that session.
func inspectDesktopSession(directory string, applicationRunning func(pid int) bool) (bool, *DoctorCheck) {
	logs := discoverDesktopLogs(directory)
	session := -1
	for index := range logs {
		if applicationRunning(logs[index].processID) {
			session = index
			break
		}
	}
	if session < 0 {
		return false, desktopStoppedCheck()
	}
	handshake, ok := latestDesktopHandshake(logs, logs[session].sessionID)
	if !ok {
		return true, desktopNoHandshakeCheck()
	}
	return true, desktopHandshakeOutcomeCheck(handshake.success)
}

type desktopHandshake struct {
	timestamp time.Time
	success   bool
}

type desktopLogEvent struct {
	context   bool
	local     bool
	handshake bool
	timestamp time.Time
	success   bool
	websocket bool
}

func latestDesktopHandshake(logs []desktopLog, sessionID string) (desktopHandshake, bool) {
	var latest desktopHandshake
	found := false
	used := 0
	for _, log := range logs {
		if log.sessionID != sessionID {
			continue
		}
		if used >= desktopMaxSessionLogFiles {
			break
		}
		used++
		contents, err := readDesktopLogSegment(log.path)
		if err != nil {
			continue
		}
		localWebsocket := false
		for _, line := range strings.Split(string(contents), "\n") {
			event, ok := parseDesktopLogLine(line)
			if !ok {
				continue
			}
			if event.context {
				localWebsocket = event.local
				continue
			}
			if event.websocket && !localWebsocket {
				continue
			}
			if found && event.timestamp.Before(latest.timestamp) {
				continue
			}
			latest = desktopHandshake{timestamp: event.timestamp, success: event.success}
			found = true
		}
	}
	return latest, found
}

func readDesktopLogSegment(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := make([]byte, desktopMaxLogSegmentBytes)
	read, err := file.Read(buffer)
	if err != nil && read == 0 {
		return nil, err
	}
	return buffer[:read], nil
}

func parseDesktopLogLine(line string) (desktopLogEvent, bool) {
	line = strings.TrimSuffix(line, "\r")
	stamp, rest, ok := strings.Cut(line, " ")
	if !ok {
		return desktopLogEvent{}, false
	}
	timestamp, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return desktopLogEvent{}, false
	}
	_, message, ok := strings.Cut(rest, " [AppServerConnection] ")
	if !ok {
		return desktopLogEvent{}, false
	}
	if fields, ok := strings.CutPrefix(message, "Starting app-server connection "); ok {
		return desktopLogEvent{context: true, local: desktopLogField(fields, "hostId") == "local" && desktopLogField(fields, "transport") == "websocket"}, true
	}
	if fields, ok := strings.CutPrefix(message, "app_server_connection.state_changed "); ok {
		return desktopLogEvent{context: true, local: desktopLogField(fields, "hostId") == "local" && desktopLogField(fields, "transport") == "websocket"}, true
	}
	fields, ok := strings.CutPrefix(message, "initialize_handshake_result ")
	if !ok {
		return desktopLogEvent{}, false
	}
	var success bool
	switch desktopLogField(fields, "outcome") {
	case "success":
		success = true
	case "failure":
		success = false
	default:
		return desktopLogEvent{}, false
	}
	var websocket bool
	switch desktopLogField(fields, "transportKind") {
	case "stdio":
		websocket = false
	case "websocket":
		websocket = true
	default:
		return desktopLogEvent{}, false
	}
	return desktopLogEvent{handshake: true, timestamp: timestamp, success: success, websocket: websocket}, true
}

func desktopLogField(fields string, name string) string {
	parts := strings.Fields(fields)
	for index := len(parts) - 1; index >= 0; index-- {
		key, value, ok := strings.Cut(parts[index], "=")
		if ok && key == name {
			return value
		}
	}
	return ""
}
