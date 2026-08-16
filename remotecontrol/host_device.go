package remotecontrol

import (
	"encoding/json"
	"sync"
)

// RemoteControlHostDeviceKindHeader mirrors Rust's
// REMOTE_CONTROL_HOST_DEVICE_KIND_HEADER (app-server-transport, #38840): it is
// sent on remote-control WebSocket handshakes when the host device can be
// identified.
const RemoteControlHostDeviceKindHeader = "x-codex-host-device-kind"

const macMiniHostDeviceKind = "mac_mini"

var (
	hostDeviceKindMu     sync.Mutex
	hostDeviceKindValue  string
	hostDeviceKindCached bool
)

// HostDeviceKind returns the remote-control host device kind for this
// machine, or "" when detection fails or the platform is unsupported. Like
// Rust's host_device_kind (#38840), successful detection results are cached
// while failed probes are retried on the next call.
func HostDeviceKind() string {
	hostDeviceKindMu.Lock()
	defer hostDeviceKindMu.Unlock()
	if hostDeviceKindCached {
		return hostDeviceKindValue
	}
	value, ok := detectHostDeviceKind()
	if ok {
		hostDeviceKindValue = value
		hostDeviceKindCached = true
	}
	return value
}

type macHardwareProfile struct {
	Hardware []macHardware `json:"SPHardwareDataType"`
}

type macHardware struct {
	MachineName string `json:"machine_name"`
}

// hostDeviceKindFromProfile parses the system_profiler JSON output and
// reports "mac_mini" when the machine name is exactly "Mac mini" (Rust
// host_device_kind_from_profile).
func hostDeviceKindFromProfile(profile []byte) (string, bool) {
	var parsed macHardwareProfile
	if err := json.Unmarshal(profile, &parsed); err != nil {
		return "", false
	}
	if len(parsed.Hardware) > 0 && parsed.Hardware[0].MachineName == "Mac mini" {
		return macMiniHostDeviceKind, true
	}
	return "", true
}
