//go:build !windows

package voicehost

import "github.com/ebitengine/purego"

// openDynamicLibrary loads a shared library and returns a symbol resolver.
func openDynamicLibrary(path string) (uintptr, symbolResolver, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return 0, nil, err
	}
	resolve := func(name string) (uintptr, error) {
		return purego.Dlsym(handle, name)
	}
	return handle, resolve, nil
}

// closeDynamicLibrary releases a loaded library.
func closeDynamicLibrary(handle uintptr) error {
	if handle == 0 {
		return nil
	}
	return purego.Dlclose(handle)
}
