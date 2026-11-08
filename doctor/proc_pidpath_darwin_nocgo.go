//go:build darwin && !cgo

package doctor

// darwinProcessExecutablePath needs libproc through cgo; a CGO-disabled macOS
// build keeps the desktop session probe conservative and reports no running
// application rather than guessing from a partial command line.
func darwinProcessExecutablePath(int) (string, bool) {
	return "", false
}
