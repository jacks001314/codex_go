package appserver

import (
	"context"
	"log/slog"
)

// retireRemoteControlForAuthChange tears down the remote-control session after a
// logout or account switch so its relay, queued-RPC, client, enrollment, and
// pairing state cannot carry over to a different authentication owner; remote
// control stays disabled until the user enables it again (Rust #44341).
func (r *RuntimeRouter) retireRemoteControlForAuthChange(ctx context.Context) {
	if r == nil || r.services.Remote == nil {
		return
	}
	if err := r.services.Remote.RetireForAuthChange(ctx); err != nil {
		slog.Warn("failed to retire remote control session after auth change", "error", err)
	}
	r.notifyRemoteControlStatusChanged(r.services.Remote.StatusChanged())
}
