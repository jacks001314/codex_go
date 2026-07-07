//go:build !windows

package windowssandbox

func NewProcThreadAttributeList() (*ProcThreadAttributeList, error) {
	return nil, unsupported("proc_thread_attr.new")
}

func NewProcThreadAttributeListWithCount(attrCount uint32) (*ProcThreadAttributeList, error) {
	return nil, unsupported("proc_thread_attr.new")
}

func (l *ProcThreadAttributeList) SetHandleList(handles []uintptr) error {
	return unsupported("proc_thread_attr.set_handle_list")
}

func (l *ProcThreadAttributeList) SetPseudoconsole(handle uintptr) error {
	return unsupported("proc_thread_attr.set_pseudoconsole")
}

func (l *ProcThreadAttributeList) Close() {
}
