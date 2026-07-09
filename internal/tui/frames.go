package tui

type FrameRequest struct {
	Immediate bool
}

func ImmediateFrameRequest() FrameRequest {
	return FrameRequest{Immediate: true}
}
