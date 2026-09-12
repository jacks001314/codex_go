//go:build windows

package voicehost

import "golang.org/x/sys/windows"

// openDynamicLibrary loads a shared library and returns a symbol resolver.
// Windows has no dlopen, so the loader uses the platform API directly.
func openDynamicLibrary(path string) (uintptr, symbolResolver, error) {
	handle, err := windows.LoadLibrary(path)
	if err != nil {
		return 0, nil, err
	}
	resolve := func(name string) (uintptr, error) {
		address, err := windows.GetProcAddress(handle, name)
		if err != nil {
			return 0, err
		}
		return address, nil
	}
	return uintptr(handle), resolve, nil
}

// closeDynamicLibrary releases a loaded library so its file is no longer
// mapped, which Windows requires before the file can be replaced.
func closeDynamicLibrary(handle uintptr) error {
	if handle == 0 {
		return nil
	}
	return windows.FreeLibrary(windows.Handle(handle))
}
