//go:build !windows && !linux

package idecontext

import (
	"io"
	"time"
)

func connectIDEContext(_ string, _ string, _ time.Time) (io.ReadWriteCloser, error) {
	return nil, &IdeContextError{Kind: IdeContextErrorUnsupportedPlatform}
}
