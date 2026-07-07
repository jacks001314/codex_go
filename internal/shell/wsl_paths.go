package shell

import (
	"os"
	"strings"
)

func IsWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func WinPathToWSL(path string) (string, bool) {
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return "", false
	}
	drive := path[0]
	if drive >= 'A' && drive <= 'Z' {
		drive = drive + ('a' - 'A')
	} else if drive < 'a' || drive > 'z' {
		return "", false
	}
	tail := strings.ReplaceAll(path[3:], "\\", "/")
	if tail == "" {
		return "/mnt/" + string(drive), true
	}
	return "/mnt/" + string(drive) + "/" + tail, true
}

func NormalizeForWSL(path string) string {
	if !IsWSL() {
		return path
	}
	if mapped, ok := WinPathToWSL(path); ok {
		return mapped
	}
	return path
}
