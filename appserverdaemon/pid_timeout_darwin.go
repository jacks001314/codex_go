//go:build darwin

package appserverdaemon

import "time"

// PIDStartTimeout accounts for background CPU and I/O scheduling on macOS.
const PIDStartTimeout = 30 * time.Second
