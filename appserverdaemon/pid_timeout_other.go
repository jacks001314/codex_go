//go:build !darwin

package appserverdaemon

import "time"

// PIDStartTimeout is the readiness budget for a detached managed process.
const PIDStartTimeout = 10 * time.Second
