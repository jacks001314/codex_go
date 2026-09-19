//go:build windows

package windowssandbox

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Desktop app context attributes (winbase.h). The policy keeps the initial
// child and its descendants inside the caller's package environment so they can
// launch executables from its protected directory.
const (
	procThreadAttributeDesktopAppPolicy                  = 0x00020012
	processCreationDesktopAppBreakawayDisableProcessTree = 2
	processCreationDesktopAppBreakawayOverride           = 4
)

func NewProcThreadAttributeList() (*ProcThreadAttributeList, error) {
	return NewProcThreadAttributeListWithCount(1)
}

func NewProcThreadAttributeListWithCount(attrCount uint32) (*ProcThreadAttributeList, error) {
	if attrCount == 0 {
		return nil, ErrInvalidRequest
	}
	impl, err := windows.NewProcThreadAttributeList(attrCount)
	if err != nil {
		return nil, err
	}
	return &ProcThreadAttributeList{impl: impl}, nil
}

func (l *ProcThreadAttributeList) SetHandleList(handles []uintptr) error {
	if l == nil || l.impl == nil {
		return ErrInvalidRequest
	}
	if len(handles) == 0 {
		return ErrInvalidRequest
	}
	l.handleList = append(l.handleList[:0], handles...)
	return l.container().Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&l.handleList[0]),
		uintptr(len(l.handleList))*unsafe.Sizeof(l.handleList[0]),
	)
}

func (l *ProcThreadAttributeList) SetPseudoconsole(handle uintptr) error {
	if l == nil || l.impl == nil || handle == 0 {
		return ErrInvalidRequest
	}
	value := windows.Handle(handle)
	err := l.container().Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		unsafe.Pointer(&value),
		unsafe.Sizeof(value),
	)
	runtime.KeepAlive(value)
	return err
}

// PreserveDesktopAppContext ports Rust's
// `ProcThreadAttributeList::preserve_desktop_app_context` (#46575): sandboxed
// descendants keep the caller's desktop app context so packaged processes can
// still launch children from their protected package directory.
func (l *ProcThreadAttributeList) PreserveDesktopAppContext() error {
	if l == nil || l.impl == nil {
		return ErrInvalidRequest
	}
	l.desktopAppPolicy = processCreationDesktopAppBreakawayDisableProcessTree |
		processCreationDesktopAppBreakawayOverride
	err := l.container().Update(
		procThreadAttributeDesktopAppPolicy,
		unsafe.Pointer(&l.desktopAppPolicy),
		unsafe.Sizeof(l.desktopAppPolicy),
	)
	runtime.KeepAlive(l)
	return err
}

func (l *ProcThreadAttributeList) WindowsList() *windows.ProcThreadAttributeList {
	if l == nil || l.impl == nil {
		return nil
	}
	return l.container().List()
}

func (l *ProcThreadAttributeList) Close() {
	if l == nil || l.impl == nil {
		return
	}
	l.container().Delete()
	l.impl = nil
	l.handleList = nil
}

func (l *ProcThreadAttributeList) container() *windows.ProcThreadAttributeListContainer {
	return l.impl.(*windows.ProcThreadAttributeListContainer)
}
