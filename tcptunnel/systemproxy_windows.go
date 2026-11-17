//go:build windows

package tcptunnel

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxySetting mirrors hyper-util's Windows matcher: the current user's
// WinINet settings apply when the environment is silent. WinINet may store
// per-scheme endpoints (`http=host:port;https=host:port`), so the HTTPS entry
// is selected when present.
func systemProxySetting(noProxy string) (string, string) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", noProxy
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return "", noProxy
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		return "", noProxy
	}
	proxy := strings.TrimSpace(server)
	if strings.Contains(server, "=") {
		proxy = ""
		for _, entry := range strings.Split(server, ";") {
			name, value, ok := strings.Cut(entry, "=")
			if ok && strings.EqualFold(strings.TrimSpace(name), "https") {
				proxy = strings.TrimSpace(value)
				break
			}
		}
		if proxy == "" {
			return "", noProxy
		}
	}
	if noProxy == "" {
		if override, _, err := key.GetStringValue("ProxyOverride"); err == nil {
			entries := strings.Split(override, ";")
			cleaned := make([]string, 0, len(entries))
			for _, entry := range entries {
				entry = strings.TrimSpace(entry)
				entry = strings.ReplaceAll(entry, "*.", "")
				cleaned = append(cleaned, entry)
			}
			noProxy = strings.Join(cleaned, ",")
		}
	}
	return proxy, noProxy
}
