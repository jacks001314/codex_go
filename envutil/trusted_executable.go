package envutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TrustedExecutable finds an installed helper without consulting PATH,
// PATHEXT, or the working directory (Rust codex-utils-path::system_executable,
// #42324). Only OS and conventional package-manager locations are trusted;
// custom installations fall back to no helper. The returned path is resolved.
func TrustedExecutable(name string) (string, bool) {
	return trustedExecutableIn(name, trustedExecutableDirectories(), trustedInstallationRoots())
}

// TrustedSystemPath returns a PATH value built only from trusted installation
// directories for automatic child processes, or "" when none are trusted.
// It must not be used for user-requested tools.
func TrustedSystemPath() (string, bool) {
	directories := canonicalDirectories(trustedExecutableDirectories(), trustedInstallationRoots())
	if len(directories) == 0 {
		return "", false
	}
	return strings.Join(directories, string(os.PathListSeparator)), true
}

// trustedExecutableIn is the testable core: it searches directories, requiring
// the resolved executable to live inside directories or roots.
func trustedExecutableIn(name string, directories []string, roots []string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || filepath.Base(name) != name {
		// Do not let callers escape the installation directories.
		return "", false
	}
	candidateName := name + executableSuffix()
	for _, directory := range directories {
		directory = strings.TrimSpace(directory)
		if directory == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(directory, candidateName))
		if err != nil {
			continue
		}
		if !withinAny(resolved, directories) && !withinAny(resolved, roots) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			continue
		}
		if !isExecutableFile(resolved) {
			continue
		}
		return resolved, true
	}
	return "", false
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func withinAny(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		if path == root {
			return true
		}
		if strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func trustedInstallationRoots() []string {
	switch runtime.GOOS {
	case "windows":
		return windowsInstallationRoots()
	default:
		return unixInstallationRoots()
	}
}

func trustedExecutableDirectories() []string {
	switch runtime.GOOS {
	case "windows":
		directories := make([]string, 0, 12)
		for _, root := range windowsInstallationRoots() {
			directories = append(directories,
				filepath.Join(root, "Git", "cmd"),
				filepath.Join(root, "Git", "mingw64", "bin"),
				filepath.Join(root, "Git", "usr", "bin"),
				root,
			)
		}
		return directories
	default:
		directories := []string{
			"/opt/homebrew/bin",
			"/usr/local/bin",
			"/opt/local/bin",
			"/Library/Developer/CommandLineTools/usr/bin",
			"/Applications/Xcode.app/Contents/Developer/usr/bin",
			"/usr/bin",
			"/bin",
			"/usr/sbin",
			"/sbin",
		}
		if runtime.GOOS == "linux" && isWSL() {
			directories = append(directories, "/mnt/c/Windows/System32")
		}
		return directories
	}
}

func unixInstallationRoots() []string {
	return []string{
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
		"/usr/local",
		"/opt/homebrew",
		"/opt/local",
		"/Library/Developer/CommandLineTools",
		"/Applications/Xcode.app/Contents/Developer",
		"/nix/store",
		"/mnt/c/Windows/System32",
	}
}

func windowsInstallationRoots() []string {
	roots := []string{}
	for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			roots = append(roots, value)
		}
	}
	if systemRoot := strings.TrimSpace(os.Getenv("SystemRoot")); systemRoot != "" {
		roots = append(roots, filepath.Join(systemRoot, "System32"))
	}
	return roots
}

func canonicalDirectories(directories []string, roots []string) []string {
	out := make([]string, 0, len(directories))
	seen := map[string]bool{}
	for _, directory := range directories {
		resolved, err := filepath.EvalSymlinks(directory)
		if err != nil || !withinAny(resolved, roots) {
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != "" || strings.TrimSpace(os.Getenv("WSL_INTEROP")) != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}
