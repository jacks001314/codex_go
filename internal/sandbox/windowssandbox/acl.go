package windowssandbox

type ACLRequest struct {
	Path string
	SID  string
	Mask uint32
}
