//go:build windows

package doctor

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// Rust c7a95f84b3 (#38827): query the supported Windows endpoint protection
// services through sc.exe. Exit code 1060 (ERROR_SERVICE_DOES_NOT_EXIST)
// means the product is not installed; any other failure leaves visibility
// incomplete.
const (
	endpointServiceDoesNotExist  = 1060
	endpointDefenderRunningState = "4"
)

var endpointWindowsScPath = func() string {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	if root == "" {
		root = `C:\Windows`
	}
	return root + `\System32\sc.exe`
}()

func endpointProtectionInspection() endpointInspection {
	inspection := endpointInspection{inspected: true}
	queries := []struct {
		product string
		service string
		defender bool
	}{
		{"CrowdStrike Falcon", "CSFalconService", false},
		{"BeyondTrust Privilege Management", "DefendpointService", false},
		{"Microsoft Defender", "WinDefend", true},
		{"SentinelOne", "SentinelAgent", false},
	}
	for _, query := range queries {
		output, exitCode := endpointServiceQuery(query.service)
		switch {
		case exitCode == 0 && query.defender:
			if endpointDefenderRunning(output) {
				inspection.products = append(inspection.products, query.product)
			}
		case exitCode == 0:
			inspection.products = append(inspection.products, query.product)
		case exitCode == endpointServiceDoesNotExist:
			// Service does not exist: product not installed.
		default:
			inspection.visibilityIncomplete = true
		}
	}
	return inspection
}

func endpointServiceQuery(service string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), endpointProductQueryTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, endpointWindowsScPath, "query", service).Output()
	if err != nil {
		var exitCode int
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return string(output), exitCode
	}
	return string(output), 0
}

func endpointDefenderRunning(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "STATE") {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) > 0 && fields[0] == endpointDefenderRunningState {
			return true
		}
	}
	return false
}
