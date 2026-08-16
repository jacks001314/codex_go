//go:build darwin

package doctor

import (
	"context"
	"os/exec"
	"strings"
)

// Rust c7a95f84b3 (#38827): inspect system extensions through
// systemextensionsctl and match supported products by signing team and bundle
// identifier.
var macEndpointProducts = []struct {
	name       string
	signer     string
	identifier string
}{
	{"CrowdStrike Falcon", "X9E956P446", "com.crowdstrike.falcon.Agent"},
	{"BeyondTrust Privilege Management", "2ZS8T6NYB8", "com.beyondtrust.endpointsecurity"},
	{"Microsoft Defender", "UBF8T346G9", "com.microsoft.wdav.epsext"},
	{"SentinelOne", "4AYE5J54KN", "com.sentinelone.network-monitoring"},
	{"Jamf Protect", "483DWKW443", "com.jamf.protect.security-extension"},
}

func endpointProtectionInspection() endpointInspection {
	inspection := endpointInspection{inspected: true}
	ctx, cancel := context.WithTimeout(context.Background(), endpointProductQueryTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/systemextensionsctl", "list").Output()
	if err != nil {
		inspection.visibilityIncomplete = true
		return inspection
	}
	if len(output) > endpointProductOutputLimit {
		inspection.visibilityIncomplete = true
		return inspection
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "*" || fields[1] != "*" {
			continue
		}
		signer := fields[2]
		bundle := fields[3]
		for _, product := range macEndpointProducts {
			if signer == product.signer && bundle == product.identifier {
				inspection.products = append(inspection.products, product.name)
			}
		}
	}
	return inspection
}
