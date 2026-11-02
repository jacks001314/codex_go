//go:build !linux

package tea

// imagePasteFallsBackToText keeps the pre-existing non-Linux behaviour: any
// image-capture failure falls through to the text paste. Windows capture is
// unchanged by the Linux clipboard-image work; aligning its error split with
// Rust is a separate change.
func imagePasteFallsBackToText(error) bool {
	return true
}
