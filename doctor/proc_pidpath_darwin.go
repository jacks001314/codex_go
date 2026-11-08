//go:build darwin && cgo

package doctor

/*
#include <libproc.h>
#include <stdlib.h>
*/
import "C"

import (
	"strings"
	"unsafe"
)

// darwinProcessExecutablePath resolves one live process's executable with
// libproc's proc_pidpath, the same call Rust's platform::process_executable_path
// uses. It returns false when the process is gone or the path is not readable.
func darwinProcessExecutablePath(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	buffer := make([]byte, int(C.PROC_PIDPATHINFO_MAXSIZE))
	written := C.proc_pidpath(C.int(pid), unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer)))
	if written <= 0 {
		return "", false
	}
	return strings.TrimRight(string(buffer[:int(written)]), "\x00"), true
}
