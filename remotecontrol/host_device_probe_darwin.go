//go:build darwin

package remotecontrol

import (
	"context"
	"os/exec"
	"time"
)

const hostDeviceProbeTimeout = 2 * time.Second

// detectHostDeviceKind runs the macOS hardware profile probe and reports
// "mac_mini" when the machine name is exactly "Mac mini" (Rust #38840).
func detectHostDeviceKind() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), hostDeviceProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/sbin/system_profiler",
		"-detailLevel", "mini", "SPHardwareDataType", "-json").Output()
	if err != nil {
		return "", false
	}
	return hostDeviceKindFromProfile(output)
}
