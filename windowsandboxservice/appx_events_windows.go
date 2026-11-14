//go:build windows

package windowsandboxservice

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var appxEventDLL = windows.NewLazySystemDLL("wevtapi.dll")
var appxEvtQuery = appxEventDLL.NewProc("EvtQuery")
var appxEvtNext = appxEventDLL.NewProc("EvtNext")
var appxEvtRender = appxEventDLL.NewProc("EvtRender")
var appxEvtContext = appxEventDLL.NewProc("EvtCreateRenderContext")
var appxEvtClose = appxEventDLL.NewProc("EvtClose")

// appxFailureEventIDs reads only numeric event IDs, never event message text.
// Diagnostics are best effort and cannot replace a deployment result.
func appxFailureEventIDs(activity windows.GUID) string {
	if activity == (windows.GUID{}) {
		return ""
	}
	channel, _ := windows.UTF16PtrFromString("Microsoft-Windows-AppXDeploymentServer/Operational")
	filter, _ := windows.UTF16PtrFromString(fmt.Sprintf("*[System[Correlation[@ActivityID='{%s}'] and Level=2]]", formatGUID(activity)))
	query, _, _ := appxEvtQuery.Call(0, uintptr(unsafe.Pointer(channel)), uintptr(unsafe.Pointer(filter)), 1)
	if query == 0 {
		return ""
	}
	defer appxEvtClose.Call(query)
	field, _ := windows.UTF16PtrFromString("Event/System/EventID")
	render, _, _ := appxEvtContext.Call(1, uintptr(unsafe.Pointer(&field)), 0)
	if render == 0 {
		return ""
	}
	defer appxEvtClose.Call(render)
	var ids []uint16
	for {
		var event uintptr
		var returned uint32
		ok, _, _ := appxEvtNext.Call(query, 1, uintptr(unsafe.Pointer(&event)), 0, 0, uintptr(unsafe.Pointer(&returned)))
		if ok == 0 || returned != 1 {
			break
		}
		// EVT_VARIANT: an eight-byte union followed by Count and Type.
		var value struct {
			Value uint64
			Count uint32
			Type  uint32
		}
		var used, properties uint32
		ok, _, _ = appxEvtRender.Call(render, event, 0, unsafe.Sizeof(value), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
		appxEvtClose.Call(event)
		if ok != 0 && properties == 1 && value.Type == 6 {
			ids = append(ids, uint16(value.Value))
		}
	}
	return formatAppxEventIDs(ids)
}

func formatAppxEventIDs(ids []uint16) string {
	unique := map[uint16]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	sorted := make([]int, 0, len(unique))
	for id := range unique {
		sorted = append(sorted, int(id))
	}
	sort.Ints(sorted)
	values := make([]string, len(sorted))
	for i, id := range sorted {
		values[i] = strconv.Itoa(id)
	}
	return strings.Join(values, ",")
}
