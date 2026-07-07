package backends

import "codex_go/internal/sandbox/windowssandbox"

func RunElevatedBackend(req *windowssandbox.CaptureRequest) (*windowssandbox.CaptureResult, error) {
	if req == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	return windowssandbox.RunWindowsSandboxCaptureForPermissionProfileElevated(
		&windowssandbox.ElevatedSandboxProfileCaptureRequest{Capture: *req},
	)
}
