//go:build windows

package windowssandbox

import (
	"bytes"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func tokenGroupTestBuffer(t *testing.T, groups []tokenGroup) []byte {
	t.Helper()
	headerSize := int(unsafe.Offsetof(windows.Tokengroups{}.Groups))
	stride := int(unsafe.Sizeof(windows.SIDAndAttributes{}))
	sidOffset := headerSize + len(groups)*stride
	total := sidOffset
	for _, group := range groups {
		total += len(group.sid)
	}
	buffer := make([]byte, total)
	*(*uint32)(unsafe.Pointer(&buffer[0])) = uint32(len(groups))
	cursor := sidOffset
	for index, group := range groups {
		copy(buffer[cursor:], group.sid)
		entry := windows.SIDAndAttributes{
			Sid:        (*windows.SID)(unsafe.Pointer(&buffer[cursor])),
			Attributes: group.attributes,
		}
		*(*windows.SIDAndAttributes)(unsafe.Pointer(&buffer[headerSize+index*stride])) = entry
		cursor += len(group.sid)
	}
	return buffer
}

func TestDecodeTokenGroupsOwnsSIDsAndPreservesOrderLikeRust(t *testing.T) {
	worldSID, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid() error = %v", err)
	}
	sid := copySIDBytes(worldSID)
	expected := []tokenGroup{
		{sid: sid, attributes: 0x4},
		{sid: sid, attributes: 0x10},
		{sid: sid, attributes: windows.SE_GROUP_LOGON_ID},
	}
	buffer := tokenGroupTestBuffer(t, expected)
	decoded, err := decodeTokenGroups(buffer)
	if err != nil {
		t.Fatalf("decodeTokenGroups() error = %v", err)
	}
	if len(decoded) != len(expected) {
		t.Fatalf("decoded groups = %#v", decoded)
	}
	for index := range expected {
		if !bytes.Equal(decoded[index].sid, expected[index].sid) || decoded[index].attributes != expected[index].attributes {
			t.Fatalf("group %d = %#v, want %#v", index, decoded[index], expected[index])
		}
	}
	wantSID := append([]byte(nil), decoded[0].sid...)
	buffer[len(buffer)-1] = 0xff
	if !bytes.Equal(decoded[0].sid, wantSID) {
		t.Fatal("decoded SID aliased the source buffer")
	}
}

func TestDecodeTokenGroupsRejectsMalformedBuffersLikeRust(t *testing.T) {
	if _, err := decodeTokenGroups(nil); err == nil {
		t.Fatal("empty token group buffer accepted")
	}
	empty := tokenGroupTestBuffer(t, nil)
	if groups, err := decodeTokenGroups(empty); err != nil || len(groups) != 0 {
		t.Fatalf("empty groups = %#v, %v", groups, err)
	}
	truncatedCount := tokenGroupTestBuffer(t, nil)
	*(*uint32)(unsafe.Pointer(&truncatedCount[0])) = ^uint32(0)
	if _, err := decodeTokenGroups(truncatedCount); err == nil {
		t.Fatal("overflowing group count accepted")
	}

	worldSID, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid() error = %v", err)
	}
	group := tokenGroup{sid: copySIDBytes(worldSID), attributes: 0x4}
	buffer := tokenGroupTestBuffer(t, []tokenGroup{group})
	headerSize := int(unsafe.Offsetof(windows.Tokengroups{}.Groups))
	entry := *(*windows.SIDAndAttributes)(unsafe.Pointer(&buffer[headerSize]))
	sidOffset := (uintptr(unsafe.Pointer(entry.Sid)) - uintptr(unsafe.Pointer(&buffer[0])))
	buffer[sidOffset] = 2
	if _, err := decodeTokenGroups(buffer); err == nil {
		t.Fatal("malformed SID revision accepted")
	}
	buffer[sidOffset] = 1
	buffer[sidOffset+1] = 16
	if _, err := decodeTokenGroups(buffer); err == nil {
		t.Fatal("oversized SID accepted")
	}
	buffer[sidOffset+1] = 1
	for _, sid := range []*windows.SID{nil, (*windows.SID)(unsafe.Pointer(&buffer[len(buffer)-1]))} {
		*(*windows.SIDAndAttributes)(unsafe.Pointer(&buffer[headerSize])) = windows.SIDAndAttributes{Sid: sid, Attributes: 4}
		if _, err := decodeTokenGroups(buffer); err == nil {
			t.Fatalf("out-of-buffer SID %p accepted", sid)
		}
	}
}

func TestTokenGroupsForTokenHonorsCallerLimitLikeRust(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	if _, err := tokenGroupsForToken(token, 0); err == nil {
		t.Fatal("zero token group size limit accepted")
	}
	groups, err := tokenGroupsForToken(token, ^uint32(0))
	if err != nil {
		t.Fatalf("tokenGroupsForToken() error = %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("current token returned no groups")
	}
	for _, group := range groups {
		if group.attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			logon, err := getLogonSIDBytes(token)
			if err != nil || !bytes.Equal(logon, group.sid) {
				t.Fatalf("getLogonSIDBytes() = %x, %v, want %x", logon, err, group.sid)
			}
		}
	}
}
