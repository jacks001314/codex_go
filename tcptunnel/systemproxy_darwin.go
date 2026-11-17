//go:build darwin

package tcptunnel

import (
	"net"
	"os/exec"
	"strings"
)

// systemProxySetting mirrors hyper-util's macOS matcher: read the HTTPS proxy
// from `scutil --proxy` when the environment is silent.
func systemProxySetting(noProxy string) (string, string) {
	output, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return "", noProxy
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	if fields["HTTPSEnable"] != "1" || fields["HTTPSProxy"] == "" {
		return "", noProxy
	}
	proxy := fields["HTTPSProxy"]
	if port := fields["HTTPSPort"]; port != "" {
		proxy = net.JoinHostPort(proxy, port)
	}
	return proxy, noProxy
}
