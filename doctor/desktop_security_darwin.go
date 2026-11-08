//go:build darwin

package doctor

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Rust 55897425e8 cli/src/doctor/desktop/macos_security.rs: combine the
// Gatekeeper assessment of the installed bundle with the last 30 minutes of
// Apple system-policy log events, and report only actionable enforcement
// evidence. Every probe is bounded by a timeout and a 256 KiB output cap so a
// noisy unified log cannot stall or flood the doctor report.
const (
	macSecurityProbeTimeout     = 10 * time.Second
	macSecurityMaxOutputBytes   = 256 * 1024
	macSecurityLogWindow        = "30m"
	macSecurityGatekeeperDetail = "gatekeeper: accepted"
)

const macSecurityLogPredicate = "(process == 'syspolicyd' OR process == 'AppleSystemPolicy' " +
	"OR process BEGINSWITH 'XProtect' OR subsystem BEGINSWITH 'com.apple.syspolicy' " +
	"OR subsystem CONTAINS[c] 'XProtect' " +
	"OR subsystem == 'com.apple.security.assessment') AND " +
	"(eventMessage CONTAINS[c] 'codex' OR eventMessage CONTAINS[c] 'chatgpt' " +
	"OR eventMessage CONTAINS[c] '.plugin-appserver' " +
	"OR eventMessage CONTAINS[c] '100024' OR eventMessage CONTAINS[c] 'EMFILE' " +
	"OR eventMessage CONTAINS[c] 'ENFILE' " +
	"OR eventMessage CONTAINS[c] 'too many open files' " +
	"OR eventMessage CONTAINS[c] 'Unexpected Xprotect assessment')"

type macSecurityEvidence int

const (
	macSecurityClear macSecurityEvidence = iota
	macSecurityAudit
	macSecurityExhausted
	macSecurityBlocked
	macSecurityMalware
	macSecurityUnavailable
)

// desktopSecurityEnforcement ports Rust's macos_security::collect. The
// installed bundle gates the assessment; a host without one never reaches this
// probe because desktopChecks omits the whole Desktop App group.
func desktopSecurityEnforcement(app *desktopApp) *DoctorCheck {
	// Rust only reaches macos_security::collect after a successful installed_app
	// probe, and that probe carries the bundle path. An unreadable installation
	// therefore reports only desktop.app.version.
	if app == nil || strings.TrimSpace(app.bundle) == "" {
		return nil
	}
	// Rust collects both probes with tokio::join!, so the gatekeeper assessment
	// and the unified-log scan run concurrently instead of costing up to 2x the
	// 10 s probe timeout.
	var gatekeeper, events macSecurityEvidence
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		gatekeeper = inspectMacGatekeeper(app.bundle)
	}()
	go func() {
		defer wait.Done()
		events = inspectMacSecurityEvents()
	}()
	wait.Wait()
	return macSecurityEnforcementCheck(gatekeeper, events)
}

func inspectMacGatekeeper(bundle string) macSecurityEvidence {
	if strings.TrimSpace(bundle) == "" {
		return macSecurityUnavailable
	}
	stdout, stderr, success, truncated := runMacSecurityCommand(
		"/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=2", bundle,
	)
	if truncated {
		return macSecurityUnavailable
	}
	_ = stdout
	return classifyMacGatekeeper(success, stderr)
}

func inspectMacSecurityEvents() macSecurityEvidence {
	stdout, _, success, truncated := runMacSecurityCommand(
		"/usr/bin/log", "show", "--last", macSecurityLogWindow, "--info",
		"--style", "compact", "--predicate", macSecurityLogPredicate,
	)
	if !success {
		return macSecurityUnavailable
	}
	evidence := classifyMacSecurityEvents(stdout)
	if truncated && evidence < macSecurityExhausted {
		return macSecurityUnavailable
	}
	return evidence
}

func runMacSecurityCommand(name string, args ...string) (stdout string, stderr string, success bool, truncated bool) {
	ctx, cancel := context.WithTimeout(context.Background(), macSecurityProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	stdoutBuffer := &boundedOutput{limit: macSecurityMaxOutputBytes}
	stderrBuffer := &boundedOutput{limit: macSecurityMaxOutputBytes}
	command.Stdout = stdoutBuffer
	command.Stderr = stderrBuffer
	err := command.Run()
	if ctx.Err() != nil {
		return stdoutBuffer.String(), stderrBuffer.String(), false, true
	}
	return stdoutBuffer.String(), stderrBuffer.String(), err == nil, stdoutBuffer.truncated || stderrBuffer.truncated
}

func classifyMacGatekeeper(success bool, stderr string) macSecurityEvidence {
	if success {
		return macSecurityClear
	}
	message := strings.ToLower(stderr)
	if strings.Contains(message, "rejected") || strings.Contains(message, "no usable signature") {
		return macSecurityBlocked
	}
	return macSecurityUnavailable
}

func classifyMacSecurityEvents(output string) macSecurityEvidence {
	evidence := macSecurityClear
	for _, line := range strings.Split(output, "\n") {
		line = strings.ToLower(line)
		containsAny := func(values ...string) bool {
			for _, value := range values {
				if strings.Contains(line, value) {
					return true
				}
			}
			return false
		}
		if !containsAny(
			"com.openai.codex",
			"codex.app",
			"chatgpt.app",
			".plugin-appserver",
			"codex-command-runner",
		) || containsAny("not blocked", "not denied") {
			continue
		}
		event := macSecurityClear
		switch {
		case containsAny("100024", "emfile", "enfile", "too many open files"):
			event = macSecurityExhausted
		case containsAny("would block", "would deny"):
			event = macSecurityAudit
		case containsAny(
			"xp_malware_detected",
			"xp_malware_remediated",
			"malware detected",
			"malware blocked",
			"malware removed",
			"remediat",
		):
			event = macSecurityMalware
		case containsAny(
			"blocked",
			"denied",
			"rejected",
			"notarization failed",
			"signature invalid",
			"execution prevented",
			"unexpected xprotect assessment",
			"damaged and",
		):
			event = macSecurityBlocked
		case containsAny("audit"):
			event = macSecurityAudit
		}
		if event > evidence {
			evidence = event
		}
	}
	return evidence
}

func macSecurityEnforcementCheck(gatekeeper, events macSecurityEvidence) *DoctorCheck {
	const id = "desktop.security.enforcement"
	type outcome struct {
		status  CheckStatus
		summary string
		remedy  string
	}
	var result outcome
	switch {
	case events == macSecurityMalware:
		result = outcome{
			CheckStatusFail,
			"macos XProtect blocked or remediated the desktop application",
			"collect the XProtect detection and ask your security administrator to review the official Codex installation",
		}
	case gatekeeper == macSecurityBlocked:
		result = outcome{
			CheckStatusFail,
			"macos gatekeeper rejected the desktop application",
			"ask your security administrator to review the application policy",
		}
	case events == macSecurityBlocked:
		result = outcome{
			CheckStatusFail,
			"a recent macos security event blocked the desktop application",
			"ask your security administrator to review the matching prevention event",
		}
	case events == macSecurityExhausted:
		result = outcome{
			CheckStatusWarning,
			"macos system-policy diagnostics indicate file descriptor exhaustion",
			"restart your Mac, retry Codex once, and contact support if the problem returns",
		}
	case events == macSecurityAudit:
		result = outcome{
			CheckStatusWarning,
			"recent desktop security events are audit-only",
			"ask your security administrator to verify the application policy",
		}
	case gatekeeper == macSecurityUnavailable:
		result = outcome{
			CheckStatusWarning,
			"the desktop security assessment was unavailable",
			"check access to macos gatekeeper diagnostics",
		}
	case events == macSecurityUnavailable:
		result = outcome{
			CheckStatusWarning,
			"recent macos security enforcement history was unavailable",
			"check access to macos unified security logs and rerun codex doctor",
		}
	default:
		return NewCheck(id, "desktop", CheckStatusOK, "the desktop application passed available macos security assessments").
			Detail(macSecurityGatekeeperDetail)
	}
	return NewCheck(id, "desktop", result.status, result.summary).Remediate(result.remedy)
}

// boundedOutput keeps at most limit bytes and records truncation instead of
// growing with the command's output.
type boundedOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = b.truncated || len(data) > 0
		return len(data), nil
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || len(data) > 0
		return len(data), nil
	}
	total := len(data)
	if len(data) > remaining {
		b.truncated = true
		data = data[:remaining]
	}
	if _, err := b.buffer.Write(data); err != nil {
		return 0, err
	}
	return total, nil
}

func (b *boundedOutput) String() string {
	return b.buffer.String()
}
