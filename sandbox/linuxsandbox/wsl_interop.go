package linuxsandbox

import "os"

// wslInteropDir is WSL2's Windows-process interop socket directory.
const wslInteropDir = "/run/WSL"

func pathIsDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// appendWSLInteropMaskArgs hides WSL's Windows-process bridge after all
// filesystem binds. Without a fresh procfs the inherited /proc can alias the
// interop sockets through /proc/<pid>/root, so mask it as well (Rust #44286).
func appendWSLInteropMaskArgs(args []string, noProc bool, restrictedFilesystem bool, interopDir string) []string {
	if !restrictedFilesystem || !pathIsDirectory(interopDir) {
		return args
	}
	args = append(args, "--tmpfs", interopDir)
	if noProc {
		args = append(args, "--tmpfs", "/proc")
	}
	return args
}
